package infra

// The managed templates checkout.
//
// The binary and the Terraform templates ship from ONE tag, and that is not an
// administrative convenience: the chart mapping compiled into this binary
// (chartmap.go) and the helm_values expressions in the HCL are two halves of one
// contract, and the test that keeps them agreeing runs against a single tree.
// Pairing a binary with templates from another commit is how the same product
// comes out one shape in shared mode and another in dedicated.
//
// So a checkout this tool creates is pinned to the tag matching the binary, and
// never to a branch. An operator who wants a moving target points --repo at their
// own clone; that one is theirs and is left alone.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// defaultTemplatesRepoURL is cloned over HTTPS rather than SSH because the
// repository is public: a client running this in their own account has no deploy
// key, and requiring one would make the managed checkout useless exactly where it
// helps most.
const defaultTemplatesRepoURL = "https://github.com/LerianStudio/lerian-terraform-foundation.git"

// TemplatesMinRef is the OLDEST tag of lerian-terraform-foundation this build is
// known to work with. It is a floor, not a pin.
//
// The CLI and the templates live in different repositories and release on their own
// cadence. An exact pin made that honest — one binary, one tag — and it also made
// the CLI unreleasable until the templates published the tag its source named, and
// left an operator who wanted a newer templates release waiting for a CLI build that
// merely renamed a constant. Neither is worth what the pin bought.
//
// So the tag is the operator's: `init --clone` and `--sync` take it as
// --templates-ref and there is no default. What stays here is the one thing the
// operator cannot know — the oldest HCL the chart mapping compiled into this binary
// (chartmap.go) was written against. CI proves it by cloning this tag and running
// the compatibility test against it. Below this the shapes genuinely differ and the
// CLI says so; at or above it, the contract is forward-compatible by convention and
// a break is a bug in whichever side broke it.
//
// v1.6.0 is where the AWS v2 layout starts — examples/aws/_modules with one root
// per product and per shared engine. Everything before it holds one directory per
// resource, which this tool cannot drive at all, so the floor is not a matter of
// taste: below it there is nothing to read.
//
// It is a constant in the source and not an ldflag: which templates a build
// understands is a property of the code, decided when the code is written.
const TemplatesMinRef = "v1.6.0"

// TemplatesRepoEnv overrides where the templates are cloned from.
//
// It exists for the deployments that cannot reach github.com: an air-gapped client
// with an internal mirror, or an organisation that vendors the templates into its
// own git server. Those are ordinary BYOC situations, and without an override the
// managed checkout would be unavailable to exactly the clients most likely to need
// a supported path. It is a variable rather than a build flag so a mirror does not
// require rebuilding the binary.
const TemplatesRepoEnv = "LERIAN_TEMPLATES_REPO"

// TemplatesRepoURL is where the templates are cloned from.
func TemplatesRepoURL() string {
	if override := strings.TrimSpace(os.Getenv(TemplatesRepoEnv)); override != "" {
		return override
	}
	return defaultTemplatesRepoURL
}

// managedCheckoutRel is where a managed checkout lives, relative to the home
// directory.
//
// Two decisions in one path:
//
// NOT HIDDEN. ~/lerian, not ~/.lerian. A dotfile directory says "tooling internals,
// do not look", and this is the opposite: it is a real git checkout the operator is
// meant to be able to open, read, fork and run terraform in by hand. Hiding it
// would tell them not to.
//
// NO VERSION IN IT. The operator's configuration — environments.conf and every
// envs/<env>.tfvars — is gitignored and therefore lives INSIDE the checkout. A
// versioned directory would orphan all of it on every upgrade, while
// `git checkout <tag>` in a fixed one leaves untracked and ignored files untouched.
//
// The leaf keeps the REPOSITORY's own name, so what is on disk matches what
// `git clone` of that URL would have produced and what every doc and error message
// calls it.
var managedCheckoutRel = filepath.Join("lerian", "lerian-terraform-foundation")

// ErrNoGit is returned when git is absent. It is checked once, early, for the same
// reason MinTerraformVersion is: discovering it halfway through a clone leaves a
// directory that is neither absent nor usable.
var ErrNoGit = errors.New("infra: git not found in PATH")

// ErrCheckoutDirty is returned when a managed checkout has modified tracked files.
// A checkout with edits is the operator's work, not this tool's to move.
var ErrCheckoutDirty = errors.New("infra: managed checkout has local modifications")

// ManagedCheckoutPath is where a managed checkout lives. override is
// --templates-dir: an operator on a read-only home, a network home, or one who
// keeps tooling under XDG needs somewhere else to put it.
func ManagedCheckoutPath(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("infra: cannot resolve the home directory: %w", err)
	}
	return filepath.Join(home, managedCheckoutRel), nil
}

// Git runs the git operations the managed checkout needs. It is an interface so
// the tests can drive a local bare repository instead of the network, and so a
// caller can report progress around a clone that takes seconds.
type Git interface {
	Clone(ctx context.Context, url, ref, dest string) error
	Fetch(ctx context.Context, dir string) error
	Checkout(ctx context.Context, dir, ref string) error
	// DescribeRef reports the tag the checkout is on, or the empty string when it
	// is not on one — a branch, a commit between tags, a directory that is not a
	// repository, or a git that cannot run. Every one of those means the same thing
	// to a caller: the ref is unknown. There is no error to return, and a signature
	// that promised one made callers write a branch that could never fire.
	DescribeRef(ctx context.Context, dir string) string
	// DirtyTracked lists modified tracked files. Untracked and ignored files are
	// deliberately not reported: they are the operator's configuration, and their
	// presence is the normal state of a working checkout.
	DirtyTracked(ctx context.Context, dir string) ([]string, error)
	// RemoteTags lists the tags a repository publishes, without cloning it. It is
	// what turns "--templates-ref is required" from an obstacle into a menu: the
	// operator has to choose a tag, so the error that asks for one has to be able
	// to say which ones exist.
	RemoteTags(ctx context.Context, url string) ([]string, error)
}

// GitCLI drives the git binary.
type GitCLI struct {
	// Binary is the git executable. Empty means "git" from PATH.
	Binary string
}

// NewGitCLI verifies git is present before anything tries to use it.
func NewGitCLI() (GitCLI, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return GitCLI{}, fmt.Errorf("%w\nCloning the templates needs it. Install git, or "+
			"download the repository yourself and point at it with --repo.", ErrNoGit)
	}
	return GitCLI{}, nil
}

func (g GitCLI) binary() string {
	if g.Binary != "" {
		return g.Binary
	}
	return "git"
}

// runRaw executes one git command and folds stderr into the error: git says why it
// failed on stderr, and an exit status alone is not a diagnosis.
//
// The output is returned UNTOUCHED. `status --porcelain` is column-significant —
// its first two characters are the index and worktree status, so a leading space
// carries meaning — and trimming the whole payload silently shifts every offset by
// one on the first line. That produced a refusal message naming "gitignore"
// instead of ".gitignore", in the one message whose entire job is to name the file.
func (g GitCLI) runRaw(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, g.binary(), args...)
	if dir != "" {
		command.Dir = dir
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("infra: git %s: %s", strings.Join(args, " "), detail)
	}
	return string(output), nil
}

// run is runRaw for the commands whose output is a single token.
func (g GitCLI) run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := g.runRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// Clone makes a FULL clone, not a shallow one.
//
// A --depth 1 clone is faster once and then cannot fetch another tag without
// unshallowing, which turns every later version bump into a special case. The
// repository is small enough that paying the history cost once is the simpler
// trade.
func (g GitCLI) Clone(ctx context.Context, url, ref, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("infra: cannot create %s: %w", filepath.Dir(dest), err)
	}
	args := []string{"clone"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	// "--" before the URL: LERIAN_TEMPLATES_REPO is operator input, and a value
	// starting with a dash would otherwise be parsed as an option such as
	// --upload-pack=<cmd> instead of a repository.
	args = append(args, "--", url, dest)
	_, err := g.run(ctx, "", args...)
	return err
}

func (g GitCLI) Fetch(ctx context.Context, dir string) error {
	_, err := g.run(ctx, dir, "fetch", "--tags", "--prune")
	return err
}

func (g GitCLI) Checkout(ctx context.Context, dir, ref string) error {
	_, err := g.run(ctx, dir, "checkout", "--detach", ref)
	return err
}

// DescribeRef asks for the exact tag on HEAD. A checkout that is not on a tag —
// main, or a commit between tags — reports "", which callers render as unknown
// rather than guessing a version.
func (g GitCLI) DescribeRef(ctx context.Context, dir string) string {
	tag, err := g.run(ctx, dir, "describe", "--tags", "--exact-match", "HEAD")
	if err != nil {
		return ""
	}
	return tag
}

func (g GitCLI) DirtyTracked(ctx context.Context, dir string) ([]string, error) {
	// --untracked-files=no: untracked files are the operator's configuration, and
	// their presence is the normal state of a working checkout, not dirt.
	out, err := g.runRaw(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if name := porcelainPath(line); name != "" {
			files = append(files, name)
		}
	}
	return files, nil
}

// RemoteTags lists the tags the templates repository publishes, newest first.
//
// `ls-remote --tags` and not a clone: this runs while deciding WHETHER to clone, so
// downloading the repository to find out which versions of it exist would be the
// wrong way round. Peeled entries (refs/tags/v1.2.3^{}) are dropped — an annotated
// tag appears twice and only the name matters here.
func (g GitCLI) RemoteTags(ctx context.Context, url string) ([]string, error) {
	out, err := g.runRaw(ctx, "", "ls-remote", "--tags", "--", url)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, line := range strings.Split(out, "\n") {
		_, ref, found := strings.Cut(strings.TrimSpace(line), "refs/tags/")
		if !found || strings.HasSuffix(ref, "^{}") || ref == "" {
			continue
		}
		tags = append(tags, ref)
	}
	SortRefs(tags)
	return tags, nil
}

// SortRefs orders version tags newest first. Anything unparseable sorts last, by
// name: a repository is free to carry tags that are not versions, and they are not
// what a caller asking for the newest release means.
func SortRefs(refs []string) {
	sort.SliceStable(refs, func(i, j int) bool {
		return CompareRefs(refs[i], refs[j]) > 0
	})
}

// LatestRef returns the newest version tag, or "" when there is none.
func LatestRef(refs []string) string {
	for _, ref := range refs {
		if _, ok := parseRef(ref); ok {
			return ref
		}
	}
	return ""
}

// CompareRefs orders two version tags: negative when a is older, positive when it
// is newer, zero when they are the same version or neither is a version.
//
// Prereleases sort BELOW the release they lead to (v1.6.0-develop.1 < v1.6.0),
// which is what semver says and what matters here: a floor of v1.5.0 must not be
// satisfied by v1.5.0-develop.2, a tag cut before the release was finished.
func CompareRefs(a, b string) int {
	versionA, okA := parseRef(a)
	versionB, okB := parseRef(b)
	switch {
	case !okA && !okB:
		return strings.Compare(b, a)
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range 3 {
		if versionA.number[i] != versionB.number[i] {
			return versionA.number[i] - versionB.number[i]
		}
	}
	switch {
	case versionA.pre == versionB.pre:
		return 0
	case versionA.pre == "":
		return 1
	case versionB.pre == "":
		return -1
	}
	return comparePrerelease(versionA.pre, versionB.pre)
}

// comparePrerelease orders two prerelease suffixes the way semver says: field by
// field, numerically when both fields are numbers.
//
// String ordering is wrong here in a way that bites immediately — "develop.10" sorts
// below "develop.2" as text, and semantic-release reaches double digits on any
// branch that lives more than a week, so the newest prerelease would be reported as
// the oldest.
func comparePrerelease(a, b string) int {
	fieldsA, fieldsB := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(fieldsA), len(fieldsB)) {
		if fieldsA[i] == fieldsB[i] {
			continue
		}
		numberA, errA := strconv.Atoi(fieldsA[i])
		numberB, errB := strconv.Atoi(fieldsB[i])
		switch {
		case errA == nil && errB == nil:
			return numberA - numberB
		case errA == nil:
			// Numeric identifiers rank below alphanumeric ones, per semver.
			return -1
		case errB == nil:
			return 1
		}
		return strings.Compare(fieldsA[i], fieldsB[i])
	}
	// Every shared field is equal: the one with more fields is the later prerelease.
	return len(fieldsA) - len(fieldsB)
}

// RefBelowMin reports whether ref is older than TemplatesMinRef.
//
// A ref that is not a version answers FALSE: a branch, a fork's tag or a commit
// between tags cannot be compared, and refusing to run — or nagging — over
// something unmeasurable would punish exactly the development checkouts that have
// good reason to look like that. What cannot be judged is not judged.
func RefBelowMin(ref string) bool {
	if _, ok := parseRef(ref); !ok {
		return false
	}
	return CompareRefs(ref, TemplatesMinRef) < 0
}

// version is a tag split into what ordering needs.
type version struct {
	number [3]int
	pre    string
}

// parseRef reads vMAJOR.MINOR.PATCH with an optional -prerelease suffix. Build
// metadata (+sha) is ignored, as semver says it must be for ordering.
func parseRef(ref string) (version, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(ref), "v")
	if !ok {
		return version{}, false
	}
	if plus := strings.IndexByte(rest, '+'); plus >= 0 {
		rest = rest[:plus]
	}
	core, pre, _ := strings.Cut(rest, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var parsed version
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return version{}, false
		}
		parsed.number[i] = number
	}
	parsed.pre = pre
	return parsed, true
}

// porcelainPath extracts the path from one `status --porcelain` line, or "" when
// the line carries none.
//
// The format is two status characters, a space, then the path — so the path starts
// at a fixed offset and the line must NOT be trimmed beforehand. A rename reads
// "R  old -> new"; the new name is the one an operator can act on.
func porcelainPath(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 4 {
		return ""
	}
	path := line[3:]
	if _, after, found := strings.Cut(path, " -> "); found {
		path = after
	}
	return strings.Trim(path, "\"")
}

// CheckoutState is what is known about a templates checkout before anything is
// done to it.
type CheckoutState struct {
	// Path is where it is, or would be.
	Path string
	// Exists reports whether Path holds a checkout this tool recognises.
	Exists bool
	// Ref is the tag it is on, or "" when it is on a branch or an untagged commit.
	Ref string
	// Managed reports whether this is the checkout this tool creates, as opposed to
	// one the operator pointed at.
	Managed bool
}

// AtVersion reports whether the checkout is on the tag this binary expects.
//
// An unknown ref is NOT treated as matching: a checkout on main may be newer,
// older, or mid-rebase, and claiming parity for it is exactly the false assurance
// this whole mechanism exists to remove.
func (c CheckoutState) AtVersion(wanted string) bool {
	return c.Ref != "" && c.Ref == wanted
}

// InspectCheckout reports what is at path without changing anything.
func InspectCheckout(ctx context.Context, git Git, path string, managed bool) CheckoutState {
	state := CheckoutState{Path: path, Managed: managed}
	if !IsCheckout(path) {
		return state
	}
	state.Exists = true
	state.Ref = git.DescribeRef(ctx, path)
	return state
}

// checkoutMarkers are the directories a lerian-terraform-foundation checkout is
// recognised by. ALL of them must be present, and they are deliberately the two
// the tool itself depends on rather than a name that merely looks distinctive:
//
//	examples/aws/_modules   what every root's source = "../../../_modules/..."
//	                        resolves to, so its absence means the roots below it
//	                        cannot initialise at all;
//	examples/aws/backend    where LoadBackend reads <env>.hcl.
//
// Both are tracked in git — _modules holds the module sources, backend/ holds
// .gitkeep and README.md — so a fresh clone is recognised before Terraform has
// ever run in it. That is why the marker is not environments.conf or
// backend/<env>.hcl: both are gitignored, so a marker built on them would fail on
// exactly the checkout that has not been bootstrapped yet. It is not products/
// either: that is the directory being discovered, and a bare "products" is a
// common enough name to match something unrelated on the way up.
var checkoutMarkers = [][]string{
	{"examples", "aws", "_modules"},
	{"examples", "aws", "backend"},
}

// IsCheckout reports whether dir holds a lerian-terraform-foundation checkout.
func IsCheckout(dir string) bool {
	if dir == "" {
		return false
	}
	for _, marker := range checkoutMarkers {
		info, err := os.Stat(filepath.Join(append([]string{dir}, marker...)...))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// FindCheckout walks up from dir looking for a checkout, returning "" at the
// filesystem root.
func FindCheckout(dir string) string {
	for {
		if IsCheckout(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// CloneTemplates creates a managed checkout at state.Path, pinned to ref.
//
// ref is the tag matching this binary. It is passed rather than derived so a
// caller that has no version — a locally built binary reporting "dev" — has to
// decide what to do about that out loud instead of silently getting a branch.
func CloneTemplates(ctx context.Context, git Git, path, ref string) error {
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return fmt.Errorf("infra: %s already exists and is not empty\n"+
			"Remove it, or point somewhere else with --templates-dir.", path)
	}
	if err := git.Clone(ctx, TemplatesRepoURL(), ref, path); err != nil {
		return err
	}
	if !IsCheckout(path) {
		// The clone worked; the tag is the problem. This fires when ref predates the
		// AWS v2 layout — the tags up to v1.5.0 carry one directory per resource
		// (examples/aws/eks, examples/aws/rds, ...) and have no _modules or backend
		// at all, so no version of this tool can drive them. Saying only "does not
		// look like a checkout" reads as a broken download and sends the operator to
		// debug git.
		//
		// The directory is left in place rather than deleted: it is a valid clone of
		// a real tag, the operator may want to look at it, and removing a directory
		// they just watched being created is a worse surprise than leaving it.
		return fmt.Errorf("infra: %s was cloned at %s, but that tag does not carry the "+
			"AWS v2 layout\n"+
			"Expected examples/aws/_modules and examples/aws/backend under it; neither is\n"+
			"there. Tags before the v2 layout hold one directory per resource\n"+
			"(examples/aws/eks, examples/aws/rds, ...), which this tool cannot drive.\n\n"+
			"This means the binary and the templates disagree about which layout exists.\n"+
			"Either use a binary from a release that ships the v2 templates, or point at a\n"+
			"checkout of one with --repo.\n\n"+
			"The clone was left at %s; remove it when you are done with it.",
			path, refDescription(ref), path)
	}
	return nil
}

// refDescription names the ref for an error message, including the case where none
// was requested.
func refDescription(ref string) string {
	if ref == "" {
		return "the default branch"
	}
	return ref
}

// SyncTemplates moves an existing managed checkout to ref.
//
// It refuses on modified tracked files rather than stashing or forcing them: a
// checkout with edits is the operator's work, and a tool that silently moves it
// has destroyed something it was never asked to touch. Untracked and ignored
// files — which is to say the entire configuration — are safe by construction,
// because git checkout does not touch them. That is why the managed path carries
// no version.
func SyncTemplates(ctx context.Context, git Git, path, ref string) error {
	dirty, err := git.DirtyTracked(ctx, path)
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("%w: %d tracked file(s) modified in %s\n\n  %s\n\n"+
			"A checkout with edits is yours, not managed. Commit them to a fork and\n"+
			"use --repo, or discard them with 'git -C %s checkout .'",
			ErrCheckoutDirty, len(dirty), path,
			strings.Join(dirty, "\n  "), path)
	}
	if err := git.Fetch(ctx, path); err != nil {
		return err
	}
	return git.Checkout(ctx, path, ref)
}
