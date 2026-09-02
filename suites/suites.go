// Package suites runs a repo's declared components' test suites and says what
// each one turned out to be.
//
// The commands are the ones ci/blocks/ already generates into every repo's
// workflow, so a local run and CI cannot disagree about what "the tests" means.
// Where a stack's CI block runs no tests — vue builds and lints and stops — the
// command is the one the component's own package.json declares.
//
// Nothing here installs anything. A component whose runner is absent reports
// Unknown rather than Failed: that is a statement about the machine, not about
// the code, and a runner that repaired it would be doing two jobs and mutating
// the box a read was asked for.
package suites

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/datapointchris/forge/config"
)

// Outcome is what running one component turned out to be.
//
// Four rather than two. `no_suite` and `unknown` are the categories a pass/fail
// runner has to lie about: a repo with no tests yet is not failing, and a
// component whose runner is not installed has not been measured at all.
// Measured 2026-08-12 across the portfolio: 35 passed, 2 failed, 3 could not
// run, 4 had no suite — so a third of the non-passing rows would have been
// misreported as failures.
type Outcome string

const (
	Passed  Outcome = "passed"
	Failed  Outcome = "failed"
	NoSuite Outcome = "no_suite"
	Unknown Outcome = "unknown"
)

// Result is one component's run, with everything needed to explain it later.
//
// Output is kept whole rather than summarized. A run is read long after it
// happened, usually because something changed, and the line that explains a
// failure is never the one a summarizer would have kept.
type Result struct {
	Repo     string   `json:"repo"`
	Stack    string   `json:"stack"`
	Dir      string   `json:"dir"`
	Command  []string `json:"command,omitempty"`
	Outcome  Outcome  `json:"outcome"`
	ExitCode int      `json:"exit_code"`
	Seconds  float64  `json:"seconds"`
	Note     string   `json:"note,omitempty"`
	Output   string   `json:"output,omitempty"`
}

// OK reports whether this result should hold up a caller.
func (r Result) OK() bool { return r.Outcome == Passed || r.Outcome == NoSuite }

// Timeout bounds one component. Generous: the slowest measured component is
// ichrisbirch's python at 46s, and a suite that has genuinely hung is worth
// waiting a few minutes to be sure about rather than reporting as flaky.
const Timeout = 5 * time.Minute

// tested are the stacks that carry a suite. The rest — actions, docker,
// terraform, shell — are linted rather than tested, and asking them for a test
// result would produce a row that is `no_suite` forever and says nothing.
var tested = map[string]bool{"go": true, "python": true, "vue": true, "node": true}

// Tested reports whether a stack has suites worth running.
func Tested(stack string) bool { return tested[stack] }

// Run executes every testable component of one repo, in declaration order.
func Run(repo config.Repo) []Result {
	return RunWith(repo, execute)
}

// RunRepos executes several repos concurrently, at most jobs at a time, and
// returns their results in registry order however they finished.
//
// The concurrency is **between** repos and never inside one. Components of a
// single repo share more than a directory: a repo holding an api and a cli
// runs both against the same declared database and the same ports, so running
// them together would turn one repo's fixtures into another's flake.
// Measured, that costs almost nothing — nearly every repo with a suite has
// exactly one testable component, so the floor is set by the few holding two
// and it stays well under the sequential time.
//
// jobs of zero means half the CPUs, never one per CPU. Measured on 16 cores
// against the whole portfolio:
//
//	jobs=1    195.2s elapsed, 195.2s of suite time
//	jobs=4     64.9s elapsed, 219.1s
//	jobs=8     59.4s elapsed, 223.0s
//	jobs=16    60.2s elapsed, 264.8s
//
// Everything is won by four. Sixteen is slower in wall clock than eight while
// spending 19% more suite time, which is workers competing for cores rather than
// finding new ones — `go test` already parallelizes within a module and a pytest
// suite is not idle either. Half leaves room for that.
//
// The floor is not the worker count: at eight, the run is as long as
// ichrisbirch's own suite. Going below a minute means splitting that, not adding
// workers.
func RunRepos(repos []config.Repo, jobs int) []Result {
	if jobs < 1 {
		jobs = max(2, runtime.NumCPU()/2)
	}
	if jobs > len(repos) {
		jobs = len(repos)
	}
	if jobs < 1 {
		return nil
	}

	// Indexed rather than appended, so the output does not depend on which repo
	// happened to finish first. A report that reorders itself between runs is a
	// diff full of changes nobody made.
	perRepo := make([][]Result, len(repos))
	queue := make(chan int)
	var wg sync.WaitGroup

	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				perRepo[i] = Run(repos[i])
			}
		}()
	}
	for i := range repos {
		queue <- i
	}
	close(queue)
	wg.Wait()

	var out []Result
	for _, results := range perRepo {
		out = append(out, results...)
	}
	return out
}

type runner func(dir string, argv []string) (code int, output string)

// RunWith is Run with the execution seam exposed, so a test can assert what
// would be run without running it.
func RunWith(repo config.Repo, run runner) []Result {
	if repo.Toolchain == nil {
		return nil
	}
	root, err := config.ExpandTilde(repo.Path)
	if err != nil {
		return []Result{{Repo: repo.Name, Outcome: Unknown, Note: "cannot resolve repo path: " + err.Error()}}
	}

	var results []Result
	for _, component := range repo.Toolchain.Components {
		if !tested[component.Stack] {
			continue
		}
		results = append(results, runComponent(repo.Name, root, component, run))
	}
	return results
}

func runComponent(name, root string, component config.Component, run runner) Result {
	result := Result{Repo: name, Stack: component.Stack, Dir: component.Dir}
	dir := root
	if component.Dir != "" && component.Dir != "." {
		dir = filepath.Join(root, component.Dir)
	}

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		result.Outcome = Unknown
		result.Note = "declared directory does not exist"
		return result
	}

	argv, outcome, note := plan(component.Stack, dir)
	if argv == nil {
		result.Outcome, result.Note = outcome, note
		return result
	}
	result.Command = argv

	start := time.Now()
	code, output := run(dir, argv)
	result.Seconds = time.Since(start).Round(time.Millisecond).Seconds()
	result.ExitCode = code
	result.Output = output
	result.Outcome, result.Note = classify(component.Stack, dir, code, output)
	return result
}

// plan is what to run, or why there is nothing to run. Answering before
// executing is what separates "this repo has no tests" from "this repo's tests
// failed", which an exit code alone cannot distinguish for every stack.
func plan(stack, dir string) (argv []string, outcome Outcome, note string) {
	switch stack {
	case "go":
		if !exists(filepath.Join(dir, "go.mod")) {
			return nil, NoSuite, "no go.mod"
		}
		return []string{"go", "test", "./..."}, "", ""

	case "python":
		if !exists(filepath.Join(dir, "pyproject.toml")) {
			return nil, NoSuite, "no pyproject.toml"
		}
		// `--with pytest` for the reason ci/blocks/30-python.yml records: a repo
		// with a real suite that never declared pytest as a dependency would
		// otherwise silently stop being tested.
		return []string{"uv", "run", "--with", "pytest", "pytest", "-q", "-p", "no:cacheprovider"}, "", ""

	case "vue", "node":
		script, ok := testScript(filepath.Join(dir, "package.json"))
		if !ok {
			return nil, NoSuite, "no test script in package.json"
		}
		// node_modules checked here rather than inferred from exit 127, because
		// 127 is also what a genuinely broken test command produces. Measured:
		// three components on this machine have no node_modules, and reporting
		// them as failures says the code is broken when the machine is not set
		// up. Installing is dotfiles' job, not a test runner's.
		if !exists(filepath.Join(dir, "node_modules")) {
			return nil, Unknown, "no node_modules — dependencies are not installed here"
		}
		return []string{"npm", "run", script, "--silent"}, "", ""
	}
	return nil, NoSuite, "stack carries no suite"
}

func classify(stack, dir string, code int, output string) (Outcome, string) {
	if code == 0 {
		return Passed, ""
	}
	switch {
	case code == 124:
		return Unknown, "timed out after " + Timeout.String()
	case stack == "python" && code == 5:
		// pytest's "collected nothing", which ci/blocks/30-python.yml also
		// treats as success. A repo with no tests yet is not a failing repo.
		return NoSuite, "pytest collected no tests"
	case code == 127:
		return Unknown, "the test runner is not installed here"
	case strings.Contains(output, "executable file not found"):
		return Unknown, "the test runner is not installed here"
	}
	if module, ok := unresolvedDeclaredImport(dir, output); ok {
		return Unknown, "declares " + module + " and does not have it installed — the dependencies are behind the manifest"
	}
	return Failed, ""
}

// unresolvedImport catches the bundler saying a module is not there. Vite's
// wording; vitest inherits it.
var unresolvedImport = regexp.MustCompile(`Failed to resolve import "([^"]+)"`)

// unresolvedDeclaredImport reports whether the run failed on an import the
// manifest *declares*, which is a stale install rather than broken code.
//
// The distinction is the whole point and cannot be made from the error alone: an
// unresolvable import that package.json declares means node_modules is behind,
// while one it does not declare is a real bug and must stay a failure. Checking
// only for a missing node_modules misses this entirely — a web component can
// have one installed before a dependency was added, and then report a failing
// suite for something no change to its code could fix.
//
// A relative path is never a dependency, so it drops out before the lookup.
func unresolvedDeclaredImport(dir, output string) (string, bool) {
	match := unresolvedImport.FindStringSubmatch(output)
	if match == nil {
		return "", false
	}
	module := match[1]
	if strings.HasPrefix(module, ".") || strings.HasPrefix(module, "/") || strings.HasPrefix(module, "@/") {
		return "", false
	}
	// A subpath import — `marked/lib` — is satisfied by the package itself.
	if name, _, found := strings.Cut(strings.TrimPrefix(module, "@"), "/"); found && !strings.HasPrefix(module, "@") {
		module = name
	}
	if declares(filepath.Join(dir, "package.json"), module) {
		return module, true
	}
	return "", false
}

func declares(manifest, module string) bool {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	_, runtime := pkg.Dependencies[module]
	_, dev := pkg.DevDependencies[module]
	return runtime || dev
}

// testScript prefers the unit script where a repo has both: `test` is where a
// frontend hangs its e2e run, which needs a browser and a server and is not
// what a fleet-wide read should be starting.
func testScript(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return "", false
	}
	for _, name := range []string{"test:unit", "test"} {
		if pkg.Scripts[name] != "" {
			return name, true
		}
	}
	return "", false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func execute(dir string, argv []string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = nil // a suite that reads stdin must not consume the caller's
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1", "CI=1")
	output, err := cmd.CombinedOutput()

	code := 0
	if ctx.Err() != nil {
		code = 124
	} else if err != nil {
		code = cmd.ProcessState.ExitCode()
		if code < 0 {
			code = 1
		}
	}
	return code, string(output)
}
