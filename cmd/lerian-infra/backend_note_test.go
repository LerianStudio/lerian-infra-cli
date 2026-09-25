package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/LerianStudio/lerian-infra-cli/pkg/infra"
)

// A dry run reports a missing backend file instead of failing, so the operator
// never sees infra.LoadBackend's error. That error carries two remediations, and
// the note has to carry both: an environment bootstrapped by someone else has
// the bucket already, so "run bootstrap apply" alone sends the operator into an
// apply that cannot succeed.
func TestMissingBackendNoteKeepsBothRemediations(t *testing.T) {
	if !strings.Contains(missingBackendNote, "--target bootstrap --action apply") {
		t.Error("note dropped the bootstrap remediation")
	}
	if !strings.Contains(missingBackendNote, "by hand") {
		t.Error("note dropped the hand-written remediation, which is the one that " +
			"applies when another operator bootstrapped the environment")
	}
	if !strings.Contains(missingBackendNote, "backend/README.md") {
		t.Error("note does not point at the README that documents the file's shape")
	}
}

// The note stands in for this error, so the two must not drift apart: whatever
// LoadBackend tells an operator who hits the failure, the dry run has to hint at
// the same set of ways out.
func TestMissingBackendNoteMatchesTheErrorItStandsInFor(t *testing.T) {
	layout := infra.Layout{Root: t.TempDir()}

	_, err := infra.LoadBackend(layout, "dev")
	if !errors.Is(err, infra.ErrNoBackendFile) {
		t.Fatalf("LoadBackend on an empty layout = %v, want ErrNoBackendFile", err)
	}

	for _, fragment := range []string{"bootstrap", "by hand"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("LoadBackend error no longer mentions %q; the dry-run note was "+
				"written to mirror it, so one of the two has drifted", fragment)
		}
	}
}
