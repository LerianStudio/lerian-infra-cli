package main

import (
	"bytes"
	"context"
	"github.com/LerianStudio/lerian-infra-cli/pkg/infra"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every one of these is an error rather than a precedence rule. An operator who
// typed two contradictory flags does not know which they want, and choosing for
// them buries the confusion inside a run that then does something unasked.
func TestContradictoryTemplateFlagsAreRefused(t *testing.T) {
	for _, test := range []struct {
		name string
		opts initOptions
		want string
	}{
		{"clone and no-clone", initOptions{clone: true, noClone: true}, "--clone and --no-clone"},
		{"repo and clone", initOptions{repo: "/x", clone: true}, "--repo and --clone"},
		{"sync and repo", initOptions{sync: true, repo: "/x"}, "--sync cannot be used with --repo"},
	} {
		err := test.opts.validateTemplateFlags()
		if err == nil {
			t.Errorf("%s: expected a refusal", test.name)
			continue
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error should name the conflict %q:\n%v", test.name, test.want, err)
		}
	}

	// And the ordinary combinations must stay legal.
	for _, ok := range []initOptions{
		{}, {clone: true}, {noClone: true}, {repo: "/x"}, {sync: true},
		{clone: true, autoApprove: true},
	} {
		if err := ok.validateTemplateFlags(); err != nil {
			t.Errorf("%+v should be allowed: %v", ok, err)
		}
	}
}

// --auto-approve means "skip the confirmation before writing the files I asked
// for", and CI passes it as a matter of routine. If it also authorised a clone,
// every CI run would be able to download a repository nobody asked it to fetch.
func TestAutoApproveDoesNotAuthoriseACloneOutsideATerminal(t *testing.T) {
	// acquireTemplates looks git up before it reaches the clone decision, so without
	// git this asserts the wrong refusal and fails instead of skipping.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dest := filepath.Join(t.TempDir(), "managed")
	out := &bytes.Buffer{}
	// A prompter that is not interactive is exactly the CI case.
	ask := &prompter{out: out, interactive: false}

	_, err := acquireTemplates(context.Background(),
		initOptions{autoApprove: true, templatesDir: dest}, ask, out)
	if err == nil {
		t.Fatal("--auto-approve must not be enough to clone")
	}
	for _, want := range []string{"--clone", "--no-clone", "--auto-approve does not imply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must mention %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("nothing should have been created")
	}
}

// --no-clone is the way to say "fail instead", and it must fail without touching
// the network or the filesystem.
func TestNoCloneFailsWithoutCreatingAnything(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dest := filepath.Join(t.TempDir(), "managed")
	out := &bytes.Buffer{}
	ask := &prompter{out: out, interactive: true}

	_, err := acquireTemplates(context.Background(),
		initOptions{noClone: true, templatesDir: dest}, ask, out)
	if err == nil || !strings.Contains(err.Error(), "--no-clone") {
		t.Fatalf("expected a refusal naming --no-clone, got %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("nothing should have been created")
	}
}

// The tags the fixture repository carries. belowMinTag is older than
// infra.TemplatesMinRef; operatorTag is NEWER than anything this binary names, which
// is the point: the operator picks the templates release, and a binary that only
// accepted a tag it knew about would be the pin all over again.
const (
	belowMinTag = "v0.9.0"
	operatorTag = "v1.9.0"
)

// The clone goes to the tag the OPERATOR named. There is no default and no ref
// compiled in: the CLI and the templates release from different repositories, and
// which templates release to run is the operator's decision, not the binary's.
func TestCloneUsesTheTagTheOperatorAsked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	out := &bytes.Buffer{}
	ask := &prompter{out: out, interactive: true}

	t.Setenv(infra.TemplatesRepoEnv, source)

	layout, err := acquireTemplates(context.Background(),
		initOptions{clone: true, templatesDir: dest, templatesRef: operatorTag}, ask, out)
	if err != nil {
		t.Fatalf("acquireTemplates: %v", err)
	}
	if layout.Root == "" {
		t.Fatal("a layout should have been returned")
	}
	state := infra.InspectCheckout(context.Background(), infra.GitCLI{}, layout.Root, true)
	if !state.AtVersion(operatorTag) {
		t.Errorf("checkout at %q, want the requested %s", state.Ref, operatorTag)
	}
	// The explanation names the ref, because an operator watching a clone happen
	// should be able to see which tag they are getting.
	if !strings.Contains(out.String(), operatorTag) {
		t.Errorf("the templates ref must be stated:\n%s", out.String())
	}
	// And it never falls back to a branch.
	if strings.Contains(out.String(), "Cloning main") {
		t.Error("a named ref means there is never a reason to clone a branch")
	}
}

// Refusing without saying which tags exist would replace a bad default with a
// guessing game. The refusal is a menu: the newest tag, a few under it, the floor,
// and the exact command to run.
func TestCloneWithoutATagListsWhatIsAvailable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	out := &bytes.Buffer{}
	ask := &prompter{out: out, interactive: true}
	t.Setenv(infra.TemplatesRepoEnv, source)

	_, err := acquireTemplates(context.Background(),
		initOptions{clone: true, templatesDir: dest}, ask, out)
	if err == nil {
		t.Fatal("a clone with no --templates-ref must be refused")
	}
	for _, want := range []string{"--clone", "--templates-ref", operatorTag, infra.TemplatesMinRef} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must mention %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("nothing should have been created")
	}
}

// The floor is the one version claim this binary still makes, and it is a refusal
// rather than a warning: below it the HCL is a shape the chart mapping was never
// written for, and the operator is about to create infrastructure from it.
func TestCloneRefusesATagBelowTheFloor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dest := filepath.Join(t.TempDir(), "managed")
	out := &bytes.Buffer{}
	ask := &prompter{out: out, interactive: true}

	_, err := acquireTemplates(context.Background(),
		initOptions{clone: true, templatesDir: dest, templatesRef: belowMinTag}, ask, out)
	if err == nil {
		t.Fatal("a tag below the floor must be refused")
	}
	for _, want := range []string{belowMinTag, infra.TemplatesMinRef} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("nothing should have been created")
	}
}

// --sync moves an existing managed checkout to the tag the operator names, and the
// configuration inside it survives.
func TestSyncMovesTheCheckoutToTheRequestedRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	git := infra.GitCLI{}
	ctx := context.Background()

	// A checkout left at the OLDER tag, as an operator who upgraded the binary has.
	if err := git.Clone(ctx, source, belowMinTag, dest); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dest, "examples", "aws", "environments.conf")
	if err := os.WriteFile(config, []byte("[dev]\naccount_id = 123456789012\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	if err := runSync(ctx, initOptions{templatesDir: dest, templatesRef: operatorTag}, out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	state := infra.InspectCheckout(ctx, git, dest, true)
	if !state.AtVersion(operatorTag) {
		t.Errorf("after sync: %q, want %s", state.Ref, operatorTag)
	}
	if _, err := os.Stat(config); err != nil {
		t.Error("the operator's environments.conf was lost by the sync")
	}
	if !strings.Contains(out.String(), "from      "+belowMinTag) || !strings.Contains(out.String(), "to        "+operatorTag) {
		t.Errorf("the sync must say where it moved from and to:\n%s", out.String())
	}

	// And a sync with nowhere named is refused, for the same reason a clone is.
	refused := &bytes.Buffer{}
	if err := runSync(ctx, initOptions{templatesDir: dest}, refused); err == nil {
		t.Error("--sync with no --templates-ref has no target and must be refused")
	}
}

// The warning fires on the floor and nothing else. A checkout NEWER than any tag
// this binary knows is the normal, supported case now that the operator picks the
// release — warning about it would train them to ignore the one message that means
// something.
func TestWarningFiresOnlyBelowTheFloorAndDoesNotBlock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	if err := (infra.GitCLI{}).Clone(context.Background(), source, belowMinTag, dest); err != nil {
		t.Fatal(err)
	}
	layout, _ := infra.NewLayout(dest)

	out := &bytes.Buffer{}
	warnTemplatesBelowMin(context.Background(), out, layout, sourceManaged)

	printed := out.String()
	for _, want := range []string{belowMinTag, infra.TemplatesMinRef, "--sync"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the warning must mention %q:\n%s", want, printed)
		}
	}

	// Silence above the floor, including for a tag newer than anything this binary
	// names.
	if err := (infra.GitCLI{}).Checkout(context.Background(), dest, operatorTag); err != nil {
		t.Fatal(err)
	}
	quiet := &bytes.Buffer{}
	warnTemplatesBelowMin(context.Background(), quiet, layout, sourceManaged)
	if quiet.Len() != 0 {
		t.Errorf("a checkout above the floor is not worth a word:\n%s", quiet.String())
	}
}

// A mirror is an ordinary BYOC situation: an air-gapped client, or an organisation
// that vendors the templates into its own git server. Without the override the
// managed checkout would be unavailable to exactly those clients.
func TestTemplatesRepoOverrideIsHonoured(t *testing.T) {
	t.Setenv(infra.TemplatesRepoEnv, "https://git.internal/mirror.git")
	if got := infra.TemplatesRepoURL(); got != "https://git.internal/mirror.git" {
		t.Errorf("override ignored: %q", got)
	}
	t.Setenv(infra.TemplatesRepoEnv, "   ")
	if got := infra.TemplatesRepoURL(); !strings.Contains(got, "github.com/LerianStudio") {
		t.Errorf("blank override should fall back to the default: %q", got)
	}
}

// localOriginRepo builds a git repository that looks like the templates and can be
// cloned FROM, so none of these tests touch the network.
func localOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	for _, marker := range [][]string{
		{"examples", "aws", "_modules"}, {"examples", "aws", "backend"},
	} {
		full := filepath.Join(append([]string{dir}, marker...)...)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, ".gitkeep"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-qm", "older templates")
	run("tag", belowMinTag)
	// A second commit so the two tags are on different trees: a tag on the same
	// commit would make `describe --exact-match` ambiguous.
	if err := os.WriteFile(filepath.Join(dir, "NEWER.md"), []byte("at the newer tag\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "templates the operator asks for")
	run("tag", operatorTag)
	return dir
}

// The preflight line is the one place every command passes through, so it is where
// templates below the floor reach an operator who never runs init. It says so on the
// same line — a note, not a block — and says nothing extra above the floor.
func TestPreflightTemplatesLineFlagsACheckoutBelowTheFloor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	git := infra.GitCLI{}
	ctx := context.Background()
	if err := git.Clone(ctx, source, belowMinTag, dest); err != nil {
		t.Fatal(err)
	}
	layout, _ := infra.NewLayout(dest)

	line := templatesLine(ctx, layout, sourceManaged)
	for _, want := range []string{"@ " + belowMinTag, infra.TemplatesMinRef, "(managed)"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line must contain %q:\n%s", want, line)
		}
	}

	if err := git.Checkout(ctx, dest, operatorTag); err != nil {
		t.Fatal(err)
	}
	line = templatesLine(ctx, layout, sourceManaged)
	if strings.Contains(line, "oldest this binary reads") {
		t.Errorf("no note when the checkout is above the floor:\n%s", line)
	}
	if !strings.Contains(line, "@ "+operatorTag) {
		t.Errorf("the line must still say which tag it is at:\n%s", line)
	}
}

// The command in the refusal has to be one this binary would ACCEPT. When the floor
// is ahead of every published tag — which is the normal state right after a CLI
// change lands and before the templates release — suggesting the newest tag would
// hand the operator a line the same tool refuses on the next run.
func TestRefusalNeverSuggestsATagBelowTheFloor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// A repository whose only tags are below the floor.
	source := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = source
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "old templates")
	run("tag", belowMinTag)
	t.Setenv(infra.TemplatesRepoEnv, source)
	git := infra.GitCLI{}

	err := missingRefError(context.Background(), git, "--clone")
	if err == nil {
		t.Fatal("a missing ref is always an error")
	}
	printed := err.Error()
	if strings.Contains(printed, "--templates-ref "+belowMinTag) {
		t.Errorf("the refusal suggests a tag it would refuse:\n%s", printed)
	}
	for _, want := range []string{infra.TemplatesMinRef, "No published tag meets that floor"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the refusal must say %q:\n%s", want, printed)
		}
	}
}
