package dies

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPlanningDie runs the sync-planning die with HOME pointed at a temp tree, so
// the die resolves $HOME/dev/repos there instead of the real synced directory.
func runPlanningDie(t *testing.T, home, repoDir, repoName string) (string, int) {
	t.Helper()
	script := filepath.Join(forgeRoot(t), "dies", "maintenance", "sync-planning.sh")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "HOME="+home, "FORGE_REPO_NAME="+repoName)

	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// makePlanningFixture returns (home, repoDir, syncedDir) with the given files
// written to each side.
func makePlanningFixture(t *testing.T, repoFiles, syncedFiles map[string]string) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	repoDir := filepath.Join(home, "repo")
	syncedDir := filepath.Join(home, "dev", "repos", "repo", "planning")

	write := func(dir string, files map[string]string) {
		if files == nil {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(filepath.Join(repoDir, ".planning"), repoFiles)
	write(syncedDir, syncedFiles)

	return home, repoDir, syncedDir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}
	return string(content)
}

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %s", path, err)
	}
	return info.Mode()&os.ModeSymlink != 0
}

// The migration is a plain mv, so a same-named file on both sides used to have
// the repo's unsynced copy silently overwrite the one other machines had been
// editing. Verified against relate, whose synced status.md was clobbered by a
// four-month-old repo copy.
func TestSyncPlanning_RefusesToClobberDivergedSyncedFile(t *testing.T) {
	home, repoDir, syncedDir := makePlanningFixture(t,
		map[string]string{"status.md": "stale repo copy\n"},
		map[string]string{"status.md": "newer synced copy\n"})

	out, code := runPlanningDie(t, home, repoDir, "repo")
	if code != 1 {
		t.Fatalf("expected error exit, got %d: %s", code, out)
	}
	if !strings.Contains(out, "status.md") {
		t.Errorf("message must name the conflicting file so it can be merged, got: %s", out)
	}
	if got := readFile(t, filepath.Join(syncedDir, "status.md")); got != "newer synced copy\n" {
		t.Errorf("synced copy was overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(repoDir, ".planning", "status.md")); got != "stale repo copy\n" {
		t.Errorf("repo copy was moved despite the refusal: %q", got)
	}
	if isSymlink(t, filepath.Join(repoDir, ".planning")) {
		t.Error("refused migration must leave .planning a real directory for a human to merge")
	}
}

// The common case for a machine that simply never ran the die: both sides hold
// the same content, so there is nothing to lose and the migration proceeds.
func TestSyncPlanning_IdenticalFileIsNotAConflict(t *testing.T) {
	home, repoDir, syncedDir := makePlanningFixture(t,
		map[string]string{"status.md": "same content\n"},
		map[string]string{"status.md": "same content\n"})

	out, code := runPlanningDie(t, home, repoDir, "repo")
	if code != 0 {
		t.Fatalf("expected OK, got exit %d: %s", code, out)
	}
	if !isSymlink(t, filepath.Join(repoDir, ".planning")) {
		t.Error(".planning was not replaced with a symlink")
	}
	if got := readFile(t, filepath.Join(syncedDir, "status.md")); got != "same content\n" {
		t.Errorf("content changed: %q", got)
	}
}

func TestSyncPlanning_MergesDisjointFiles(t *testing.T) {
	home, repoDir, syncedDir := makePlanningFixture(t,
		map[string]string{"design.md": "from repo\n"},
		map[string]string{"status.md": "from synced\n"})

	out, code := runPlanningDie(t, home, repoDir, "repo")
	if code != 0 {
		t.Fatalf("expected OK, got exit %d: %s", code, out)
	}
	for name, want := range map[string]string{"design.md": "from repo\n", "status.md": "from synced\n"} {
		if got := readFile(t, filepath.Join(syncedDir, name)); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	if !isSymlink(t, filepath.Join(repoDir, ".planning")) {
		t.Error(".planning was not replaced with a symlink")
	}
}

func TestSyncPlanning_IsIdempotent(t *testing.T) {
	home, repoDir, _ := makePlanningFixture(t,
		map[string]string{"status.md": "content\n"}, nil)

	if _, code := runPlanningDie(t, home, repoDir, "repo"); code != 0 {
		t.Fatalf("first run should migrate, got exit %d", code)
	}

	out, code := runPlanningDie(t, home, repoDir, "repo")
	if code != exitSkip {
		t.Fatalf("second run should SKIP, got exit %d: %s", code, out)
	}
}

// The registry name is used rather than the directory basename, because
// basenames collide and can differ from the name the synced tree is keyed on.
func TestSyncPlanning_UsesRegistryNameForSyncedPath(t *testing.T) {
	home, repoDir, _ := makePlanningFixture(t,
		map[string]string{"status.md": "content\n"}, nil)

	if _, code := runPlanningDie(t, home, repoDir, "other-name"); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	target := filepath.Join(home, "dev", "repos", "other-name", "planning")
	if got := readFile(t, filepath.Join(target, "status.md")); got != "content\n" {
		t.Errorf("file did not land under the registry name: %q", got)
	}
}
