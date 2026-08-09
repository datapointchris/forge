package dies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/v5/config"
	"github.com/datapointchris/forge/v5/reconcile"
)

// syncedPath is where the die should put a repo-relative directory.
func syncedPath(target reconcile.Target, name string) string {
	return filepath.Join(sandbox(target), "sync", target.Repo.Name, name)
}

func TestPlanningLinksAnAbsentDirectory(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, Planning{})

	link, err := os.Readlink(target.Path(".planning"))
	if err != nil {
		t.Fatalf(".planning is not a symlink: %s", err)
	}
	if want := syncedPath(target, "planning"); link != want {
		t.Errorf("link = %s, want %s", link, want)
	}
}

func TestPlanningMigratesARealDirectory(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".planning/status.md": "# Planning Status\n",
		".planning/design.md": "# Design\n",
	})

	applyAll(t, target, Planning{})

	if _, err := os.Readlink(target.Path(".planning")); err != nil {
		t.Fatalf(".planning was not replaced with a symlink: %s", err)
	}
	moved := readFile(t, filepath.Join(syncedPath(target, "planning"), "status.md"))
	if moved != "# Planning Status\n" {
		t.Errorf("content did not migrate: %q", moved)
	}
	// Reachable through the link is the whole point of the migration.
	if through := readFile(t, target.Path(".planning", "status.md")); through != "# Planning Status\n" {
		t.Errorf("content not reachable through the link: %q", through)
	}
}

// The synced copy is what other machines have been editing. Moving this repo's
// copy over it would lose the newer content silently.
func TestPlanningRefusesToMigrateOverADivergedCopy(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".planning/status.md": "local edit\n"})

	synced := syncedPath(target, "planning")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "status.md"), []byte("the other machine's newer edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, Planning{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1", measured.Changes)
	}
	if measured.Changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand — migrating would lose the newer side", measured.Changes[0].Repair)
	}

	applyAll(t, target, Planning{})

	if got := readFile(t, filepath.Join(synced, "status.md")); got != "the other machine's newer edit\n" {
		t.Errorf("apply overwrote the synced copy: %q", got)
	}
	if got := readFile(t, target.Path(".planning", "status.md")); got != "local edit\n" {
		t.Errorf("apply moved the local copy anyway: %q", got)
	}
}

// Identical files are not a conflict — that is a repo already in sync whose
// link was lost, and refusing it would strand the repo.
func TestPlanningMigratesWhenTheCopiesMatch(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".planning/status.md": "same\n"})

	synced := syncedPath(target, "planning")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "status.md"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	applyAll(t, target, Planning{})

	if _, err := os.Readlink(target.Path(".planning")); err != nil {
		t.Errorf("an already-matching directory was not linked: %s", err)
	}
}

// Repointing a deliberate link is not a repair; it is overruling a decision
// forge cannot see the reason for.
func TestPlanningLeavesAForeignSymlinkAlone(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	elsewhere := filepath.Join(sandbox(target), "somewhere-else")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, target.Path(".planning")); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, Planning{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.ByHand {
		t.Fatalf("changes = %v, want one by-hand finding", measured.Changes)
	}

	applyAll(t, target, Planning{})

	link, _ := os.Readlink(target.Path(".planning"))
	if link != elsewhere {
		t.Errorf("apply repointed a deliberate link: %s", link)
	}
}

func TestPlanningIsIdempotent(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, Planning{})

	if measured := reconcile.Assess(target, Planning{}); len(measured.Changes) != 0 {
		t.Errorf("a second look found %v, want nothing", measured.Changes)
	}
}

// A correct link whose target was deleted is drift the die can repair — the
// repo is left pointing at nothing until it does.
func TestPlanningRecreatesAMissingTarget(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	applyAll(t, target, Planning{})

	if err := os.RemoveAll(syncedPath(target, "planning")); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, Planning{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.Automatic {
		t.Fatalf("changes = %v, want one automatic repair", measured.Changes)
	}

	applyAll(t, target, Planning{})
	if info, err := os.Stat(syncedPath(target, "planning")); err != nil || !info.IsDir() {
		t.Error("the missing synced directory was not recreated")
	}
}

// The fleet-specific `if repo_name = ichrisbirch` is a registry field now, so
// the tool carries the operation and the repo carries the fact.
func TestPlanningSyncsDeclaredExtraDirectories(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{"stats/data/rows.csv": "a,b\n"})
	target.Repo.SyncedDirs = []config.SyncedDir{{Dir: "stats/data", As: "stats"}}

	applyAll(t, target, Planning{})

	if _, err := os.Readlink(target.Path("stats/data")); err != nil {
		t.Fatalf("a declared synced dir was not linked: %s", err)
	}
	if got := readFile(t, filepath.Join(syncedPath(target, "stats"), "rows.csv")); got != "a,b\n" {
		t.Errorf("declared synced dir did not migrate: %q", got)
	}
}

func TestPlanningMergesDisjointFiles(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".planning/local.md": "local\n"})

	synced := syncedPath(target, "planning")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "remote.md"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	applyAll(t, target, Planning{})

	for name, want := range map[string]string{"local.md": "local\n", "remote.md": "remote\n"} {
		if got := readFile(t, target.Path(".planning", name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Basenames are neither unique (two `homelab` repos) nor always equal to the
// registry name (`zmk-config-corne42` lives at ~/code/zmk/corne42), so the
// synced path is keyed on the registry name and never on the directory.
func TestPlanningKeysTheSyncedPathOnTheRegistryName(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	target.Repo.Name = "zmk-config-corne42"

	applyAll(t, target, Planning{})

	link, err := os.Readlink(target.Path(".planning"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(sandbox(target), "sync", "zmk-config-corne42", "planning"); link != want {
		t.Errorf("link = %s, want %s — keyed on the directory basename, not the registry name", link, want)
	}
}
