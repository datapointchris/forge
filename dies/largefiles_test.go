package dies

import (
	"os"
	"strings"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

func TestLargeFilesSaysNothingAboutARepoOfOrdinaryFiles(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"main.go":   "package main\n",
		"README.md": strings.Repeat("a line\n", 200),
	})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, LargeFiles{})

	if check := measured.Fold(reconcile.LensCheck); check.Status != reconcile.Converged {
		t.Errorf("check status = %q, want converged", check.Status)
	}
}

func TestLargeFilesReportsATrackedFileOverTheThreshold(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"main.go":  "package main\n",
		"blob.bin": strings.Repeat("x", largeEnoughToQuestion+1),
	})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, LargeFiles{})
	changes := measured.Changes

	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1: %+v", len(changes), changes)
	}
	if changes[0].Item != "blob.bin" {
		t.Errorf("item = %q, want blob.bin", changes[0].Item)
	}
	// Never automatic. Deleting a file from a repo is not forge's call, and a
	// plan that offered to would be a plan that could run.
	if changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand", changes[0].Repair)
	}
	if plan := measured.Fold(reconcile.LensPlan); plan.Status != reconcile.Converged {
		t.Errorf("plan status = %q, want converged — apply must have nothing to do", plan.Status)
	}
}

// The set is what git tracks, not what the directory holds. A walk would report
// every dependency tree in the fleet, every run.
func TestLargeFilesIgnoresAnUntrackedFile(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"main.go":    "package main\n",
		".gitignore": "ignored/\n",
	})
	initRepo(t, target, "main")

	if err := os.MkdirAll(target.Path("ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(target.Path("ignored", "blob.bin"), strings.Repeat("x", largeEnoughToQuestion+1)); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, LargeFiles{})

	if len(measured.Changes) != 0 {
		t.Errorf("an untracked file was reported: %+v", measured.Changes)
	}
}

func TestLargeFilesSaysSoWhenThereIsNoCheckout(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{"main.go": "package main\n"})

	measured := reconcile.Assess(target, LargeFiles{})

	if len(measured.Changes) != 0 {
		t.Errorf("a directory that is not a repo produced changes: %+v", measured.Changes)
	}
}

func TestHumanBytesReadsAtEveryScale(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{512, "512B"},
		{2048, "2.0KB"},
		{3 << 20, "3.0MB"},
		{2 << 30, "2.0GB"},
	} {
		if got := humanBytes(tc.bytes); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
