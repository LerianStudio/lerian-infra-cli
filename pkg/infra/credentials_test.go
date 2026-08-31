package infra

import (
	"errors"
	"os"
	"path/filepath"
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

	err := RequireAWSCLI()
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

// And it stays quiet when the tool is there: a check that fires on a working
// machine is worse than no check.
func TestRequireAWSCLIPassesWhenTheBinaryIsOnPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := RequireAWSCLI(); err != nil {
		t.Errorf("aws is on PATH: %v", err)
	}
}
