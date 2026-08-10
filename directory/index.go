// Package directory runs a maintained directory's hooks.
//
// pre-commit cannot run without git. It shells out to `git ls-files`,
// `git diff` and `git rev-parse` to decide which files exist and which changed,
// and outside a repo the first of those exits 1 and pre-commit stops with
// "Is it installed, and are you in a Git repository directory?".
//
// What it actually needs is an index enumerating the files, and an index is
// cheap to fabricate. A bare repository somewhere else, pointed at the
// directory as its work tree, supplies one for the length of a run. No .git is
// created inside the directory itself — these are Syncthing folders, and a .git
// inside one is a conflict on every peer the moment two machines both have hooks
// that write.
package directory

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datapointchris/forge/v7/config"
)

// ConfigPath is the generated file whose hooks a run executes.
const ConfigPath = ".pre-commit-config.yaml"

// toolchainStamp marks the config as generated. A hand-written one is not
// something to run blind, for the same reason ci.Run aborts on an unstamped
// validate.yml.
const toolchainStamp = "# forge-toolchain:"

// Index is a throwaway git index over a directory git does not version.
type Index struct {
	Name     string
	GitDir   string
	WorkTree string
}

// CacheHome is where the indexes live.
//
// Cache, by data.md's own test: losing one costs a `git init` and a
// `git add -A`, which is seconds of recompute and no change in behavior. It is
// also per-machine by construction — an index over a replicated tree is wrong
// on every machine but the one that built it — and that section's closing line
// is exactly why a replicated cache does not belong in a synced directory.
func CacheHome() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "forge")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "forge")
	}
	return filepath.Join(home, ".cache", "forge")
}

// IndexFor addresses the index for one maintained directory.
//
// Keyed on the declared name rather than the basename: basenames are not unique
// across the fleet, and two directories sharing one index would each see the
// other's files.
func IndexFor(target config.Repo) Index {
	return Index{
		Name:     target.Name,
		GitDir:   filepath.Join(CacheHome(), "directories", target.Name+".git"),
		WorkTree: target.Path,
	}
}

// gitVars are the inherited variables that would aim git somewhere else.
//
// Stripped rather than trusted to be absent: pre-commit sets GIT_DIR and
// GIT_INDEX_FILE when it runs as a hook, so a forge invoked from inside one
// would otherwise index whatever repo started it.
var gitVars = []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_COMMON_DIR="}

func baseEnv() []string {
	var kept []string
	for _, entry := range os.Environ() {
		if slices.ContainsFunc(gitVars, func(prefix string) bool {
			return strings.HasPrefix(entry, prefix)
		}) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// Env carries the index into every git process in the run.
//
// Environment, never --git-dir flags. pre-commit invokes git itself, several
// times, and only the environment reaches those calls — a flag would configure
// the one process forge starts and none of the ones that matter.
func (i Index) Env() []string {
	return append(baseEnv(),
		"GIT_DIR="+i.GitDir,
		"GIT_WORK_TREE="+i.WorkTree,
		// Named for whoever is reading `ps` or a hook's own environment.
		"FORGE_DIRECTORY_NAME="+i.Name,
	)
}

// createEnv is Env without the work tree.
//
// `git init` refuses outright: "GIT_WORK_TREE not allowed without specifying
// GIT_DIR", which it reports even when GIT_DIR is set, because the repository
// it is being asked to create does not exist yet. The work tree only becomes
// meaningful once there is an index to attach it to.
func (i Index) createEnv() []string {
	return append(baseEnv(), "GIT_DIR="+i.GitDir)
}

// Ensure creates the index if it is not there.
func (i Index) Ensure() error {
	if inside, err := i.insideWorkTree(); err != nil {
		return err
	} else if inside {
		return fmt.Errorf("index %s is inside the directory it indexes; refusing to write a git dir into a synced tree", i.GitDir)
	}

	if _, err := os.Stat(filepath.Join(i.GitDir, "HEAD")); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(i.GitDir), 0o755); err != nil {
		return fmt.Errorf("creating the index directory: %w", err)
	}
	if err := i.gitWith(i.createEnv(), "init", "--quiet", "--bare", i.GitDir); err != nil {
		return err
	}
	// A bare repo has no work tree, and git versions disagree about whether
	// GIT_WORK_TREE overrides that. Saying so in the index's own config settles
	// it rather than depending on which git is installed.
	return i.gitWith(i.createEnv(), "config", "core.bare", "false")
}

// Refresh makes the index match the directory.
//
// Rebuilt every run rather than updated. The index is not a record of anything
// — a file deleted since the last run would otherwise linger in it and be
// handed to hooks that cannot open it.
func (i Index) Refresh() error {
	if err := i.git("add", "--all"); err != nil {
		return fmt.Errorf("indexing %s: %w", i.WorkTree, err)
	}
	return nil
}

// Files lists what the index holds, which is what a run will lint.
func (i Index) Files() ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Env = i.Env()
	cmd.Dir = i.WorkTree
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing indexed files: %w", err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Discard removes the index. It is a cache; the next run rebuilds it.
func (i Index) Discard() error {
	if inside, err := i.insideWorkTree(); err != nil {
		return err
	} else if inside {
		return fmt.Errorf("refusing to remove %s: it is inside the directory it indexes", i.GitDir)
	}
	return os.RemoveAll(i.GitDir)
}

// insideWorkTree is the invariant the whole design rests on. Checked rather
// than assumed, because the failure is silent: a .git appearing in a Syncthing
// folder does not announce itself until two machines have both written to it.
func (i Index) insideWorkTree() (bool, error) {
	gitDir, err := realPath(i.GitDir)
	if err != nil {
		return false, err
	}
	workTree, err := realPath(i.WorkTree)
	if err != nil {
		return false, err
	}
	// Both are absolute by here, so Rel only fails across Windows volumes. The
	// error is returned rather than read as "not inside": this check is the one
	// thing standing between a .git and a Syncthing folder, and a failure to
	// answer must not resolve to the permissive answer.
	rel, err := filepath.Rel(workTree, gitDir)
	if err != nil {
		return false, fmt.Errorf("comparing %s to %s: %w", i.GitDir, i.WorkTree, err)
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// realPath resolves a path through symlinks, which is what makes the check
// above physical rather than lexical. filepath.Abs cleans `..` and nothing
// else, so an XDG_CACHE_HOME that is itself a link into a maintained directory
// yields a git dir reading as outside the tree while landing inside it — the
// one arrangement that defeats a string comparison, and silently.
//
// Resolves the deepest component that exists and rejoins the rest, because the
// git dir is checked before it is created and EvalSymlinks fails on what is not
// there. The unresolved tail cannot be a link itself: it does not exist yet.
func realPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail string
	for current := abs; ; {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, tail), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolving %s: %w", path, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(current), tail)
		current = parent
	}
}

func (i Index) git(args ...string) error {
	return i.gitWith(i.Env(), args...)
}

func (i Index) gitWith(env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = env
	cmd.Dir = i.WorkTree
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ErrNotGenerated is returned when the directory has no generated config to run.
var ErrNotGenerated = errors.New("no generated .pre-commit-config.yaml")

// CheckGenerated reports whether the directory carries a config forge wrote.
//
// Running a hand-written config would be running something forge cannot
// account for, and running a missing one would report a green pass for a
// directory nothing has ever checked — the worse of the two.
func CheckGenerated(workTree string) error {
	data, err := os.ReadFile(filepath.Join(workTree, ConfigPath))
	if err != nil {
		return fmt.Errorf("%w in %s: run `forge directories apply precommit` first", ErrNotGenerated, workTree)
	}
	if !strings.HasPrefix(string(data), toolchainStamp) {
		return fmt.Errorf("%s in %s has no %s stamp, so it was hand-written: run `forge directories apply precommit` to adopt the standard",
			ConfigPath, workTree, toolchainStamp)
	}
	return nil
}

// ensureHookEnvironments installs the hook environments in a throwaway real
// repo, before the run that uses them.
//
// GIT_DIR and GIT_WORK_TREE are inherited by everything pre-commit spawns, and
// installing a hook means running its build backend. check-json5 builds with
// poetry, poetry asks git which files are ignored, and git — told the work tree
// is the maintained directory — reports a project root whose .git does not
// exist. The build fails at exit 128 for a reason that has nothing to do with
// the directory being checked.
//
// Installing first, somewhere git is ordinary, keeps that env away from the
// build. The environments land in pre-commit's own store keyed by hook repo and
// rev, so the real run finds them already there and spawns no backend at all.
// It is also idempotent and quiet once warm. The same throwaway-repo trick
// `forge toolchain plan` uses to ask pre-commit a question about a config that
// is not checked out anywhere.
func ensureHookEnvironments(workTree string, stderr io.Writer) error {
	config, err := os.ReadFile(filepath.Join(workTree, ConfigPath))
	if err != nil {
		return err
	}

	staging, err := os.MkdirTemp("", "forge-hooks-")
	if err != nil {
		return fmt.Errorf("creating the install directory: %w", err)
	}
	// Reported, not returned: the install already succeeded by then, and failing
	// the run over a temp directory that outlived it would discard a real answer
	// for a cosmetic problem.
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			_, _ = fmt.Fprintf(stderr, "could not remove %s: %s\n", staging, err)
		}
	}()

	if err := os.WriteFile(filepath.Join(staging, ConfigPath), config, 0o644); err != nil {
		return err
	}

	for _, args := range [][]string{
		{"git", "init", "--quiet"},
		{"pre-commit", "install-hooks"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = baseEnv()
		cmd.Dir = staging
		cmd.Stdout = stderr
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// Result is what one directory's run came to.
type Result struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Ran distinguishes "the hooks were executed" from "they were not".
	// Without it a directory that never ran reports Passed: false and reads
	// identically to one whose hooks found something, which are opposite
	// answers — one is drift to fix, the other is a broken setup.
	Ran bool `json:"ran"`
	// Files is how much was examined, so a pass over nothing is visible as one.
	Files int `json:"files"`
	// Passed is false when a hook failed or rewrote a file. pre-commit does not
	// distinguish the two: a formatter that fixed something exits 1 the same way
	// a linter that found something does, and both mean look at the diff.
	Passed bool `json:"passed"`
}

// Run builds the index and executes every pre-commit-stage hook against it.
//
// Output is streamed rather than captured. ci.md § "Show the full hook output
// when a commit fails" is the rule, and a summary of a hook failure is missing
// the part that says what to change.
func Run(index Index, stdout, stderr io.Writer) (Result, error) {
	result := Result{Name: index.Name, Path: index.WorkTree}

	if _, err := os.Stat(index.WorkTree); err != nil {
		return result, fmt.Errorf("%s: %w", index.Name, err)
	}
	if err := CheckGenerated(index.WorkTree); err != nil {
		return result, err
	}
	if _, err := exec.LookPath("pre-commit"); err != nil {
		return result, fmt.Errorf("pre-commit is not installed: %w", err)
	}

	if err := index.Ensure(); err != nil {
		return result, err
	}
	if err := index.Refresh(); err != nil {
		return result, err
	}

	files, err := index.Files()
	if err != nil {
		return result, err
	}
	result.Files = len(files)

	if err := ensureHookEnvironments(index.WorkTree, stderr); err != nil {
		return result, err
	}
	result.Ran = true

	cmd := exec.Command("pre-commit", "run", "--all-files")
	cmd.Env = index.Env()
	cmd.Dir = index.WorkTree
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	if err == nil {
		result.Passed = true
		return result, nil
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// Exit 1 is pre-commit's answer, not a failure to ask: hooks ran and
		// something is not clean. Anything else means pre-commit itself broke.
		if exit.ExitCode() == 1 {
			return result, nil
		}
	}
	return result, fmt.Errorf("pre-commit: %w", err)
}
