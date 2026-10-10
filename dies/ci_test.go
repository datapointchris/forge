package dies

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

// privateFixture is a repo the registry declares private, which is the only
// thing that puts a generated workflow on the self-hosted runner.
func privateFixture(t *testing.T, declared *config.Toolchain, files map[string]string) reconcile.Target {
	t.Helper()
	target := fixture(t, declared, files)
	target.Repo.Visibility = "private"
	return target
}

func changedItems(changes []reconcile.Change) []string {
	items := make([]string, 0, len(changes))
	for _, change := range changes {
		items = append(items, change.Item)
	}
	return items
}

func TestAPrivateRepoGetsTheLintConfigBesideTheWorkflow(t *testing.T) {
	target := privateFixture(t, stacks("go"), nil)

	applyAll(t, target, CI{})

	workflow, err := os.ReadFile(target.Path(ci.WorkflowPath))
	if err != nil {
		t.Fatalf("read the workflow: %s", err)
	}
	if !strings.Contains(string(workflow), "runs-on: "+string(ci.SelfHosted)) {
		t.Errorf("a private repo's workflow does not name the pool:\n%s", workflow)
	}

	config, err := os.ReadFile(target.Path(ci.ActionlintConfigPath))
	if err != nil {
		t.Fatalf("read the lint config: %s", err)
	}
	if !strings.Contains(string(config), ci.RunnerLabel) {
		t.Errorf("the lint config does not declare the pool:\n%s", config)
	}
}

// A public repo's lint config declares the pinned image and never the pool.
// Declaring the pool there would retire the one check that catches a
// hand-written workflow in a repo a fork can open a pull request against.
//
// This is the assertion that Observe hands ActionlintConfig the repo's runner.
// Passing SelfHosted regardless writes a config every other test accepts.
func TestAPublicRepoDeclaresItsImageToActionlintAndNeverThePool(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, CI{})

	workflow, err := os.ReadFile(target.Path(ci.WorkflowPath))
	if err != nil {
		t.Fatalf("read the workflow: %s", err)
	}
	if strings.Contains(string(workflow), ci.RunnerLabel) {
		t.Errorf("a public repo's workflow names the pool:\n%s", workflow)
	}

	config, err := os.ReadFile(target.Path(ci.ActionlintConfigPath))
	if err != nil {
		t.Fatalf("read the lint config: %s", err)
	}
	if strings.Contains(string(config), ci.RunnerLabel) {
		t.Errorf("a public repo's lint config declares the pool:\n%s", config)
	}
	if image := testAssets(t).Manifest.HostedRunner; !strings.Contains(string(config), image) {
		t.Errorf("the lint config does not declare %s:\n%s", image, config)
	}
}

// lintWorkflow builds a throwaway repo, and nothing a real repo carries on disk
// reaches it. So a workflow naming a label actionlint cannot discover needs
// its declaration written beside it there, or actionlint reports an unknown
// label and the finding refuses `forge repos plan ci` for every repo at once.
// The pool is never on actionlint's list, and an image newer than the pinned
// actionlint is not on it either.
func TestTheDiesOwnLintRejectsAnUndiscoveredLabelWithoutTheConfigAndAcceptsItWithOne(t *testing.T) {
	requireActionlint(t)

	assets := testAssets(t)
	for runner, label := range map[ci.Runner]string{
		ci.SelfHosted:              ci.RunnerLabel,
		ci.Hosted(assets.Manifest): assets.Manifest.HostedRunner,
	} {
		workflow, err := ci.Generate(assets.CI, assets.Manifest,
			[]config.Component{{Stack: "go", Dir: "."}}, "", nil, ci.Ungated, runner)
		if err != nil {
			t.Fatalf("Generate: %s", err)
		}

		bare := lintWorkflow(workflow, "")
		if !strings.Contains(bare, label) {
			t.Fatalf("actionlint accepted an undeclared %s, so this test measures nothing: %q", label, bare)
		}

		if finding := lintWorkflow(workflow, ci.ActionlintConfig(assets.Manifest, runner)); finding != "" {
			t.Errorf("%s: the generated workflow does not lint against its own config: %s", runner, finding)
		}
	}
}

// requireActionlint skips only where actionlint genuinely cannot be run, and
// fails where it is expected.
//
// The plain skip made this file green on every CI run, because nothing installs
// actionlint into the generated go job. FORGE_REQUIRE_ACTIONLINT is set there,
// so an absent binary is a broken gate rather than a machine without a tool.
func requireActionlint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("actionlint"); err == nil {
		return
	}
	if os.Getenv("FORGE_REQUIRE_ACTIONLINT") != "" {
		t.Fatal("actionlint is absent where the gate declares it present — this assertion would pass vacuously")
	}
	t.Skip("actionlint is not installed, so runValidator returns no finding either way")
}

// Several repos hand-wrote a workflow before generation existed. Overwriting
// one would destroy work with no way back.
func TestAHandWrittenWorkflowIsRefusedRatherThanOverwritten(t *testing.T) {
	handWritten := "name: CI\njobs: {}\n"
	target := fixture(t, stacks("go"), map[string]string{ci.WorkflowPath: handWritten})

	applyAll(t, target, CI{})

	data, err := os.ReadFile(target.Path(ci.WorkflowPath))
	if err != nil {
		t.Fatalf("read back: %s", err)
	}
	if string(data) != handWritten {
		t.Errorf("a hand-written workflow was overwritten:\n%s", data)
	}
}

// A node component takes the vue setup, so it reads the same file.
func TestANodeRepoWithoutANodeVersionFileIsRefusedRatherThanGivenAJobThatCannotStart(t *testing.T) {
	for _, stack := range []string{"vue", "node"} {
		t.Run(stack, func(t *testing.T) {
			target := fixture(t, stacks(stack), nil)

			changes := reconcile.Assess(target, CI{}).Changes
			if !slices.ContainsFunc(changes, func(c reconcile.Change) bool {
				return c.Item == ci.WorkflowPath && c.Repair == reconcile.ByHand && strings.Contains(c.Detail, ci.NodeVersionFile)
			}) {
				t.Fatalf("no by-hand finding names the missing %s: %+v", ci.NodeVersionFile, changes)
			}
			applyAll(t, target, CI{})
			if _, err := os.Stat(target.Path(ci.WorkflowPath)); !os.IsNotExist(err) {
				t.Errorf("a workflow whose setup cannot start was written")
			}

			if err := os.WriteFile(target.Path(ci.NodeVersionFile), []byte("24\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			applyAll(t, target, CI{})
			if _, err := os.Stat(target.Path(ci.WorkflowPath)); err != nil {
				t.Errorf("the workflow was not written once %s exists: %v", ci.NodeVersionFile, err)
			}
		})
	}
}

// The shell job is gone, so a section set up for its suites has nowhere to go.
// Regenerating anyway would delete the repo's own steps.
func TestACustomSectionWhoseJobIsGoneHoldsTheWorkflowBack(t *testing.T) {
	existing := "# forge-toolchain: 1\nname: CI\n\njobs:\n\n  shell:\n    runs-on: ubuntu-latest\n    steps:\n\n" +
		"      - uses: actions/checkout@v1\n\n" +
		"      # > custom:before:shell - yq, which the suites read frontmatter with\n" +
		"      - run: echo installing yq\n\n" +
		"      # generated:shell\n      - run: shellcheck bin/*\n"
	committed := "# forge-toolchain: 1\nrepos:\n  # generated:shell\n" +
		"  - repo: https://github.com/koalaman/shellcheck-precommit\n    rev: v0.10.0\n    hooks:\n      - id: shellcheck\n"
	target := fixture(t, stacks("shell"), map[string]string{ci.WorkflowPath: existing, ".pre-commit-config.yaml": committed})

	changes := reconcile.Assess(target, CI{}).Changes
	if !slices.ContainsFunc(changes, func(c reconcile.Change) bool {
		return c.Item == ci.WorkflowPath && c.Repair == reconcile.ByHand && strings.Contains(c.Detail, "before:shell")
	}) {
		t.Fatalf("no by-hand finding names the orphaned section: %+v", changes)
	}
	applyAll(t, target, CI{})
	if got := readFile(t, target.Path(ci.WorkflowPath)); got != existing {
		t.Errorf("the workflow was rewritten, dropping the section:\n%s", got)
	}
}

// The two files are gated on their own blockers rather than on one shared
// verdict. A hand-written lint config is no reason to stop regenerating the
// workflow, and a repo can be in exactly that state.
func TestAHandWrittenLintConfigStillLetsTheWorkflowRegenerate(t *testing.T) {
	handWritten := "self-hosted-runner:\n  labels:\n    - " + ci.RunnerLabel + "\n"
	target := privateFixture(t, stacks("go"), map[string]string{ci.ActionlintConfigPath: handWritten})

	applyAll(t, target, CI{})

	if _, err := os.Stat(target.Path(ci.WorkflowPath)); err != nil {
		t.Errorf("the workflow was not written: %s", err)
	}
	data, err := os.ReadFile(target.Path(ci.ActionlintConfigPath))
	if err != nil {
		t.Fatalf("read back: %s", err)
	}
	if string(data) != handWritten {
		t.Errorf("a hand-written lint config was overwritten:\n%s", data)
	}
}

// A second apply over a converged repo must find nothing to do. An unstable
// generator writes a file every run, which shows up as drift in every repo on
// every sweep and trains the reader to ignore it.
func TestASecondApplyFindsNothingToDo(t *testing.T) {
	target := privateFixture(t, stacks("go"), nil)

	applyAll(t, target, CI{})

	measured := reconcile.Assess(target, CI{})
	if len(measured.Changes) != 0 {
		t.Fatalf("changes = %v, want none — generation is not stable", changedItems(measured.Changes))
	}
}

// Perform is handed one change at a time and re-observes before writing. It has
// to write the file that change names, not whichever the die holds first.
func TestPerformWritesTheFileTheChangeNames(t *testing.T) {
	target := privateFixture(t, stacks("go"), nil)

	measured := reconcile.Assess(target, CI{})
	var lintChange reconcile.Change
	for _, change := range measured.Changes {
		if change.Item == ci.ActionlintConfigPath {
			lintChange = change
		}
	}
	if lintChange.Item == "" {
		t.Fatalf("no change for %s: %v", ci.ActionlintConfigPath, changedItems(measured.Changes))
	}

	if _, err := (CI{}).Perform(target, lintChange); err != nil {
		t.Fatalf("Perform: %s", err)
	}

	if _, err := os.Stat(target.Path(ci.ActionlintConfigPath)); err != nil {
		t.Errorf("the named file was not written: %s", err)
	}
	if _, err := os.Stat(target.Path(ci.WorkflowPath)); !os.IsNotExist(err) {
		t.Error("performing the lint-config change also wrote the workflow")
	}
}

// A repo that turns public keeps whatever forge wrote while it was private.
// A self-hosted declaration left on disk there is the drift, and check, plan
// and apply all have to see it.
func TestARepoThatTurnsPublicHasThePoolTakenOutOfItsLintConfig(t *testing.T) {
	target := privateFixture(t, stacks("go"), nil)
	applyAll(t, target, CI{})

	if _, err := os.Stat(target.Path(ci.ActionlintConfigPath)); err != nil {
		t.Fatalf("the private fixture never got a lint config: %s", err)
	}

	target.Repo.Visibility = "public"

	if measured := reconcile.Assess(target, CI{}); len(measured.Changes) == 0 {
		t.Fatal("a public repo still carrying a self-hosted declaration reported converged")
	}

	applyAll(t, target, CI{})

	config, err := os.ReadFile(target.Path(ci.ActionlintConfigPath))
	if err != nil {
		t.Fatalf("read the lint config: %s", err)
	}
	if strings.Contains(string(config), ci.RunnerLabel) {
		t.Errorf("the pool survived the repo going public:\n%s", config)
	}
	if again := reconcile.Assess(target, CI{}); len(again.Changes) != 0 {
		t.Errorf("changes after the rewrite = %v, want none", changedItems(again.Changes))
	}
}

// forge rewrites only what it wrote. A lint config with no stamp is the repo's
// own file, whatever runner that repo now takes.
func TestAHandWrittenLintConfigIsLeftAloneInAPublicRepo(t *testing.T) {
	handWritten := "self-hosted-runner:\n  labels:\n    - " + ci.RunnerLabel + "\n"
	target := fixture(t, stacks("go"), map[string]string{ci.ActionlintConfigPath: handWritten})

	applyAll(t, target, CI{})

	data, err := os.ReadFile(target.Path(ci.ActionlintConfigPath))
	if err != nil {
		t.Fatalf("read back: %s", err)
	}
	if string(data) != handWritten {
		t.Errorf("a hand-written lint config was removed or rewritten:\n%s", data)
	}
}

// actionlint reads both spellings and prefers the one forge writes, so writing
// it beside a hand-written .yml overrides that file rather than being refused
// by it. The refusal is this die's central promise.
func TestAHandWrittenLintConfigAtTheOtherSpellingIsRefused(t *testing.T) {
	handWritten := "self-hosted-runner:\n  labels:\n    - other-pool\n"
	target := privateFixture(t, stacks("go"), map[string]string{ci.ActionlintConfigAltPath: handWritten})

	applyAll(t, target, CI{})

	if _, err := os.Stat(target.Path(ci.ActionlintConfigPath)); !os.IsNotExist(err) {
		t.Errorf("forge wrote %s over a hand-written %s", ci.ActionlintConfigPath, ci.ActionlintConfigAltPath)
	}
	data, err := os.ReadFile(target.Path(ci.ActionlintConfigAltPath))
	if err != nil {
		t.Fatalf("read back: %s", err)
	}
	if string(data) != handWritten {
		t.Errorf("the hand-written config at the other spelling changed:\n%s", data)
	}
}

// A run interrupted between the two writes has to leave a repo that still
// builds. A declaration for a label nothing uses is inert; a workflow naming a
// label nothing declares fails that repo's actionlint hook on every commit.
func TestTheLintDeclarationIsWrittenBeforeTheWorkflowThatNamesIt(t *testing.T) {
	target := privateFixture(t, stacks("go"), nil)

	items := changedItems(reconcile.Assess(target, CI{}).Changes)
	lint, workflow := -1, -1
	for i, item := range items {
		switch item {
		case ci.ActionlintConfigPath:
			lint = i
		case ci.WorkflowPath:
			workflow = i
		}
	}
	if lint == -1 || workflow == -1 {
		t.Fatalf("changes = %v, want both files", items)
	}
	if lint > workflow {
		t.Errorf("changes = %v, want the lint declaration before the workflow that names it", items)
	}
}

// The registry is silent about which stacks are here, and forge's stamp on the
// pre-commit config still makes those hooks forge's to run.
func TestAStampedRepoWithNoComponentsGetsTheHooksJob(t *testing.T) {
	target := fixture(t, nil, map[string]string{".pre-commit-config.yaml": "# forge-toolchain: 1\nrepos: []\n"})

	applyAll(t, target, PreCommit{})
	applyAll(t, target, CI{})

	workflow, err := os.ReadFile(target.Path(ci.WorkflowPath))
	if err != nil {
		t.Fatalf("read the workflow: %s", err)
	}
	if !strings.Contains(string(workflow), "\n  "+ci.HooksJob+":\n") {
		t.Errorf("no hooks job:\n%s", workflow)
	}
}

// The precommit die would add every generic block's hooks to this config, and
// is not asked to. A job listing them would fail on each one this file lacks.
func TestTheHooksJobRunsTheHooksTheCommittedConfigHolds(t *testing.T) {
	committed := "# forge-toolchain: 1\n" +
		"repos:\n" +
		"  # generated:codespell\n" +
		"  - repo: https://github.com/codespell-project/codespell\n" +
		"    rev: v2.4.1\n" +
		"    hooks:\n" +
		"      - id: codespell\n"
	target := fixture(t, nil, map[string]string{".pre-commit-config.yaml": committed})

	applyAll(t, target, CI{})

	workflow, err := os.ReadFile(target.Path(ci.WorkflowPath))
	if err != nil {
		t.Fatalf("read the workflow: %s", err)
	}
	listed := regexp.MustCompile(`(?s)hooks=\(\n(.*?)\n\s*\)`).FindStringSubmatch(string(workflow))
	if listed == nil {
		t.Fatalf("no hook list:\n%s", workflow)
	}
	if got := strings.Fields(listed[1]); !slices.Equal(got, []string{"codespell"}) {
		t.Errorf("hooks job runs %v, want the committed config's [codespell]", got)
	}
}

func TestARepoForgeMaintainsNothingInIsOwedNoWorkflow(t *testing.T) {
	target := fixture(t, nil, nil)

	applyAll(t, target, CI{})

	if _, err := os.Stat(target.Path(ci.WorkflowPath)); !os.IsNotExist(err) {
		t.Errorf("a workflow was written for a repo with nothing to run: %v", err)
	}
}

func TestAHandWrittenWorkflowTakesTheDeclaredPinsAndNothingElse(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	release := "name: release\n" +
		"on: push\n" +
		"jobs:\n" +
		"  build:\n" +
		"    runs-on: ubuntu-latest\n" +
		"    steps:\n" +
		"      - uses: actions/checkout@v1\n" +
		"      - uses: actions/setup-go@" + commit + " # v5\n" +
		"        with:\n" +
		"          go-version: \"1.10\"\n" +
		"      - uses: example/unmanaged@v3\n"
	target := fixture(t, stacks("go"), map[string]string{".github/workflows/release.yml": release})

	applyAll(t, target, CI{})

	got := readFile(t, target.Path(".github/workflows/release.yml"))
	want := strings.NewReplacer(
		"actions/checkout@v1", "actions/checkout@fixture-checkout",
		"runs-on: ubuntu-latest", "runs-on: "+testAssets(t).Manifest.HostedRunner,
	).Replace(release)
	if got != want {
		t.Errorf("release.yml =\n%s\nwant only the checkout pin and the runner changed:\n%s", got, want)
	}
}

// The blocker says the two pipelines may duplicate jobs, and the pins are no
// part of that.
func TestAHandWrittenPipelineBesideTheGeneratedOneIsStillRepinned(t *testing.T) {
	pipeline := "name: ci\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v1\n"
	target := fixture(t, stacks("go"), map[string]string{".github/workflows/ci.yml": pipeline})

	applyAll(t, target, CI{})

	if got := readFile(t, target.Path(".github/workflows/ci.yml")); !strings.Contains(got, "actions/checkout@fixture-checkout") {
		t.Errorf("ci.yml kept its own checkout pin:\n%s", got)
	}
	changes, err := CI{}.Diff(target, mustObserve(t, target))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changedItems(changes), ".github/workflows/ci.yml") {
		t.Error("the blocker on the hand-written pipeline went with its pins")
	}
}

func mustObserve(t *testing.T, target reconcile.Target) reconcile.Observation {
	t.Helper()
	observed, err := CI{}.Observe(target)
	if err != nil {
		t.Fatalf("Observe: %s", err)
	}
	return observed
}
