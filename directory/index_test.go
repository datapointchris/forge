package directory

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/forge/config"
)

func TestCacheHomeFollowsXDG(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	if got, want := CacheHome(), filepath.Join(cacheHome, "forge"); got != want {
		t.Errorf("CacheHome() = %q, want %q", got, want)
	}

	t.Setenv("XDG_CACHE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := CacheHome(), filepath.Join(home, ".cache", "forge"); got != want {
		t.Errorf("CacheHome() = %q, want %q", got, want)
	}
}

// The property the whole design turns on. A .git inside a Syncthing folder does
// not announce itself — it conflicts later, on a peer, after two machines have
// both run hooks that write.
func TestIndexIsNeverInsideTheWorkTree(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	for _, path := range []string{"~/.claude", "/home/chris/dev", t.TempDir()} {
		index := IndexFor(config.Repo{Name: "sample", Path: path})
		inside, err := index.insideWorkTree()
		if err != nil {
			t.Fatalf("insideWorkTree(%s): %s", path, err)
		}
		if inside {
			t.Errorf("index %s is inside its work tree %s", index.GitDir, index.WorkTree)
		}
	}
}

// The one arrangement a string comparison gets wrong: a cache reached through a
// link that lands inside the tree. Lexically the two paths diverge immediately,
// so filepath.Abs alone reports "outside" and a .git is created in a Syncthing
// folder — the exact failure the package exists to prevent.
func TestSymlinkedCacheIsSeenAsInsideTheWorkTree(t *testing.T) {
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "cache-link")
	if err := os.Symlink(filepath.Join(tree, "cache"), link); err != nil {
		t.Skipf("symlinks are unavailable: %s", err)
	}

	index := Index{Name: "sample", GitDir: filepath.Join(link, "sample.git"), WorkTree: tree}
	inside, err := index.insideWorkTree()
	if err != nil {
		t.Fatalf("insideWorkTree: %s", err)
	}
	if !inside {
		t.Errorf("a git dir resolving to %s was not seen as inside %s",
			filepath.Join(tree, "cache", "sample.git"), tree)
	}
	if err := index.Ensure(); err == nil {
		t.Error("Ensure created an index inside the tree it indexes")
	}
}

// The work tree side of the same problem, and the reason realPath resolves both:
// a declared path reached through a link must compare as the directory it names.
func TestASymlinkedWorkTreeStillMatchesItsOwnIndex(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "tree-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are unavailable: %s", err)
	}

	index := Index{Name: "sample", GitDir: filepath.Join(real, ".git"), WorkTree: link}
	inside, err := index.insideWorkTree()
	if err != nil {
		t.Fatalf("insideWorkTree: %s", err)
	}
	if !inside {
		t.Errorf("%s was not seen as inside %s, which is the same directory", index.GitDir, link)
	}
}

// And when one would be, every write path refuses rather than proceeding.
func TestEnsureAndDiscardRefuseAnIndexInsideTheWorkTree(t *testing.T) {
	tree := t.TempDir()
	index := Index{Name: "bad", GitDir: filepath.Join(tree, ".git"), WorkTree: tree}

	if err := index.Ensure(); err == nil {
		t.Error("Ensure wrote a git dir into the tree it indexes")
	}
	if err := index.Discard(); err == nil {
		t.Error("Discard would have removed a directory inside the work tree")
	}
	if _, err := os.Stat(filepath.Join(tree, ".git")); err == nil {
		t.Error("a .git was created inside the work tree")
	}
}

// pre-commit re-invokes git itself, and only the environment reaches those
// calls. A --git-dir flag would configure the one process forge starts and none
// of the ones that decide which files exist — a regression that breaks nothing
// visible until a hook sees the wrong file list.
func TestEnvCarriesGitDirAndWorkTree(t *testing.T) {
	index := Index{Name: "claude", GitDir: "/cache/claude.git", WorkTree: "/home/chris/.claude"}

	env := index.Env()
	for _, want := range []string{
		"GIT_DIR=/cache/claude.git",
		"GIT_WORK_TREE=/home/chris/.claude",
		"FORGE_DIRECTORY_NAME=claude",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("Env() is missing %q", want)
		}
	}
}

// Two directories must not share an index, or each would lint the other's
// files. Basenames are not unique across the fleet; declared names are.
func TestIndexIsKeyedOnTheDeclaredName(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	first := IndexFor(config.Repo{Name: "dev", Path: "/home/chris/dev"})
	second := IndexFor(config.Repo{Name: "work-dev", Path: "/home/chris/work/dev"})

	if first.GitDir == second.GitDir {
		t.Errorf("two directories share the index %s", first.GitDir)
	}
}

func TestCheckGeneratedRequiresAStampedConfig(t *testing.T) {
	tree := t.TempDir()

	if err := CheckGenerated(tree); err == nil {
		t.Error("a directory with no config was accepted")
	}

	handWritten := filepath.Join(tree, ConfigPath)
	if err := os.WriteFile(handWritten, []byte("repos:\n  - repo: local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckGenerated(tree); err == nil {
		t.Error("a hand-written config was accepted")
	}

	if err := os.WriteFile(handWritten, []byte(toolchainStamp+" 11\nrepos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckGenerated(tree); err != nil {
		t.Errorf("a stamped config was rejected: %s", err)
	}
}

// The end-to-end claim: a bare index outside the tree enumerates the tree, and
// the ordinary .gitignore decides what it holds. Real git, no network, and
// deliberately no `pre-commit run` — that needs a tool the suite must not
// require and a hook fetch it must not perform.
func TestIndexEnumeratesTheTreeAndHonorsGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	tree := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	write(t, tree, "README.md", "# sample\n")
	write(t, tree, "hooks/run.sh", "#!/usr/bin/env bash\n")
	write(t, tree, ".gitignore", "secrets/\n*.log\n")
	write(t, tree, "secrets/token.json", `{"token": "nope"}`)
	write(t, tree, "debug.log", "noise\n")

	index := IndexFor(config.Repo{Name: "sample", Path: tree})
	if err := index.Ensure(); err != nil {
		t.Fatalf("Ensure: %s", err)
	}
	if err := index.Refresh(); err != nil {
		t.Fatalf("Refresh: %s", err)
	}

	files, err := index.Files()
	if err != nil {
		t.Fatalf("Files: %s", err)
	}
	slices.Sort(files)

	want := []string{".gitignore", "README.md", "hooks/run.sh"}
	if !slices.Equal(files, want) {
		t.Errorf("indexed %v, want %v", files, want)
	}
	// Stated separately: an ignored secret reaching the index is the failure
	// that matters most, and a list comparison reports it as one diff among
	// several rather than as the thing to look at.
	for _, file := range files {
		if strings.HasPrefix(file, "secrets/") {
			t.Errorf("an ignored path entered the index: %s", file)
		}
	}

	if _, err := os.Stat(filepath.Join(tree, ".git")); err == nil {
		t.Error("a .git was created inside the indexed tree")
	}

	// A file removed since the last run must not linger: hooks would be handed
	// a path they cannot open.
	if err := os.Remove(filepath.Join(tree, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := index.Refresh(); err != nil {
		t.Fatalf("Refresh after delete: %s", err)
	}
	files, err = index.Files()
	if err != nil {
		t.Fatalf("Files after delete: %s", err)
	}
	if slices.Contains(files, "README.md") {
		t.Errorf("a deleted file survived in the index: %v", files)
	}

	if err := index.Discard(); err != nil {
		t.Fatalf("Discard: %s", err)
	}
	if _, err := os.Stat(index.GitDir); err == nil {
		t.Error("Discard left the index behind")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
