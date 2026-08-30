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

// The clone is pinned to the ref the build DECLARES, not to the binary's own version:
// the CLI and the templates release from different repositories, so nothing makes
// the two coincide by construction. A local build is not special here — it declares
// a ref like any release does, and gets exactly that tag.
func TestCloneUsesTheDeclaredTemplatesRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	out := &bytes.Buffer{}
	ask := &prompter{out: out, interactive: true}

	t.Setenv(infra.TemplatesRepoEnv, source)

	layout, err := acquireTemplates(context.Background(),
		initOptions{clone: true, templatesDir: dest}, ask, out)
	if err != nil {
		t.Fatalf("acquireTemplates: %v", err)
	}
	if layout.Root == "" {
		t.Fatal("a layout should have been returned")
	}
	state := infra.InspectCheckout(context.Background(), infra.GitCLI{}, layout.Root, true)
	if !state.AtVersion(infra.TemplatesRef) {
		t.Errorf("checkout at %q, want the declared %s", state.Ref, infra.TemplatesRef)
	}
	// The explanation names the ref, because an operator watching a clone happen
	// should be able to see which tag they are getting.
	if !strings.Contains(out.String(), infra.TemplatesRef) {
		t.Errorf("the templates ref must be stated:\n%s", out.String())
	}
	// And it never falls back to a branch, whatever the binary's version says.
	if strings.Contains(out.String(), "Cloning main") {
		t.Error("a declared ref means there is never a reason to clone a branch")
	}
}

// --sync moves an existing managed checkout to the declared ref, and the operator's
// configuration inside it survives.
func TestSyncMovesTheCheckoutToTheDeclaredRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	git := infra.GitCLI{}
	ctx := context.Background()

	// A checkout left at the OLDER tag, as an operator who upgraded the binary has.
	if err := git.Clone(ctx, source, "v0.9.0", dest); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dest, "examples", "aws", "environments.conf")
	if err := os.WriteFile(config, []byte("[dev]\naccount_id = 123456789012\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	if err := runSync(ctx, initOptions{templatesDir: dest}, out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	state := infra.InspectCheckout(ctx, git, dest, true)
	if !state.AtVersion(infra.TemplatesRef) {
		t.Errorf("after sync: %q, want %s", state.Ref, infra.TemplatesRef)
	}
	if _, err := os.Stat(config); err != nil {
		t.Error("the operator's environments.conf was lost by the sync")
	}
	if !strings.Contains(out.String(), "from      v0.9.0") || !strings.Contains(out.String(), "to        "+infra.TemplatesRef) {
		t.Errorf("the sync must say where it moved from and to:\n%s", out.String())
	}
}

// A checkout on any other tag is reported — with both refs named, so the operator can
// tell which side is stale — and the run is not blocked.
func TestMismatchNamesBothRefsAndDoesNotBlock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	if err := (infra.GitCLI{}).Clone(context.Background(), source, "v0.9.0", dest); err != nil {
		t.Fatal(err)
	}
	layout, _ := infra.NewLayout(dest)

	out := &bytes.Buffer{}
	warnVersionMismatch(context.Background(), out, layout, sourceManaged)

	printed := out.String()
	for _, want := range []string{"v0.9.0", infra.TemplatesRef, "--sync"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the warning must mention %q:\n%s", want, printed)
		}
	}

	// And silence when they agree.
	if err := (infra.GitCLI{}).Checkout(context.Background(), dest, infra.TemplatesRef); err != nil {
		t.Fatal(err)
	}
	quiet := &bytes.Buffer{}
	warnVersionMismatch(context.Background(), quiet, layout, sourceManaged)
	if quiet.Len() != 0 {
		t.Errorf("no warning when the checkout is at the declared ref:\n%s", quiet.String())
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
	run("tag", "v0.9.0")
	// A second commit so the two tags are on different trees: a tag on the same
	// commit would make `describe --exact-match` ambiguous.
	if err := os.WriteFile(filepath.Join(dir, "NEWER.md"), []byte("at the declared ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "templates at the declared ref")
	run("tag", infra.TemplatesRef)
	return dir
}

// The preflight line is the one place every command passes through, so it is where a
// mismatch reaches an operator who never runs init. It names the expected ref on the
// same line — a note, not a block — and says nothing extra when they agree.
func TestPreflightTemplatesLineNamesTheExpectedRefOnMismatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := localOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "managed")
	git := infra.GitCLI{}
	ctx := context.Background()
	if err := git.Clone(ctx, source, "v0.9.0", dest); err != nil {
		t.Fatal(err)
	}
	layout, _ := infra.NewLayout(dest)

	line := templatesLine(ctx, layout, sourceManaged)
	for _, want := range []string{"@ v0.9.0", "built for " + infra.TemplatesRef, "(managed)"} {
		if !strings.Contains(line, want) {
			t.Errorf("mismatch line must contain %q:\n%s", want, line)
		}
	}

	if err := git.Checkout(ctx, dest, infra.TemplatesRef); err != nil {
		t.Fatal(err)
	}
	line = templatesLine(ctx, layout, sourceManaged)
	if strings.Contains(line, "built for") {
		t.Errorf("no expectation note when the checkout is at the declared ref:\n%s", line)
	}
	if !strings.Contains(line, "@ "+infra.TemplatesRef) {
		t.Errorf("the line must still say which tag it is at:\n%s", line)
	}
}
