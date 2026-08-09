package dies

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/v5/config"
	"github.com/datapointchris/forge/v5/reconcile"
	"github.com/datapointchris/forge/v5/toolchain"
)

// repoRoot walks up to the directory holding go.mod.
//
// Deliberately independent of the bash-era helpers: a Go die is exercised in
// process, so a test needs neither `forge` on PATH nor an XDG_DATA_HOME
// registry, and depending on that scaffolding would keep it alive after the
// scripts it served are gone.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %s", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// testAssets reads the real blocks, configs and manifest off disk, so a die is
// tested against what actually ships rather than a fake of it.
func testAssets(t *testing.T) reconcile.Assets {
	t.Helper()

	root := repoRoot(t)
	preCommit := os.DirFS(filepath.Join(root, "pre-commit"))

	manifest, err := toolchain.Load(preCommit)
	if err != nil {
		t.Fatalf("loading toolchain: %s", err)
	}

	return reconcile.Assets{
		PreCommit: preCommit,
		CI:        os.DirFS(filepath.Join(root, "ci", "blocks")),
		Manifest:  manifest,
	}
}

// fixture builds a repo in a temp directory and the Target that addresses it.
//
// The sync base is a sibling of the repo inside the same temp directory, so a
// die that writes outside the repo — planning links .planning into it — is
// still contained, and a test never reads or writes the real ~/dev/repos.
func fixture(t *testing.T, declared *config.Toolchain, files map[string]string) reconcile.Target {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %s", dir, err)
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %s", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %s", path, err)
		}
	}

	repo := config.Repo{Name: "fixture", Path: dir, Toolchain: declared}
	return reconcile.Target{
		Repo:   repo,
		Assets: testAssets(t),
		Config: &config.SyncerConfig{SyncBase: filepath.Join(root, "sync"), Repos: []config.Repo{repo}},
	}
}

// sandbox is the temp directory holding both the fixture repo and its sync
// base — everything a die is allowed to touch.
func sandbox(target reconcile.Target) string { return filepath.Dir(target.Repo.Path) }

func stacks(names ...string) *config.Toolchain {
	declared := &config.Toolchain{}
	for _, name := range names {
		declared.Components = append(declared.Components, config.Component{Stack: name, Dir: "."})
	}
	return declared
}

// snapshot records every file under dir by path and content, so a later
// comparison catches a modification, a deletion and a newly created file alike.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()

	tree := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		tree[rel] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotting %s: %s", dir, err)
	}
	return tree
}

func lines(t *testing.T, path string) []string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}

	var kept []string
	for _, line := range splitLines(string(content)) {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return kept
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func has(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}
	return string(content)
}
