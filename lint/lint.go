// Package lint runs a repo's standard pre-commit hooks over every file it
// tracks and says what each run turned out to be.
//
// The hooks are the ones the generated CI's hooks job runs, read from the
// repo's committed config, so a local run and CI cannot disagree about what
// "the lint" means. They run over every file rather than a change, because the
// question is whether the repo is clean, and a commit only ever checked what it
// touched.
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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
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

// Timeout bounds one repo's hooks together. The slowest measured repo builds
// its Go linters cold, and a hook that has genuinely hung is worth waiting
// for rather than reporting as flaky.
const Timeout = 10 * time.Minute

// HookRunner runs one hook by id in dir and returns its exit code and output.
type HookRunner func(ctx context.Context, dir, hook string) (code int, output string)

// Run lints one repo with pre-commit.
func Run(repo config.Repo) Result {
	if _, err := exec.LookPath("pre-commit"); err != nil {
		return Result{Repo: repo.Name, Outcome: Unknown, Note: "pre-commit is not installed here"}
	}
	return RunWith(repo, preCommit)
}

// RunRepos lints several repos concurrently, at most jobs at a time, and
// returns their results in the order given however they finished. jobs of zero
// means half the CPUs: each hook already spreads across cores, as a Go build or
// a type check does.
func RunRepos(repos []config.Repo, jobs int) []Result {
	if jobs < 1 {
		jobs = max(2, runtime.NumCPU()/2)
	}
	jobs = min(jobs, len(repos))

	results := make([]Result, len(repos))
	queue := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				results[i] = Run(repos[i])
			}
		}()
	}
	for i := range repos {
		queue <- i
	}
	close(queue)
	wg.Wait()
	return results
}

// RunWith is Run with the hook runner exposed, so a test can stand in for
// pre-commit and still exercise the clone.
func RunWith(repo config.Repo, run HookRunner) Result {
	started := time.Now()
	result := lintRepo(repo, run)
	result.Repo = repo.Name
	result.Seconds = time.Since(started).Seconds()
	return result
}

func lintRepo(repo config.Repo, run HookRunner) Result {
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

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	tree, err := checkout(ctx, root, repo.Name)
	if err != nil {
		return Result{Outcome: Unknown, Note: err.Error()}
	}
	defer tree.remove()

	var result Result
	for _, id := range hooks {
		started := time.Now()
		code, output := run(ctx, tree.dir, id)
		hook := Hook{ID: id, Outcome: classify(ctx, code, output), Seconds: time.Since(started).Seconds()}
		if hook.Outcome != Passed {
			hook.Output = output
		}
		result.Hooks = append(result.Hooks, hook)
	}
	result.Outcome, result.Note = summarize(result.Hooks)
	return result
}

// missingToolRE is how pre-commit reports a hook whose tool is not on PATH:
// its own message for a `language: system` entry, or the shell's exit 127 for
// an entry that runs one through `bash -c`.
var missingToolRE = regexp.MustCompile("Executable `[^`]+` not found|- exit code: 127\\b")

func classify(ctx context.Context, code int, output string) Outcome {
	switch {
	case ctx.Err() != nil:
		return Unknown
	case code == 0:
		return Passed
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
	cmd := exec.CommandContext(ctx, "pre-commit", "run", hook, "--all-files")
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Env = append(hookEnv(os.Environ()), "NO_COLOR=1")
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

// remove deletes the clone. A linked node_modules goes as a link, and what it
// points at stays.
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
func prepareNodePackages(ctx context.Context, root, dir string) error {
	listed, err := git(root, "ls-files", "-z", "--", ":(glob)**/package.json")
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
		if err := linkInstalled(installed, filepath.Join(dir, pkg, "node_modules")); err != nil {
			return err
		}
		if err := postinstall(ctx, filepath.Join(dir, pkg)); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
	return nil
}

// linkInstalled makes node_modules a directory of the clone's own holding a
// link to each installed entry. A link to the whole directory would send every
// cache a tool keeps there — vue-tsc's build info under .tmp, jiti's
// transpiled config under .cache — into the checkout. Those live in top-level
// dot-directories, so each is left out and created in the clone instead. .bin
// is linked, because npm scripts find their tools there.
func linkInstalled(installed, node string) error {
	entries, err := os.ReadDir(installed)
	if err != nil {
		return err
	}
	if err := os.Mkdir(node, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && name != ".bin" && entry.IsDir() {
			continue
		}
		if err := os.Symlink(filepath.Join(installed, name), filepath.Join(node, name)); err != nil {
			return err
		}
	}
	return nil
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
	cmd := exec.CommandContext(ctx, "npm", "run", "postinstall")
	cmd.Dir = pkg
	cmd.Env = append(hookEnv(os.Environ()), "NO_COLOR=1")
	if output, err := cmd.CombinedOutput(); err != nil {
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
