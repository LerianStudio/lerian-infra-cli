package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/LerianStudio/lerian-infra-cli/pkg/infra"
)

// checkUsage is printed by `lerian-infra check --help`.
const checkUsage = `lerian-infra check — is this machine able to run the CLI

Usage:
  lerian-infra check [flags]

Reports every dependency this tool shells out to, and the templates checkout it
would use, in one pass. It makes no AWS call and reads no environment
configuration, so it answers one question only: can this machine run
lerian-infra at all.

The same checks run during a normal invocation, each one immediately before the
first call that needs it. That ordering is deliberate — what is verified is
exactly what is about to be used — but it surfaces problems one at a time, so a
bare machine is fixed over as many rounds as it has gaps. This command collects
them instead.

Exits non-zero when anything is missing, which makes it usable as a CI gate.

Flags:
  --repo <path>           the checkout to report on. Skips discovery.
  --templates-dir <path>  where the managed checkout lives
                          (default ~/lerian/lerian-terraform-foundation)
  -h, --help              this message
`

// checkResult is one row of the report. Detail carries the full remediation of a
// failure, which the underlying errors already write well, so this command adds
// nothing to it beyond a place to print it.
type checkResult struct {
	name    string
	summary string
	detail  string
	ok      bool
}

func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var opts struct {
		repo         string
		templatesDir string
	}

	flags := flag.NewFlagSet("lerian-infra check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, checkUsage) }
	flags.StringVar(&opts.repo, "repo", "", "path to the checkout")
	flags.StringVar(&opts.templatesDir, "templates-dir", "",
		"where the managed checkout lives (default ~/lerian/lerian-terraform-foundation)")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if rest := flags.Args(); len(rest) > 0 {
		return fmt.Errorf("unexpected argument %q\nRun lerian-infra check --help", rest[0])
	}

	results := []checkResult{
		checkAWSCLI(ctx),
		checkTerraform(ctx),
		checkGit(),
		checkTemplates(ctx, opts.repo, os.Getenv("LERIAN_TF_REPO"), opts.templatesDir),
	}

	return reportChecks(stdout, results)
}

// checkAWSCLI defers to the same verification a real run makes, which covers the
// major version too: v1 cannot export a profile's credentials.
func checkAWSCLI(ctx context.Context) checkResult {
	result := checkResult{name: "aws", summary: binaryPath("aws"), ok: true}
	if err := infra.RequireAWSCLI(ctx); err != nil {
		result.ok = false
		result.summary = "not usable"
		result.detail = err.Error()
	}
	return result
}

// checkTerraform builds the same CLI a run builds. Constructing it is the check:
// it resolves the binary and rejects anything below MinTerraformVersion.
func checkTerraform(ctx context.Context) checkResult {
	result := checkResult{name: "terraform", summary: binaryPath("terraform"), ok: true}
	if _, err := infra.NewCLI(ctx); err != nil {
		result.ok = false
		result.summary = "not usable"
		result.detail = err.Error()
	}
	return result
}

func checkGit() checkResult {
	result := checkResult{name: "git", summary: binaryPath("git"), ok: true}
	if _, err := infra.NewGitCLI(); err != nil {
		result.ok = false
		result.summary = "not usable"
		result.detail = err.Error()
	}
	return result
}

// checkTemplates reports which checkout a run would resolve to, and at which
// version. Discovery has more than one source — a flag, $LERIAN_TF_REPO, the
// working directory and its parents, the managed path — so naming the winner is
// worth a row of its own. It has to resolve from the same inputs a run does, or
// the row reports a checkout that is not the one about to be used.
func checkTemplates(ctx context.Context, repo, envRepo, templatesDir string) checkResult {
	result := checkResult{name: "templates", ok: true}

	layout, source, err := resolveLayout(repo, envRepo, templatesDir)
	if err != nil {
		result.ok = false
		result.summary = "not found"
		result.detail = err.Error()
		return result
	}
	result.summary = templatesLine(ctx, layout, source)
	return result
}

// binaryPath is for the report only. The verification is the infra function next
// to it; this just says where the binary it accepted lives, because "ok" without
// a path hides a second copy earlier in PATH.
func binaryPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return "not found in PATH"
	}
	return path
}

func reportChecks(out io.Writer, results []checkResult) error {
	theme := newStyle(out)
	fmt.Fprintf(out, "\n%s\n", theme.bold("==> Environment check"))

	width := 0
	for _, r := range results {
		if len(r.name) > width {
			width = len(r.name)
		}
	}

	failed := 0
	for _, r := range results {
		mark := "ok"
		if !r.ok {
			mark = theme.alert("missing")
			failed++
		}
		fmt.Fprintf(out, "  %-*s  %-52s %s\n", width, r.name, r.summary, mark)
	}

	// The remediations come after the table rather than inline, so the table stays
	// scannable when several things are wrong — which is the case this command
	// exists for.
	for _, r := range results {
		if r.ok || r.detail == "" {
			continue
		}
		// indent() leaves the first line alone, because its other callers place it
		// themselves. Here the whole block is being pushed under a heading.
		fmt.Fprintf(out, "\n%s\n%s\n", theme.bold("  "+r.name), indent("    "+r.detail, "    "))
	}

	if failed == 0 {
		fmt.Fprintf(out, "\n  %d checks, all ok. No AWS call was made.\n", len(results))
		return nil
	}
	fmt.Fprintf(out, "\n  %d of %d checks failed.\n", failed, len(results))
	return fmt.Errorf("check: %s", strings.Join(failedNames(results), ", "))
}

func failedNames(results []checkResult) []string {
	var names []string
	for _, r := range results {
		if !r.ok {
			names = append(names, r.name)
		}
	}
	return names
}
