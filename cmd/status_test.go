package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/v4/config"
)

// newRepoWithPlanning builds a repo dir whose .planning is a symlink to dest,
// mirroring the layout the sync-planning die produces.
func newRepoWithPlanning(t *testing.T, root, name, dest string) config.Repo {
	t.Helper()
	repoDir := filepath.Join(root, name)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dest, filepath.Join(repoDir, ".planning")); err != nil {
		t.Fatal(err)
	}
	return config.Repo{Name: name, Path: repoDir}
}

func writePlanning(t *testing.T, dir, status string, docs ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if err := os.WriteFile(filepath.Join(dir, "status.md"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range docs {
		if err := os.WriteFile(filepath.Join(dir, d), []byte("# doc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Two repos can share a registry name (the portfolio ~/homelab and khuedoan's
// reference clone). Planning must resolve through each repo's own .planning, so
// one repo's docs are never attributed to the other.
func TestCollectRepoStatusSameNameDifferentPaths(t *testing.T) {
	root := t.TempDir()

	minePlanning := filepath.Join(root, "synced", "homelab", "planning")
	writePlanning(t, minePlanning, "# Planning Status\n\nMine.", "k3s-migration.md")
	mine := newRepoWithPlanning(t, root, "homelab", minePlanning)

	// The reference clone lives elsewhere and has no .planning at all.
	cloneDir := filepath.Join(root, "refs", "homelab")
	if err := os.MkdirAll(cloneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clone := config.Repo{Name: "homelab", Path: cloneDir, Owner: "khuedoan"}

	gotMine := collectRepoStatus(mine)
	if gotMine.statusMD == "" {
		t.Error("portfolio repo lost its status.md")
	}
	if len(gotMine.designDocs) != 1 || gotMine.designDocs[0] != "k3s-migration.md" {
		t.Errorf("designDocs = %v, want [k3s-migration.md]", gotMine.designDocs)
	}

	gotClone := collectRepoStatus(clone)
	if gotClone.hasPlanningContent() {
		t.Errorf("reference clone inherited planning content: %q / %v",
			gotClone.statusMD, gotClone.designDocs)
	}
}

// A repo whose .planning is a real directory still renders, but is flagged so
// the brief can warn that Syncthing never sees those docs.
func TestCollectRepoStatusUnsyncedPlanningDir(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "dectl")
	writePlanning(t, filepath.Join(repoDir, ".planning"), "# Planning Status\n\nLocal only.")

	got := collectRepoStatus(config.Repo{Name: "dectl", Path: repoDir})
	if got.statusMD == "" {
		t.Error("unsynced planning content should still be collected")
	}
	if !got.unsynced {
		t.Error("real .planning dir should be reported as unsynced")
	}
}

func TestCollectRepoStatusNoPlanning(t *testing.T) {
	got := collectRepoStatus(config.Repo{Name: "bare", Path: t.TempDir()})
	if got.hasPlanningContent() || got.unsynced {
		t.Errorf("bare repo should have no content and no warning, got %+v", got)
	}
}
