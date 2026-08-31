package infra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	return fakeAWSCLIWith(t, awsScript{version: banner})
}

// awsScript is what the fake AWS CLI should do: what `--version` prints, and what
// any other invocation writes to each stream before exiting with exit.
type awsScript struct {
	version string
	stdout  string
	stderr  string
	exit    int
}

// fakeAWSCLIWith writes the fake and returns the directory to put on PATH. Only
// POSIX shell is generated for the credential cases, so those tests skip on Windows;
// the version cases need nothing but an echo and work on both.
func fakeAWSCLIWith(t *testing.T, script awsScript) string {
	t.Helper()
	dir := t.TempDir()

	if runtime.GOOS == "windows" {
		if script.stdout != "" || script.stderr != "" || script.exit != 0 {
			t.Skip("the credential fixture is a POSIX shell script")
		}
		// LookPath needs the extension to be one PATHEXT lists.
		t.Setenv("PATHEXT", ".BAT;.EXE;.CMD")
		body := "@echo " + script.version + "\r\n"
		if err := os.WriteFile(filepath.Join(dir, "aws.bat"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then printf '%s\\n' \"" + script.version + "\"; exit 0; fi\n" +
		"printf '%s' \"" + script.stdout + "\"\n" +
		"printf '%s' \"" + script.stderr + "\" >&2\n" +
		"exit " + strconv.Itoa(script.exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stdout and stderr are read apart, and only stdout is parsed. Merging them let a
// stderr line shaped like KEY=VALUE be read as a credential — a warning or a
// deprecation notice would become the access key the run then uses.
func TestResolveCredentialsIgnoresKeyValueLinesOnStderr(t *testing.T) {
	t.Setenv("PATH", fakeAWSCLIWith(t, awsScript{
		version: "aws-cli/2.31.22",
		stdout:  "AWS_ACCESS_KEY_ID=AKIAREAL\nAWS_SECRET_ACCESS_KEY=realsecret\n",
		stderr:  "AWS_ACCESS_KEY_ID=AKIAFROMSTDERR\n",
	}))

	credentials, err := ResolveCredentials(context.Background(), "lerian-dev")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "AKIAREAL" {
		t.Errorf("AccessKeyID = %q, want the value from stdout", credentials.AccessKeyID)
	}
	if credentials.SecretAccessKey != "realsecret" {
		t.Errorf("SecretAccessKey = %q", credentials.SecretAccessKey)
	}
}

// A non-zero exit AFTER credentials were printed must not put the secret into the
// error text — the caller prints that error, which is how a secret reaches a CI log.
func TestResolveCredentialsNeverPutsTheSecretInTheError(t *testing.T) {
	t.Setenv("PATH", fakeAWSCLIWith(t, awsScript{
		version: "aws-cli/2.31.22",
		stdout:  "AWS_ACCESS_KEY_ID=AKIAREAL\nAWS_SECRET_ACCESS_KEY=topsecretvalue\n",
		stderr:  "the SSO session has expired\n",
		exit:    255,
	}))

	_, err := ResolveCredentials(context.Background(), "lerian-dev")
	if err == nil {
		t.Fatal("a non-zero exit is a failure even when something was printed")
	}
	if strings.Contains(err.Error(), "topsecretvalue") {
		t.Errorf("the secret must never reach the error text:\n%v", err)
	}
	// The stderr the AWS CLI wrote is what diagnoses it, so that has to be there.
	if !strings.Contains(err.Error(), "SSO session has expired") {
		t.Errorf("the error must carry what the AWS CLI said:\n%v", err)
	}
}
