package lint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/gitenv"
	"github.com/datapointchris/forge/precommit"
)

const committedHooks = `repos:
  # generated:shell
  - repo: local
    hooks:
      - id: fixer
        name: fixer
        entry: true
        language: system
  # > custom:after:all - the repo's own
  - repo: local
    hooks:
      - id: needs-a-workstation
        name: needs a workstation
        entry: true
        language: system
`

// repoWith commits files into a fresh repository under a temporary home, so
// the clone lands in a cache the test owns.
func repoWith(t *testing.T, files map[string]string) config.Repo {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	gitIn(t, root, "init", "--quiet")
	for rel, body := range files {
		write(t, filepath.Join(root, rel), body)
		gitIn(t, root, "add", "--", rel)
	}
	gitIn(t, root, "commit", "--quiet", "--no-verify", "-m", "fixture")
	return config.Repo{Name: "fixture", Path: root}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitenv.WithoutRepoTarget(os.Environ()),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

const twoHooks = committedHooks + `  # generated:python
  - repo: local
    hooks:
      - id: typecheck
        name: typecheck
        entry: true
        language: system
`

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A fixer writes into whatever tree it runs in, and someone may be working in
// the checkout. So the hooks see HEAD in a clone, an uncommitted edit stays
// where it was, and the clone is gone afterwards.
func TestTheHooksRunInAThrowawayCloneOfHEAD(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks, "script.sh": "committed\n"})
	write(t, filepath.Join(repo.Path, "script.sh"), "uncommitted\n")

	var ran []string
	var seen, dir string
	result := RunWith(context.Background(), repo, func(_ context.Context, in, hook string) (int, string) {
		ran = append(ran, hook)
		dir = in
		body, _ := os.ReadFile(filepath.Join(in, "script.sh"))
		seen = string(body)
		write(t, filepath.Join(in, "script.sh"), "rewritten by a fixer\n")
		return 0, ""
	})

	if result.Outcome != Passed {
		t.Fatalf("outcome = %q (%s)", result.Outcome, result.Note)
	}
	if !slices.Equal(ran, []string{"fixer"}) {
		t.Errorf("ran %v, want the standard hook alone", ran)
	}
	if seen != "committed\n" {
		t.Errorf("the hook saw %q, want HEAD's content", seen)
	}
	if body, _ := os.ReadFile(filepath.Join(repo.Path, "script.sh")); string(body) != "uncommitted\n" {
		t.Errorf("the checkout was written: %q", body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the clone at %s outlived the run", dir)
	}
}

// CI runs the committed config, so a config nobody committed lints nothing.
func TestAnUncommittedConfigHasNoHooks(t *testing.T) {
	repo := repoWith(t, map[string]string{"script.sh": "committed\n"})
	write(t, filepath.Join(repo.Path, precommit.ConfigPath), committedHooks)

	result := RunWith(context.Background(), repo, func(context.Context, string, string) (int, string) {
		t.Error("a hook ran from a config HEAD does not hold")
		return 0, ""
	})
	if result.Outcome != NoHooks {
		t.Errorf("outcome = %q, want %q", result.Outcome, NoHooks)
	}
}

// The vue hooks resolve their tools in node_modules, which no clone carries.
// Each installed package is linked in. The caches tools keep in its
// dot-directories are the clone's own, so a hook's write never reaches the
// checkout, and removing the clone never removes what a link points at.
func TestInstalledPackagesAreLinkedAndTheirCachesStayInTheClone(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks, "web/package.json": "{}\n"})
	modules := filepath.Join(repo.Path, "web", "node_modules")
	write(t, filepath.Join(modules, "eslint", "package.json"), "{}\n")
	write(t, filepath.Join(modules, ".bin", "eslint"), "#!/bin/sh\n")
	write(t, filepath.Join(modules, ".cache", "checkout-only"), "\n")

	var sawPackage, sawBin, sawCache bool
	RunWith(context.Background(), repo, func(_ context.Context, in, _ string) (int, string) {
		linked := filepath.Join(in, "web", "node_modules")
		sawPackage = exists(filepath.Join(linked, "eslint", "package.json"))
		sawBin = exists(filepath.Join(linked, ".bin", "eslint"))
		sawCache = exists(filepath.Join(linked, ".cache", "checkout-only"))
		write(t, filepath.Join(linked, ".cache", "jiti", "config.cjs"), "\n")
		return 0, ""
	})

	if !sawPackage || !sawBin {
		t.Errorf("the hook saw package=%v .bin=%v, want both linked", sawPackage, sawBin)
	}
	if sawCache {
		t.Error("the hook saw the checkout's cache")
	}
	if exists(filepath.Join(modules, ".cache", "jiti")) {
		t.Error("a cache written in the clone reached the checkout")
	}
	if !exists(filepath.Join(modules, "eslint", "package.json")) {
		t.Error("removing the clone removed the installed packages")
	}
}

// npm ci runs postinstall in CI, and Nuxt's generates the types its typecheck
// reads. So the clone runs it against HEAD, and the checkout is not written.
func TestAPackagesPostinstallRunsInTheClone(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is not installed")
	}
	repo := repoWith(t, map[string]string{
		precommit.ConfigPath: committedHooks,
		"web/package.json":   `{"scripts":{"postinstall":"echo generated > .generated"}}` + "\n",
	})
	write(t, filepath.Join(repo.Path, "web", "node_modules", "eslint", "package.json"), "{}\n")

	var generated bool
	result := RunWith(context.Background(), repo, func(_ context.Context, in, _ string) (int, string) {
		generated = exists(filepath.Join(in, "web", ".generated"))
		return 0, ""
	})

	if result.Outcome != Passed {
		t.Fatalf("outcome = %q (%s)", result.Outcome, result.Note)
	}
	if !generated {
		t.Error("the hooks ran before postinstall generated its files")
	}
	if exists(filepath.Join(repo.Path, "web", ".generated")) {
		t.Error("postinstall wrote into the checkout")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// npm links a workspace package into node_modules, as node_modules/shared ->
// ../shared. Linked through, that resolves beside the checkout, and the hooks
// read an edit nobody committed. A scoped workspace sits one level down, and a
// .bin entry reaches one through node_modules.
func TestAWorkspacePackageResolvesToHEADNotTheCheckout(t *testing.T) {
	repo := repoWith(t, map[string]string{
		precommit.ConfigPath:     committedHooks,
		"package.json":           `{"workspaces": ["shared", "packages/util"]}` + "\n",
		"shared/index.js":        "committed\n",
		"packages/util/index.js": "committed\n",
	})
	write(t, filepath.Join(repo.Path, "shared", "index.js"), "uncommitted\n")
	write(t, filepath.Join(repo.Path, "packages", "util", "index.js"), "uncommitted\n")
	modules := filepath.Join(repo.Path, "node_modules")
	symlink(t, "../shared", filepath.Join(modules, "shared"))
	symlink(t, "../../packages/util", filepath.Join(modules, "@app", "util"))
	symlink(t, "../shared/index.js", filepath.Join(modules, ".bin", "shared"))

	seen := map[string]string{}
	RunWith(context.Background(), repo, func(_ context.Context, in, _ string) (int, string) {
		for _, rel := range []string{"shared/index.js", "@app/util/index.js", ".bin/shared"} {
			body, _ := os.ReadFile(filepath.Join(in, "node_modules", rel))
			seen[rel] = string(body)
		}
		return 0, ""
	})
	for rel, body := range seen {
		if body != "committed\n" {
			t.Errorf("node_modules/%s read %q, want HEAD's content", rel, body)
		}
	}
}

// A package staged in the checkout and not yet committed has no directory in
// the clone. It made the whole repo unknown with no hook run, and unknown
// leaves the exit code at 0.
func TestAPackageStagedButNotCommittedLeavesTheRepoLinted(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks})
	write(t, filepath.Join(repo.Path, "web", "package.json"), "{}\n")
	write(t, filepath.Join(repo.Path, "web", "node_modules", "eslint", "package.json"), "{}\n")
	gitIn(t, repo.Path, "add", "--", "web/package.json")

	ran := 0
	result := RunWith(context.Background(), repo, func(context.Context, string, string) (int, string) {
		ran++
		return 0, ""
	})
	if result.Outcome != Passed || ran != 1 {
		t.Errorf("outcome = %q after %d hooks (%s), want passed after 1", result.Outcome, ran, result.Note)
	}
}

// Killing pre-commit alone left its hook's tool running in a clone about to
// be removed, and the pipe that tool held kept the wait open past the deadline.
func TestAHookPastItsDeadlineIsStoppedWithEverythingItStarted(t *testing.T) {
	bin := t.TempDir()
	pidfile := filepath.Join(t.TempDir(), "tool.pid")
	write(t, filepath.Join(bin, "pre-commit"), "#!/bin/sh\nsleep 30 &\necho $! > \"$PIDFILE\"\nwait\n")
	if err := os.Chmod(filepath.Join(bin, "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PIDFILE", pidfile)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	preCommit(ctx, t.TempDir(), "slow")
	if elapsed := time.Since(started); elapsed >= waitDelay {
		t.Errorf("returned after %s: the tool held the wait open", elapsed)
	}

	body, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the hook's tool, pid %d, outlived the run", pid)
		}
	}
}

// An interrupted sweep left its clone in the cache, holding links into the
// checkout, with nothing to remove it later.
func TestAnInterruptedRunRemovesItsCloneAndSaysWhereItStopped(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: twoHooks})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dir string
	result := RunWith(ctx, repo, func(ctx context.Context, in, _ string) (int, string) {
		dir = in
		cancel()
		<-ctx.Done()
		return 1, ""
	})
	if want := "interrupted during fixer; 1 of 2 hooks never ran"; result.Outcome != Unknown || !strings.HasPrefix(result.Note, want) {
		t.Errorf("outcome %q, note %q; want unknown, opening %q", result.Outcome, result.Note, want)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the clone at %s outlived the interrupted run", dir)
	}
}

// expiresWhenTold reaches its deadline when expire is called, so a test can
// put the limit inside a hook rather than racing the clone made before it.
type expiresWhenTold struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func (c *expiresWhenTold) expire()               { c.once.Do(func() { close(c.done) }) }
func (c *expiresWhenTold) Done() <-chan struct{} { return c.done }

func (c *expiresWhenTold) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// Hooks skipped once the time ran out were reported as could-not-run, and
// nothing said the time had run out.
func TestARepoOutOfTimeSaysSoAndNamesTheHookItStoppedIn(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: twoHooks})
	parent := &expiresWhenTold{Context: context.Background(), done: make(chan struct{})}

	result := RunWith(parent, repo, func(ctx context.Context, _, _ string) (int, string) {
		parent.expire()
		<-ctx.Done()
		return 1, ""
	})
	if want := "hit the " + Timeout.String() + " limit during fixer; 1 of 2 hooks never ran"; !strings.HasPrefix(result.Note, want) {
		t.Errorf("note %q, want it to open %q", result.Note, want)
	}
}

// Offline with a cold hook cache, pre-commit cannot fetch a hook's environment
// and exits 3. Every hook read as failed, and the run exited 1 on code that may
// be clean.
func TestAHookPreCommitCouldNotSetUpIsUnknown(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks})
	const why = "An unexpected error has occurred: CalledProcessError: command: ('/usr/bin/git', 'fetch', 'origin', '--tags')"
	result := RunWith(context.Background(), repo, func(context.Context, string, string) (int, string) {
		return 3, why
	})
	if result.Outcome != Unknown || len(result.Hooks) != 1 || result.Hooks[0].Output != why {
		t.Errorf("outcome %q, hooks %+v; want unknown with pre-commit's reason kept", result.Outcome, result.Hooks)
	}
}

// forge run from inside a pre-commit hook inherits GIT_DIR and GIT_INDEX_FILE
// naming that hook's repository. The run must still lint the repo it was given.
func TestARunInsideAnotherRepositorysHookLintsItsOwnTarget(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks, "script.sh": "committed\n"})
	other := repoWith(t, map[string]string{"script.sh": "the other repository\n"})
	t.Setenv("GIT_DIR", filepath.Join(other.Path, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other.Path, ".git", "index"))

	var seen string
	result := RunWith(context.Background(), repo, func(_ context.Context, in, _ string) (int, string) {
		body, _ := os.ReadFile(filepath.Join(in, "script.sh"))
		seen = string(body)
		return 0, ""
	})
	if result.Outcome != Passed || seen != "committed\n" {
		t.Errorf("outcome %q (%s), the hook saw %q; want passed over the given repo's HEAD", result.Outcome, result.Note, seen)
	}
}

// A tool missing from this machine says nothing about the code, so it must not
// read as a failure. A real failure still outranks it.
func TestAMissingToolIsUnknownAndAFailureOutranksIt(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: twoHooks})
	outputs := map[string]struct {
		code   int
		output string
	}{
		"fixer":     {1, "Executable `shfmt` not found"},
		"typecheck": {1, "- hook id: typecheck\n- exit code: 1\n\nfound 2 errors"},
	}
	result := RunWith(context.Background(), repo, func(_ context.Context, _, hook string) (int, string) {
		return outputs[hook].code, outputs[hook].output
	})

	if result.Outcome != Failed {
		t.Errorf("outcome = %q, want failed", result.Outcome)
	}
	byID := map[string]Outcome{}
	for _, hook := range result.Hooks {
		byID[hook.ID] = hook.Outcome
	}
	if byID["fixer"] != Unknown || byID["typecheck"] != Failed {
		t.Errorf("hook outcomes = %v, want fixer unknown and typecheck failed", byID)
	}
}
