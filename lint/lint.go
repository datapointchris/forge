// Package lint runs a repo's standard pre-commit hooks over every file it
// tracks and says what each run turned out to be.
//
// The hooks are the ones the generated CI's hooks job runs, read from the
// repo's committed config, so a local run and CI run the same hooks. CI runs
// them over what a push changed, and this runs them over every file, so it can
// fail where CI passes. The question here is whether the repo is clean, and a
// commit only ever checked what it touched.
//
// Several hooks rewrite what they check, so they run in a throwaway clone of
// HEAD and never in the checkout. A checkout is where someone may be working,
// and a read across the portfolio must not edit it.
//
// Nothing here installs anything. A hook whose tool is absent reports Unknown
// rather than Failed: that is a statement about the machine, not the code.
package lint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/gitenv"
	"github.com/datapointchris/forge/precommit"
)

// Outcome is what linting one repo, or running one hook, turned out to be.
type Outcome string

const (
	Passed  Outcome = "passed"
	Failed  Outcome = "failed"
	NoHooks Outcome = "no_hooks"
	Unknown Outcome = "unknown"
)

// Hook is one hook's run. Output is kept for a hook that did not pass, whole,
// because the line explaining a failure is never the one a summary would keep.
type Hook struct {
	ID      string  `json:"id"`
	Outcome Outcome `json:"outcome"`
	Seconds float64 `json:"seconds"`
	Output  string  `json:"output,omitempty"`
}

// Result is one repo's run.
type Result struct {
	Repo    string  `json:"repo"`
	Outcome Outcome `json:"outcome"`
	Seconds float64 `json:"seconds"`
	Note    string  `json:"note,omitempty"`
	Hooks   []Hook  `json:"hooks,omitempty"`
}

// Timeout bounds one repo's hooks together. The slowest repo builds its Go
// linters cold inside this bound, so only a hook that has hung reaches it, and
// that hook reports `unknown`.
const Timeout = 10 * time.Minute

// waitDelay is how long a stopped command's pipes may stay open. A tool that
// outlives its group being killed is not waited on past it.
const waitDelay = 5 * time.Second

// HookRunner runs one hook by id in dir and returns its exit code and output.
type HookRunner func(ctx context.Context, dir, hook string) (code int, output string)

// Run lints one repo with pre-commit.
func Run(ctx context.Context, repo config.Repo) Result {
	if _, err := exec.LookPath("pre-commit"); err != nil {
		return Result{Repo: repo.Name, Outcome: Unknown, Note: "pre-commit is not installed here"}
	}
	return RunWith(ctx, repo, preCommit)
}

// RunRepos lints several repos concurrently, at most jobs at a time, and
// returns their results in the order given however they finished. jobs of zero
// means half the CPUs: each hook already spreads across cores, as a Go build or
// a type check does.
//
// A canceled ctx stops the running hooks and starts no more repos, so each
// clone is removed before the run returns. A repo never started reports
// unknown.
func RunRepos(ctx context.Context, repos []config.Repo, jobs int) []Result {
	if jobs < 1 {
		jobs = max(2, runtime.NumCPU()/2)
	}
	jobs = min(jobs, len(repos))

	results := make([]Result, len(repos))
	for i, repo := range repos {
		results[i] = Result{Repo: repo.Name, Outcome: Unknown, Note: "not started: interrupted"}
	}
	queue := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				results[i] = Run(ctx, repos[i])
			}
		}()
	}
dispatch:
	for i := range repos {
		select {
		case queue <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(queue)
	wg.Wait()
	return results
}

// RunWith is Run with the hook runner exposed, so a test can stand in for
// pre-commit and still exercise the clone.
func RunWith(ctx context.Context, repo config.Repo, run HookRunner) Result {
	started := time.Now()
	result := lintRepo(ctx, repo, run)
	result.Repo = repo.Name
	result.Seconds = time.Since(started).Seconds()
	return result
}

func lintRepo(parent context.Context, repo config.Repo, run HookRunner) Result {
	root, err := config.ExpandTilde(repo.Path)
	if err != nil {
		return Result{Outcome: Unknown, Note: "cannot resolve the repo path: " + err.Error()}
	}
	committed, found, err := committedConfig(root)
	if err != nil {
		return Result{Outcome: Unknown, Note: err.Error()}
	}
	if !found {
		return Result{Outcome: NoHooks, Note: "no " + precommit.ConfigPath + " at HEAD"}
	}
	hooks := ci.HooksToRun(committed)
	if len(hooks) == 0 {
		return Result{Outcome: NoHooks, Note: "the config holds no hook a standard block wrote"}
	}

	ctx, cancel := context.WithTimeout(parent, Timeout)
	defer cancel()
	tree, err := checkout(ctx, root, repo.Name)
	if err != nil {
		if stop := stopped(ctx); stop != "" {
			return Result{Outcome: Unknown, Note: stop + " preparing the clone"}
		}
		return Result{Outcome: Unknown, Note: err.Error()}
	}
	defer tree.remove()

	var result Result
	for _, id := range hooks {
		if ctx.Err() != nil {
			break
		}
		started := time.Now()
		code, output := run(ctx, tree.dir, id)
		hook := Hook{ID: id, Outcome: classify(ctx, code, output), Seconds: time.Since(started).Seconds()}
		if hook.Outcome != Passed {
			hook.Output = output
		}
		result.Hooks = append(result.Hooks, hook)
	}
	result.Outcome, result.Note = summarize(result.Hooks)
	if stop := stopped(ctx); stop != "" {
		note := stop + " before any hook ran"
		if ran := len(result.Hooks); ran > 0 {
			note = fmt.Sprintf("%s during %s; %d of %d hooks never ran", stop, result.Hooks[ran-1].ID, len(hooks)-ran, len(hooks))
		}
		result.Note = strings.Join(slices.DeleteFunc([]string{note, result.Note}, func(s string) bool { return s == "" }), "; ")
		if result.Outcome == Passed {
			result.Outcome = Unknown
		}
	}
	return result
}

// stopped says why ctx ended, or nothing while it runs.
func stopped(ctx context.Context) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "hit the " + Timeout.String() + " limit"
	case ctx.Err() != nil:
		return "interrupted"
	default:
		return ""
	}
}

// missingToolRE is how pre-commit reports a hook whose tool is not on PATH:
// its own message for a `language: system` entry, or the shell's exit 127 for
// an entry that runs one through `bash -c`.
var missingToolRE = regexp.MustCompile("Executable `[^`]+` not found|- exit code: 127\\b")

// preCommitSetupFailed is pre-commit's exit when it could not run at all, as
// when a hook's environment cannot be fetched offline. Its output says why.
const preCommitSetupFailed = 3

func classify(ctx context.Context, code int, output string) Outcome {
	switch {
	case ctx.Err() != nil:
		return Unknown
	case code == 0:
		return Passed
	case code == preCommitSetupFailed:
		return Unknown
	case missingToolRE.MatchString(output):
		return Unknown
	default:
		return Failed
	}
}

// summarize is a repo's outcome from its hooks'. A failure outranks a hook
// that could not run, because the failure is a finding in the code either way.
func summarize(hooks []Hook) (Outcome, string) {
	var failed, unknown []string
	for _, hook := range hooks {
		switch hook.Outcome {
		case Failed:
			failed = append(failed, hook.ID)
		case Unknown:
			unknown = append(unknown, hook.ID)
		}
	}
	var notes []string
	if len(failed) > 0 {
		notes = append(notes, "failed: "+strings.Join(failed, ", "))
	}
	if len(unknown) > 0 {
		notes = append(notes, "could not run: "+strings.Join(unknown, ", "))
	}
	note := strings.Join(notes, "; ")
	switch {
	case len(failed) > 0:
		return Failed, note
	case len(unknown) > 0:
		return Unknown, note
	default:
		return Passed, note
	}
}

// preCommit runs one hook over every tracked file.
//
// SKIP is dropped because a value left in the caller's shell would pass a
// hook by not running it. NO_COLOR keeps escape codes out of kept output.
func preCommit(ctx context.Context, dir, hook string) (int, string) {
	cmd := bounded(ctx, dir, "pre-commit", "run", hook, "--all-files")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	code := 1
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() > 0 {
		code = cmd.ProcessState.ExitCode()
	}
	return code, string(output)
}

// bounded is a command ctx stops whole. pre-commit and npm each run a tool as
// their child, and killing only them leaves the tool running in a clone about
// to be removed, holding the output pipe the wait is reading. So the command
// leads its own process group, and stopping it kills the group.
func bounded(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(hookEnv(os.Environ()), "NO_COLOR=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	return cmd
}

func hookEnv(env []string) []string {
	var kept []string
	for _, entry := range gitenv.WithoutRepoTarget(env) {
		if !strings.HasPrefix(entry, "SKIP=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// committedConfig is the pre-commit config at HEAD, and whether there is one.
// The working tree's copy is not read: the clone is HEAD, and an edit nobody
// committed is not what CI runs either.
func committedConfig(root string) (string, bool, error) {
	if _, err := git(root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return "", false, fmt.Errorf("no commit to lint: %w", err)
	}
	listed, err := git(root, "ls-tree", "--name-only", "HEAD", "--", precommit.ConfigPath)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(listed) == "" {
		return "", false, nil
	}
	body, err := git(root, "show", "HEAD:"+precommit.ConfigPath)
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}

// clone is a throwaway checkout of one repo's HEAD.
type clone struct{ dir string }

// checkout clones root's HEAD under forge's cache. --shared borrows root's
// objects instead of copying them, and writes nothing into root's own .git: a
// worktree would register itself there, and a run killed before it cleaned up
// would leave the entry behind.
func checkout(ctx context.Context, root, name string) (clone, error) {
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return clone{}, err
	}
	parent := filepath.Join(config.CacheHome(), "lint")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return clone{}, err
	}
	dir, err := os.MkdirTemp(parent, name+"-")
	if err != nil {
		return clone{}, err
	}
	made := clone{dir: dir}
	if _, err := git(parent, "clone", "--quiet", "--shared", "--no-checkout", root, dir); err != nil {
		made.remove()
		return clone{}, err
	}
	if _, err := git(dir, "checkout", "--quiet", "--detach", strings.TrimSpace(head)); err != nil {
		made.remove()
		return clone{}, err
	}
	if err := prepareNodePackages(ctx, root, dir); err != nil {
		made.remove()
		return clone{}, err
	}
	return made, nil
}

// remove deletes the clone. Each link in a mirrored node_modules goes as a
// link, and what it points at stays.
func (c clone) remove() {
	_ = os.RemoveAll(c.dir)
}

// prepareNodePackages gives each package in the clone the checkout's installed
// dependencies, then runs its postinstall script where it has one. The vue
// hooks run npm scripts whose tools resolve in node_modules, and an install per
// run would cost more than the lint. npm ci runs postinstall in CI, and Nuxt's
// generates the types the typecheck reads, so running it here makes those
// types HEAD's. A package with nothing installed is left bare, and its hooks
// report the tool missing.
//
// The packages are listed from the clone's index, which is HEAD. One staged in
// the checkout and not yet committed has no directory in the clone.
func prepareNodePackages(ctx context.Context, root, dir string) error {
	listed, err := git(dir, "ls-files", "-z", "--", ":(glob)**/package.json")
	if err != nil {
		return err
	}
	for _, rel := range strings.Split(listed, "\x00") {
		if rel == "" {
			continue
		}
		pkg := filepath.Dir(rel)
		installed := filepath.Join(root, pkg, "node_modules")
		if info, err := os.Stat(installed); err != nil || !info.IsDir() {
			continue
		}
		modules := mirror{root: root, clone: dir, installed: installed, node: filepath.Join(dir, pkg, "node_modules")}
		if err := modules.fill(modules.installed, modules.node, true); err != nil {
			return err
		}
		if err := postinstall(ctx, filepath.Join(dir, pkg)); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
	return nil
}

// mirror builds one package's node_modules in the clone from the checkout's.
// A link to the whole directory would send every cache a tool keeps there —
// vue-tsc's build info under .tmp, jiti's transpiled config under .cache —
// into the checkout. So node_modules is a directory of the clone's own, holding
// a link per installed entry, and the caches are created there instead.
type mirror struct {
	root, clone     string // the checkout and its clone
	installed, node string // the checkout's node_modules and the clone's
}

// fill mirrors from into to. At the top level, a dot-directory other than .bin
// is a cache and is left out. .bin and each @scope directory are rebuilt one
// level down, because they hold the links npm makes.
func (m mirror) fill(from, to string, top bool) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	if err := os.Mkdir(to, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		src, dst := filepath.Join(from, name), filepath.Join(to, name)
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			err = m.relink(src, dst)
		case top && isCache(name) && entry.IsDir():
			continue
		case top && (name == ".bin" || strings.HasPrefix(name, "@")) && entry.IsDir():
			err = m.fill(src, dst, false)
		default:
			err = os.Symlink(src, dst)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// relink recreates a link npm made, where linking through it would resolve in
// the checkout. A workspace package is one: node_modules/shared -> ../shared
// would lint the checkout's working tree, so a target in the repo's source
// points at the clone's copy, which is HEAD's. A target in the installed
// packages points at the clone's mirror of it, so a .bin entry reaches a
// workspace package the same way. Anything else keeps its target.
func (m mirror) relink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(src), target)
	}
	if rel, ok := within(m.installed, target); ok {
		if first, _, _ := strings.Cut(rel, string(filepath.Separator)); !isCache(first) {
			target = filepath.Join(m.node, rel)
		}
	} else if rel, ok := within(m.root, target); ok && !slices.Contains(strings.Split(rel, string(filepath.Separator)), "node_modules") {
		target = filepath.Join(m.clone, rel)
	}
	return os.Symlink(target, dst)
}

func isCache(name string) bool { return strings.HasPrefix(name, ".") && name != ".bin" }

// within is path relative to base, and whether it lies inside base.
func within(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// postinstall runs the package's postinstall script in the clone, if it has
// one.
func postinstall(ctx context.Context, pkg string) error {
	body, err := os.ReadFile(filepath.Join(pkg, "package.json"))
	if err != nil {
		return err
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("reading its scripts: %w", err)
	}
	if manifest.Scripts["postinstall"] == "" {
		return nil
	}
	if output, err := bounded(ctx, pkg, "npm", "run", "postinstall").CombinedOutput(); err != nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		return fmt.Errorf("npm run postinstall: %w: %s", err, lines[len(lines)-1])
	}
	return nil
}

// git runs git in dir with stdout returned and stderr folded into the error.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitenv.WithoutRepoTarget(os.Environ())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New("git " + strings.Join(args, " ") + ": " + detail)
	}
	return string(out), nil
}
