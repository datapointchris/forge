package dies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

// syncedPath is where the die should put a repo-relative directory.
func syncedPath(target reconcile.Target, name string) string {
	return filepath.Join(sandbox(target), "sync", target.Repo.Name, name)
}

// declaring is a fixture whose registry entry syncs stats/data as "stats".
func declaring(t *testing.T, files map[string]string) reconcile.Target {
	t.Helper()
	target := fixture(t, stacks("python"), files)
	target.Repo.SyncedDirs = []config.SyncedDir{{Dir: "stats/data", As: "stats"}}
	return target
}

func TestSyncedDirsLinksAnAbsentDirectory(t *testing.T) {
	target := declaring(t, nil)

	applyAll(t, target, SyncedDirs{})

	link, err := os.Readlink(target.Path("stats/data"))
	if err != nil {
		t.Fatalf("stats/data is not a symlink: %s", err)
	}
	if want := syncedPath(target, "stats"); link != want {
		t.Errorf("link = %s, want %s", link, want)
	}
}

func TestSyncedDirsMigratesARealDirectory(t *testing.T) {
	target := declaring(t, map[string]string{"stats/data/rows.csv": "a,b\n"})

	applyAll(t, target, SyncedDirs{})

	if _, err := os.Readlink(target.Path("stats/data")); err != nil {
		t.Fatalf("stats/data was not replaced with a symlink: %s", err)
	}
	if moved := readFile(t, filepath.Join(syncedPath(target, "stats"), "rows.csv")); moved != "a,b\n" {
		t.Errorf("content did not migrate: %q", moved)
	}
	// Reachable through the link is the whole point of the migration.
	if through := readFile(t, target.Path("stats/data", "rows.csv")); through != "a,b\n" {
		t.Errorf("content not reachable through the link: %q", through)
	}
}

// The synced copy is what other machines have been editing. Moving this repo's
// copy over it would lose the newer content silently.
func TestSyncedDirsRefusesToMigrateOverADivergedCopy(t *testing.T) {
	target := declaring(t, map[string]string{"stats/data/rows.csv": "local edit\n"})

	synced := syncedPath(target, "stats")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "rows.csv"), []byte("the other machine's newer edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, SyncedDirs{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1", measured.Changes)
	}
	if measured.Changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand — migrating would lose the newer side", measured.Changes[0].Repair)
	}

	applyAll(t, target, SyncedDirs{})

	if got := readFile(t, filepath.Join(synced, "rows.csv")); got != "the other machine's newer edit\n" {
		t.Errorf("apply overwrote the synced copy: %q", got)
	}
	if got := readFile(t, target.Path("stats/data", "rows.csv")); got != "local edit\n" {
		t.Errorf("apply moved the local copy anyway: %q", got)
	}
}

// Identical files are not a conflict — that is a repo already in sync whose
// link was lost, and refusing it would strand the repo.
func TestSyncedDirsMigratesWhenTheCopiesMatch(t *testing.T) {
	target := declaring(t, map[string]string{"stats/data/rows.csv": "same\n"})

	synced := syncedPath(target, "stats")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "rows.csv"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	applyAll(t, target, SyncedDirs{})

	if _, err := os.Readlink(target.Path("stats/data")); err != nil {
		t.Errorf("an already-matching directory was not linked: %s", err)
	}
}

// Repointing a deliberate link is not a repair; it is overruling a decision
// forge cannot see the reason for.
func TestSyncedDirsLeavesAForeignSymlinkAlone(t *testing.T) {
	target := declaring(t, nil)

	elsewhere := filepath.Join(sandbox(target), "somewhere-else")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target.Path("stats"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, target.Path("stats/data")); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, SyncedDirs{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.ByHand {
		t.Fatalf("changes = %v, want one by-hand finding", measured.Changes)
	}

	applyAll(t, target, SyncedDirs{})

	if link, _ := os.Readlink(target.Path("stats/data")); link != elsewhere {
		t.Errorf("apply repointed a deliberate link: %s", link)
	}
}

func TestSyncedDirsIsIdempotent(t *testing.T) {
	target := declaring(t, nil)

	applyAll(t, target, SyncedDirs{})

	if measured := reconcile.Assess(target, SyncedDirs{}); len(measured.Changes) != 0 {
		t.Errorf("a second look found %v, want nothing", measured.Changes)
	}
}

// A correct link whose target was deleted is drift the die can repair — the
// repo is left pointing at nothing until it does.
func TestSyncedDirsRecreatesAMissingTarget(t *testing.T) {
	target := declaring(t, nil)
	applyAll(t, target, SyncedDirs{})

	if err := os.RemoveAll(syncedPath(target, "stats")); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, SyncedDirs{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.Automatic {
		t.Fatalf("changes = %v, want one automatic repair", measured.Changes)
	}

	applyAll(t, target, SyncedDirs{})
	if info, err := os.Stat(syncedPath(target, "stats")); err != nil || !info.IsDir() {
		t.Error("the missing synced directory was not recreated")
	}
}

func TestSyncedDirsMergesDisjointFiles(t *testing.T) {
	target := declaring(t, map[string]string{"stats/data/local.csv": "local\n"})

	synced := syncedPath(target, "stats")
	if err := os.MkdirAll(synced, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "remote.csv"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	applyAll(t, target, SyncedDirs{})

	for name, want := range map[string]string{"local.csv": "local\n", "remote.csv": "remote\n"} {
		if got := readFile(t, target.Path("stats/data", name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Basenames are neither unique — a reference clone can share a name with a
// portfolio repo — nor always equal to the registry name, so the synced path is
// keyed on the registry name and never on the directory.
func TestSyncedDirsKeysTheSyncedPathOnTheRegistryName(t *testing.T) {
	target := declaring(t, nil)
	target.Repo.Name = "widget-config-alpha"

	applyAll(t, target, SyncedDirs{})

	link, err := os.Readlink(target.Path("stats/data"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(sandbox(target), "sync", "widget-config-alpha", "stats"); link != want {
		t.Errorf("link = %s, want %s — keyed on the directory basename, not the registry name", link, want)
	}
}

// A repo declaring nothing has nothing to link, so it needs no sync base and
// gains no .planning.
func TestSyncedDirsConvergesARepoDeclaringNothing(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	target.Config = nil

	measured := reconcile.Assess(target, SyncedDirs{})
	if measured.Refusal != "" || len(measured.Changes) != 0 {
		t.Fatalf("refusal %q, changes %v — want converged with no sync base", measured.Refusal, measured.Changes)
	}

	applied := fixture(t, stacks("go"), nil)
	applyAll(t, applied, SyncedDirs{})
	if _, err := os.Lstat(applied.Path(retiredLink)); !os.IsNotExist(err) {
		t.Errorf(".planning exists after apply: %v", err)
	}
}

func TestSyncedDirsRemovesARetiredLinkIntoTheSyncBase(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	planning := syncedPath(target, "planning")
	if err := os.MkdirAll(planning, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planning, "status.md"), []byte("## Current State\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planning, target.Path(retiredLink)); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, SyncedDirs{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.Automatic {
		t.Fatalf("changes = %v, want one automatic removal", measured.Changes)
	}

	applyAll(t, target, SyncedDirs{})

	if _, err := os.Lstat(target.Path(retiredLink)); !os.IsNotExist(err) {
		t.Errorf("the retired link survived apply: %v", err)
	}
	// Removing the link must never reach through it.
	if got := readFile(t, filepath.Join(planning, "status.md")); got != "## Current State\n" {
		t.Errorf("the synced status was touched: %q", got)
	}
}

// A link left dangling, which is what a machine holds once the sync base no
// longer has the directory it named, is still the die's to remove.
func TestSyncedDirsRemovesADanglingRetiredLink(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	if err := os.Symlink(syncedPath(target, "planning"), target.Path(retiredLink)); err != nil {
		t.Fatal(err)
	}

	applyAll(t, target, SyncedDirs{})

	if _, err := os.Lstat(target.Path(retiredLink)); !os.IsNotExist(err) {
		t.Errorf("the dangling retired link survived apply: %v", err)
	}
}

func TestSyncedDirsLeavesAPlanningLinkOutsideTheSyncBaseAlone(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	elsewhere := filepath.Join(sandbox(target), "somewhere-else")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, target.Path(retiredLink)); err != nil {
		t.Fatal(err)
	}

	if measured := reconcile.Assess(target, SyncedDirs{}); len(measured.Changes) != 0 {
		t.Fatalf("changes = %v, want none — a link aimed elsewhere is the repo's own", measured.Changes)
	}
}

func TestSyncedDirsRemovesAnEmptyRetiredDirectory(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	if err := os.MkdirAll(target.Path(retiredLink), 0o755); err != nil {
		t.Fatal(err)
	}

	applyAll(t, target, SyncedDirs{})

	if _, err := os.Lstat(target.Path(retiredLink)); !os.IsNotExist(err) {
		t.Errorf("the empty retired directory survived apply: %v", err)
	}
}

// Files in a real .planning were never synced, so nothing else holds them and
// only a person can say where they go.
func TestSyncedDirsReportsARetiredDirectoryWithFilesByHand(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".planning/status.md": "local only\n"})

	measured := reconcile.Assess(target, SyncedDirs{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.ByHand {
		t.Fatalf("changes = %v, want one by-hand finding", measured.Changes)
	}

	applyAll(t, target, SyncedDirs{})

	if got := readFile(t, target.Path(retiredLink, "status.md")); got != "local only\n" {
		t.Errorf("apply touched the unsynced files: %q", got)
	}
}
