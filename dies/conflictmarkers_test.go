package dies

import (
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

const conflicted = `package main

<<<<<<< HEAD
const greeting = "ours"
=======
const greeting = "theirs"
>>>>>>> branch
`

func TestConflictMarkersSaysNothingAboutACleanRepo(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, ConflictMarkers{})

	if check := measured.Fold(reconcile.LensCheck); check.Status != reconcile.Converged {
		t.Errorf("check status = %q, want converged", check.Status)
	}
}

func TestConflictMarkersReportsACommittedConflictWithItsLine(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{"main.go": conflicted})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, ConflictMarkers{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %d, want 1: %+v", len(measured.Changes), measured.Changes)
	}
	if measured.Changes[0].Item != "main.go" {
		t.Errorf("item = %q, want main.go", measured.Changes[0].Item)
	}
	if measured.Changes[0].Observed != "line 3" {
		t.Errorf("observed = %q, want line 3", measured.Changes[0].Observed)
	}
	if measured.Changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand", measured.Changes[0].Repair)
	}
}

// The live false positive: a document explaining how to resolve a conflict
// contains a complete conflict, at the start of its lines, inside a fence.
// terminal-library/workflows/git-merge-conflicts.md is the case, and a die
// reporting it every run is a die nobody reads.
func TestConflictMarkersIgnoresAnExampleInsideAMarkdownFence(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"workflows/git-merge-conflicts.md": "# Resolving\n\n```text\n" + conflicted + "```\n",
	})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, ConflictMarkers{})

	if len(measured.Changes) != 0 {
		t.Errorf("a fenced example was reported as a conflict: %+v", measured.Changes)
	}
}

// A fence hides an example, not everything after it. A real conflict below a
// closed fence still counts.
func TestConflictMarkersStillSeesAConflictAfterAClosedFence(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"notes.md": "# Notes\n\n```text\nexample\n```\n\n" + conflicted,
	})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, ConflictMarkers{})

	if len(measured.Changes) != 1 {
		t.Fatalf("a conflict after a closed fence was missed: %+v", measured.Changes)
	}
}

func TestFirstMarkerLineIgnoresMarkersThatAreNotAtTheStartOfALine(t *testing.T) {
	// A diff quoted mid-sentence, which is prose rather than a conflict.
	prose := []byte("The marker <<<<<<< HEAD appears in running text.\n")
	if got := firstMarkerLine(prose, false); got != 0 {
		t.Errorf("line = %d, want 0 for a mid-line marker", got)
	}
}

func TestConflictMarkersSkipsBinaryFiles(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"asset.bin": "\x00\x01<<<<<<< HEAD\nbinary\n",
	})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, ConflictMarkers{})

	if len(measured.Changes) != 0 {
		t.Errorf("a binary file was reported: %+v", measured.Changes)
	}
}
