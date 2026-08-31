package infra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The AWS CLI is a hard dependency, and its absence used to arrive as
// `exec: "aws": executable file not found in $PATH` inside a credentials error —
// which reads as a broken profile, sending the operator to fix the one thing that
// is fine.
func TestRequireAWSCLIExplainsBothWaysToConfigureAProfile(t *testing.T) {
	// An empty PATH is the only honest way to test "not installed".
	t.Setenv("PATH", t.TempDir())

	err := RequireAWSCLI(context.Background())
	if err == nil {
		t.Fatal("with an empty PATH the AWS CLI cannot be found")
	}
	if !errors.Is(err, ErrNoAWSCLI) {
		t.Errorf("callers match on ErrNoAWSCLI: %v", err)
	}
	message := err.Error()
	// SSO is common here and not universal. A profile holding an access key and
	// secret is just as valid, and telling that operator to run `aws configure sso`
	// points them at something that is not broken.
	for _, want := range []string{
		"aws configure sso",
		"aws configure --profile",
		"access key and secret",
		"one per environment",
		"--profile '' with --account",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the message must mention %q:\n%s", want, message)
		}
	}
}

// And it stays quiet when a usable one is there: a check that fires on a working
// machine is worse than no check.
func TestRequireAWSCLIPassesForVersionTwo(t *testing.T) {
	t.Setenv("PATH", fakeAWSCLI(t, "aws-cli/2.31.22 Python/3.13.9 Darwin/25.6.0 source/arm64"))
	if err := RequireAWSCLI(context.Background()); err != nil {
		t.Errorf("a v2 CLI is what this tool wants: %v", err)
	}
}

// v1 passes a presence check and then fails at the first profile: `configure
// export-credentials` landed in v2 and was never backported, and v1 rejects it with
// an invalid-choice error that reads as a broken profile.
func TestRequireAWSCLIRejectsVersionOne(t *testing.T) {
	t.Setenv("PATH", fakeAWSCLI(t, "aws-cli/1.42.5 Python/3.11.9 Darwin/25.6.0 botocore/1.42.5"))

	err := RequireAWSCLI(context.Background())
	if err == nil {
		t.Fatal("v1 cannot resolve credentials here and must be refused")
	}
	if !errors.Is(err, ErrOldAWSCLI) {
		t.Errorf("callers match on ErrOldAWSCLI: %v", err)
	}
	if !strings.Contains(err.Error(), "export-credentials") {
		t.Errorf("the refusal must say what v2 has that v1 does not:\n%v", err)
	}
}

// A banner this parser does not recognise is ACCEPTED. A wrapper, a shim or a future
// format change must not be refused by a parser guessing at it — the real command's
// own error is a better last word than a false one here.
func TestRequireAWSCLIAcceptsAnUnreadableVersion(t *testing.T) {
	for _, banner := range []string{
		"some vendored wrapper 3.1",
		"aws-cli/unreleased",
		"", // prints nothing at all
	} {
		t.Setenv("PATH", fakeAWSCLI(t, banner))
		if err := RequireAWSCLI(context.Background()); err != nil {
			t.Errorf("%q cannot be judged, so it must pass: %v", banner, err)
		}
	}
}

// fakeAWSCLI writes an executable that prints banner for `--version`, and returns the
// directory to put on PATH.
//
// The name carries .exe on Windows: exec.LookPath there resolves only the extensions
// in PATHEXT, so an extensionless fixture is invisible to the very call under test.
func fakeAWSCLI(t *testing.T, banner string) string {
	t.Helper()
	dir := t.TempDir()

	name, script := "aws", "#!/bin/sh\nprintf '%s\\n' \""+banner+"\"\n"
	if runtime.GOOS == "windows" {
		name, script = "aws.bat", "@echo "+banner+"\r\n"
		// LookPath needs the extension to be one PATHEXT lists.
		t.Setenv("PATHEXT", ".BAT;.EXE;.CMD")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
