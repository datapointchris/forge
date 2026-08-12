package suites

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datapointchris/forge/config"
)

// repo builds a registry entry pointing at a real directory, since every
// decision here is made by looking at what is on disk.
func repo(t *testing.T, components ...config.Component) config.Repo {
	t.Helper()
	return config.Repo{
		Name:      "demo",
		Path:      t.TempDir(),
		Toolchain: &config.Toolchain{Components: components},
	}
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// answering returns a fixed result, so a test asserts classification rather
// than the behavior of a real suite.
func answering(code int, output string) runner {
	return func(string, []string) (int, string) { return code, output }
}

func recording(argv *[]string) runner {
	return func(_ string, a []string) (int, string) { *argv = a; return 0, "" }
}

func never(t *testing.T) runner {
	t.Helper()
	return func(_ string, argv []string) (int, string) {
		t.Errorf("nothing should have been run, but got %v", argv)
		return 0, ""
	}
}

func only(t *testing.T, results []Result) Result {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d: %+v", len(results), results)
	}
	return results[0]
}

// The four outcomes, and why two would not do.

func TestPytestCollectingNothingIsNoSuiteNotAFailure(t *testing.T) {
	// A repo with no tests yet is not a failing repo, and ci/blocks/30-python.yml
	// makes the same call for the generated workflow.
	r := repo(t, config.Component{Stack: "python", Dir: "."})
	write(t, r.Path, "pyproject.toml", "[project]\nname='demo'\n")

	got := only(t, RunWith(r, answering(5, "no tests ran")))

	if got.Outcome != NoSuite {
		t.Errorf("pytest exit 5 is an empty suite, not a failure; got %q", got.Outcome)
	}
}

func TestAMissingRunnerIsUnknownNotAFailure(t *testing.T) {
	// It is a statement about the machine, not about the code. Reporting it as a
	// failure sends someone to debug a suite that was never run.
	r := repo(t, config.Component{Stack: "go", Dir: "."})
	write(t, r.Path, "go.mod", "module demo\n")

	got := only(t, RunWith(r, answering(127, "")))

	if got.Outcome != Unknown {
		t.Errorf("exit 127 means the runner is absent; got %q", got.Outcome)
	}
	if got.Note == "" {
		t.Error("an unknown outcome must say why, or it is indistinguishable from a bug here")
	}
}

func TestATimeoutIsUnknownNotAFailure(t *testing.T) {
	r := repo(t, config.Component{Stack: "go", Dir: "."})
	write(t, r.Path, "go.mod", "module demo\n")

	got := only(t, RunWith(r, answering(124, "")))

	if got.Outcome != Unknown {
		t.Errorf("a suite that never finished was not measured; got %q", got.Outcome)
	}
}

func TestARealNonZeroExitIsAFailure(t *testing.T) {
	r := repo(t, config.Component{Stack: "go", Dir: "."})
	write(t, r.Path, "go.mod", "module demo\n")

	got := only(t, RunWith(r, answering(1, "FAIL demo/pkg")))

	if got.Outcome != Failed {
		t.Errorf("exit 1 from a runner that ran is a failure; got %q", got.Outcome)
	}
	if got.Output != "FAIL demo/pkg" {
		t.Errorf("captured output must survive to explain the failure later; got %q", got.Output)
	}
}

func TestPassingIsPassing(t *testing.T) {
	r := repo(t, config.Component{Stack: "go", Dir: "."})
	write(t, r.Path, "go.mod", "module demo\n")

	got := only(t, RunWith(r, answering(0, "ok demo 0.1s")))

	if got.Outcome != Passed || !got.OK() {
		t.Errorf("expected a passing result, got %q", got.Outcome)
	}
}

// What is decided before anything runs.

func TestMissingNodeModulesIsUnknownWithoutRunningAnything(t *testing.T) {
	// Decided by looking rather than by reading exit 127, because 127 is also
	// what a genuinely broken test command produces. Three components on the
	// portfolio were in this state when the check was written.
	r := repo(t, config.Component{Stack: "vue", Dir: "."})
	write(t, r.Path, "package.json", `{"scripts":{"test":"vitest run"}}`)

	got := only(t, RunWith(r, never(t)))

	if got.Outcome != Unknown {
		t.Errorf("absent dependencies are unmeasured, not failed; got %q", got.Outcome)
	}
}

func TestNoTestScriptIsNoSuite(t *testing.T) {
	r := repo(t, config.Component{Stack: "vue", Dir: "."})
	write(t, r.Path, "package.json", `{"scripts":{"build":"vite build"}}`)

	got := only(t, RunWith(r, never(t)))

	if got.Outcome != NoSuite {
		t.Errorf("a frontend with no test script has no suite; got %q", got.Outcome)
	}
}

func TestNoGoModIsNoSuite(t *testing.T) {
	r := repo(t, config.Component{Stack: "go", Dir: "."})

	got := only(t, RunWith(r, never(t)))

	if got.Outcome != NoSuite {
		t.Errorf("no module means nothing to test; got %q", got.Outcome)
	}
}

func TestADeclaredDirectoryThatIsGoneIsUnknown(t *testing.T) {
	// The registry is hand-edited, so it can name a directory that has moved.
	// That is a fact about the declaration, and it must not read as a pass.
	r := repo(t, config.Component{Stack: "go", Dir: "api"})

	got := only(t, RunWith(r, never(t)))

	if got.Outcome != Unknown {
		t.Errorf("a directory that does not exist was not measured; got %q", got.Outcome)
	}
}

// Which components are asked at all.

func TestStacksWithNoSuitesAreSkippedEntirely(t *testing.T) {
	// Not reported as `no_suite`: a row that says the same thing on every repo
	// forever is noise, and actions/docker/terraform are linted, not tested.
	r := repo(t,
		config.Component{Stack: "actions", Dir: "."},
		config.Component{Stack: "docker", Dir: "."},
		config.Component{Stack: "terraform", Dir: "."},
		config.Component{Stack: "shell", Dir: "."},
	)

	if results := RunWith(r, never(t)); len(results) != 0 {
		t.Errorf("untested stacks produce no rows, got %+v", results)
	}
}

func TestEveryTestableComponentGetsItsOwnRow(t *testing.T) {
	// nomad, meso and learning each hold two Go modules deliberately isolated
	// from each other, so one row per repo would hide half the fleet's tests.
	r := repo(t,
		config.Component{Stack: "go", Dir: "api"},
		config.Component{Stack: "go", Dir: "cli"},
		config.Component{Stack: "actions", Dir: "."},
	)
	for _, dir := range []string{"api", "cli"} {
		write(t, r.Path, filepath.Join(dir, "go.mod"), "module demo\n")
	}

	results := RunWith(r, answering(0, ""))

	if len(results) != 2 {
		t.Fatalf("expected a row per Go component, got %d: %+v", len(results), results)
	}
	if results[0].Dir != "api" || results[1].Dir != "cli" {
		t.Errorf("rows should keep declaration order, got %q then %q", results[0].Dir, results[1].Dir)
	}
}

// What actually gets run.

func TestGoRunsTheCommandCIRuns(t *testing.T) {
	r := repo(t, config.Component{Stack: "go", Dir: "."})
	write(t, r.Path, "go.mod", "module demo\n")

	var argv []string
	RunWith(r, recording(&argv))

	want := []string{"go", "test", "./..."}
	if len(argv) != len(want) {
		t.Fatalf("expected %v, got %v", want, argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, argv)
		}
	}
}

func TestPythonSuppliesPytestRatherThanAssumingIt(t *testing.T) {
	// ci/blocks/30-python.yml's reason: a repo with a real suite that never
	// declared pytest as a dependency would otherwise silently stop being tested.
	r := repo(t, config.Component{Stack: "python", Dir: "."})
	write(t, r.Path, "pyproject.toml", "[project]\nname='demo'\n")

	var argv []string
	RunWith(r, recording(&argv))

	var withPytest bool
	for i, arg := range argv {
		if arg == "--with" && i+1 < len(argv) && argv[i+1] == "pytest" {
			withPytest = true
		}
	}
	if !withPytest {
		t.Errorf("pytest must be supplied, not assumed; got %v", argv)
	}
}

func TestTheUnitScriptWinsOverTheOneThatNeedsABrowser(t *testing.T) {
	// `test` is where a frontend hangs its e2e run, which wants a browser and a
	// server. A fleet-wide read must not be what starts those.
	r := repo(t, config.Component{Stack: "vue", Dir: "."})
	write(t, r.Path, "package.json", `{"scripts":{"test":"playwright test","test:unit":"vitest run"}}`)
	write(t, r.Path, "node_modules/.keep", "")

	var argv []string
	RunWith(r, recording(&argv))

	if len(argv) < 3 || argv[2] != "test:unit" {
		t.Errorf("expected the unit script to be chosen, got %v", argv)
	}
}

func TestARepoDeclaringNoComponentsProducesNothing(t *testing.T) {
	if results := Run(config.Repo{Name: "demo", Path: t.TempDir()}); results != nil {
		t.Errorf("a repo with no toolchain has nothing to run, got %+v", results)
	}
}

// Running several repos at once.

func TestResultsKeepRegistryOrderHoweverTheyFinish(t *testing.T) {
	// A report that reorders itself between runs is a diff full of changes
	// nobody made, which is the whole point of storing runs to compare.
	var repos []config.Repo
	for _, name := range []string{"alpha", "beta", "gamma", "delta"} {
		r := repo(t, config.Component{Stack: "go", Dir: "."})
		r.Name = name
		write(t, r.Path, "go.mod", "module "+name+"\n")
		repos = append(repos, r)
	}

	results := RunRepos(repos, 4)

	if len(results) != 4 {
		t.Fatalf("expected a row per repo, got %d", len(results))
	}
	for i, want := range []string{"alpha", "beta", "gamma", "delta"} {
		if results[i].Repo != want {
			t.Errorf("position %d should be %q, got %q", i, want, results[i].Repo)
		}
	}
}

func TestEveryRepoRunsExactlyOnce(t *testing.T) {
	// A worker pool that drops or repeats an item is silent about it: the count
	// is the only thing that notices.
	var repos []config.Repo
	for i := range 30 {
		r := repo(t, config.Component{Stack: "go", Dir: "."})
		r.Name = fmt.Sprintf("repo%02d", i)
		write(t, r.Path, "go.mod", "module demo\n")
		repos = append(repos, r)
	}

	seen := map[string]int{}
	for _, result := range RunRepos(repos, 8) {
		seen[result.Repo]++
	}

	if len(seen) != 30 {
		t.Errorf("expected 30 repos to report, got %d", len(seen))
	}
	for name, count := range seen {
		if count != 1 {
			t.Errorf("%s ran %d times", name, count)
		}
	}
}

func TestMoreWorkersThanReposIsNotAnError(t *testing.T) {
	r := repo(t, config.Component{Stack: "go", Dir: "."})
	write(t, r.Path, "go.mod", "module demo\n")

	if results := RunRepos([]config.Repo{r}, 64); len(results) != 1 {
		t.Errorf("expected one result, got %d", len(results))
	}
}

func TestNoReposIsNotAHang(t *testing.T) {
	// The pool sizes itself from the work, so an empty list must not leave a
	// worker waiting on a channel nobody will write to.
	done := make(chan []Result, 1)
	go func() { done <- RunRepos(nil, 0) }()
	select {
	case results := <-done:
		if len(results) != 0 {
			t.Errorf("expected nothing, got %+v", results)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunRepos hung on an empty list")
	}
}
