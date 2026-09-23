package main

import (
	"bytes"
	"strings"
	"testing"
)

// The reason this command exists: a run verifies each dependency immediately
// before the first call that needs it, so a bare machine surfaces one gap per
// round. The report has to list every failure at once, or it buys nothing over
// the ordinary flow.
func TestReportListsEveryFailureAtOnce(t *testing.T) {
	results := []checkResult{
		{name: "aws", summary: "not usable", detail: "install the AWS CLI v2", ok: false},
		{name: "terraform", summary: "not usable", detail: "terraform not found in PATH", ok: false},
		{name: "git", summary: "/usr/bin/git", ok: true},
		{name: "templates", summary: "not found", detail: "no checkout found", ok: false},
	}

	var out bytes.Buffer
	err := reportChecks(&out, results)

	if err == nil {
		t.Fatal("reportChecks returned nil with three failures; the exit code is what makes this usable as a CI gate")
	}
	for _, name := range []string{"aws", "terraform", "templates"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name the failing check %q: %v", name, err)
		}
	}
	for _, remediation := range []string{
		"install the AWS CLI v2",
		"terraform not found in PATH",
		"no checkout found",
	} {
		if !strings.Contains(out.String(), remediation) {
			t.Errorf("report dropped a remediation: %q\n%s", remediation, out.String())
		}
	}
}

func TestReportSucceedsWhenEverythingIsPresent(t *testing.T) {
	results := []checkResult{
		{name: "aws", summary: "/usr/local/bin/aws", ok: true},
		{name: "git", summary: "/usr/bin/git", ok: true},
	}

	var out bytes.Buffer
	if err := reportChecks(&out, results); err != nil {
		t.Fatalf("reportChecks on an all-ok report = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "all ok") {
		t.Errorf("report does not say it passed:\n%s", out.String())
	}
}

// The command promises to touch no AWS API, which is what lets it answer "can
// this machine run the tool" without a configured profile. Saying so in the
// output is part of the contract.
func TestReportSaysNoAWSCallWasMade(t *testing.T) {
	var out bytes.Buffer
	if err := reportChecks(&out, []checkResult{{name: "git", summary: "/usr/bin/git", ok: true}}); err != nil {
		t.Fatalf("reportChecks = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "No AWS call was made") {
		t.Errorf("report does not state that no AWS call was made:\n%s", out.String())
	}
}

// A passing row still names the path, because "ok" alone hides the case where
// PATH resolves to a different binary than the operator expects.
func TestReportKeepsThePathOfAPassingCheck(t *testing.T) {
	var out bytes.Buffer
	_ = reportChecks(&out, []checkResult{{name: "aws", summary: "/opt/homebrew/bin/aws", ok: true}})

	if !strings.Contains(out.String(), "/opt/homebrew/bin/aws") {
		t.Errorf("report dropped the resolved path:\n%s", out.String())
	}
}

// git is the one dependency every machine is likely to have, so it is the only
// check that can run here without assuming an installed toolchain.
func TestCheckGitReportsTheResolvedBinary(t *testing.T) {
	result := checkGit()

	if result.name != "git" {
		t.Errorf("name = %q, want %q", result.name, "git")
	}
	if result.ok && !strings.Contains(result.summary, "git") {
		t.Errorf("a passing git check does not name the binary: %q", result.summary)
	}
	if !result.ok && result.detail == "" {
		t.Error("a failing check carries no remediation, which is the whole value of the row")
	}
}

func TestBinaryPathSaysSoWhenTheBinaryIsAbsent(t *testing.T) {
	got := binaryPath("lerian-infra-a-binary-that-does-not-exist")

	if !strings.Contains(got, "not found") {
		t.Errorf("binaryPath on a missing binary = %q, want it to say not found", got)
	}
}
