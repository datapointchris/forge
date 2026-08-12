package dies

import (
	"os"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

func TestBrokenSymlinksSaysNothingAboutARepoWithNone(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{"main.go": "package main\n"})
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, BrokenSymlinks{})

	if check := measured.Fold(reconcile.LensCheck); check.Status != reconcile.Converged {
		t.Errorf("check status = %q, want converged", check.Status)
	}
}

func TestBrokenSymlinksLeavesAResolvingLinkAlone(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{"real.md": "content\n"})
	if err := os.Symlink("real.md", target.Path("alias.md")); err != nil {
		t.Fatal(err)
	}
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, BrokenSymlinks{})

	if len(measured.Changes) != 0 {
		t.Errorf("a link that resolves was reported: %+v", measured.Changes)
	}
}

func TestBrokenSymlinksReportsALinkToNothing(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{"main.go": "package main\n"})
	if err := os.Symlink("gone.md", target.Path("alias.md")); err != nil {
		t.Fatal(err)
	}
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, BrokenSymlinks{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %d, want 1: %+v", len(measured.Changes), measured.Changes)
	}
	if measured.Changes[0].Item != "alias.md" {
		t.Errorf("item = %q, want alias.md", measured.Changes[0].Item)
	}
	if measured.Changes[0].Observed != "→ gone.md" {
		t.Errorf("observed = %q, want the link target", measured.Changes[0].Observed)
	}
	// Never automatic: a link can legitimately be waiting for something this
	// machine has not installed.
	if measured.Changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand", measured.Changes[0].Repair)
	}
}

// A relative target resolves against the link's own directory. Computing that
// by hand is how a checker reports a working link as broken, so this pins that
// the die does not.
func TestBrokenSymlinksResolvesARelativeTargetFromTheLinksDirectory(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"docs/real.md": "content\n",
	})
	if err := os.Symlink("real.md", target.Path("docs", "alias.md")); err != nil {
		t.Fatal(err)
	}
	initRepo(t, target, "main")

	measured := reconcile.Assess(target, BrokenSymlinks{})

	if len(measured.Changes) != 0 {
		t.Errorf("a relative link inside a subdirectory was reported broken: %+v", measured.Changes)
	}
}

// An untracked dangling link is usually a machine fact — a tool not installed
// here — and reporting those would bury the finding that matters.
func TestBrokenSymlinksIgnoresAnUntrackedDanglingLink(t *testing.T) {
	target := unversionedFixture(t, stacks("go"), map[string]string{
		"main.go":    "package main\n",
		".gitignore": "local-*\n",
	})
	initRepo(t, target, "main")
	if err := os.Symlink("nowhere", target.Path("local-alias")); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, BrokenSymlinks{})

	if len(measured.Changes) != 0 {
		t.Errorf("an untracked dangling link was reported: %+v", measured.Changes)
	}
}
