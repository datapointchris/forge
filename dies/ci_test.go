package dies

import (
	"os"
	"os/exec"
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

// A public repo gets neither half, and the missing lint config is the half
// worth asserting. Declaring the label there would retire the one check that
// catches a hand-written workflow in a repo a fork can open a pull request
// against.
func TestAPublicRepoIsOfferedNoLintConfigAndStaysOnTheHostedImage(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, CI{})

	workflow, err := os.ReadFile(target.Path(ci.WorkflowPath))
	if err != nil {
		t.Fatalf("read the workflow: %s", err)
	}
	if strings.Contains(string(workflow), ci.RunnerLabel) {
		t.Errorf("a public repo's workflow names the pool:\n%s", workflow)
	}

	if _, err := os.Stat(target.Path(ci.ActionlintConfigPath)); !os.IsNotExist(err) {
		t.Errorf("a public repo was given %s", ci.ActionlintConfigPath)
	}
}

// lintWorkflow builds a throwaway repo, and nothing a real repo carries on disk
// reaches it. So a workflow naming the pool needs its declaration written
// beside it there, or actionlint reports an unknown label and the finding
// refuses `forge repos plan ci` for every private repo at once.
func TestTheDiesOwnLintRejectsThePoolLabelWithoutTheConfigAndAcceptsItWithOne(t *testing.T) {
	requireActionlint(t)

	assets := testAssets(t)
	workflow, err := ci.Generate(assets.CI, assets.Manifest,
		[]config.Component{{Stack: "go", Dir: "."}}, nil, ci.Ungated, ci.SelfHosted)
	if err != nil {
		t.Fatalf("Generate: %s", err)
	}

	bare := lintWorkflow(workflow, "")
	if !strings.Contains(bare, ci.RunnerLabel) {
		t.Fatalf("actionlint accepted an undeclared pool label, so this test measures nothing: %q", bare)
	}

	if finding := lintWorkflow(workflow, ci.ActionlintConfig(assets.Manifest.Version)); finding != "" {
		t.Errorf("the generated workflow does not lint against its own config: %s", finding)
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

// The choice of config is what a public repo's safety rests on, and it has to
// be assertable without running actionlint at all.
//
// Hoisting the config out of Observe's runner branch, so lintWorkflow always
// received one, used to pass the entire suite: the only assertion was that
// lintWorkflow honors the argument it is handed, never that Observe picks the
// right one.
func TestOnlyASelfHostedRepoIsOwedALintConfig(t *testing.T) {
	if got := lintConfigFor(ci.Hosted, 18); got != "" {
		t.Errorf("a public repo was offered a lint config: %q", got)
	}
	if got := lintConfigFor(ci.Runner(""), 18); got != "" {
		t.Errorf("the zero runner was offered a lint config: %q", got)
	}
	if got := lintConfigFor(ci.SelfHosted, 18); !strings.Contains(got, ci.RunnerLabel) {
		t.Errorf("a private repo's lint config does not declare %q: %q", ci.RunnerLabel, got)
	}
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

// A repo that turns public keeps whatever forge wrote while it was private, so
// the die has to observe a path it no longer writes to. Scoping that read to
// the runner left a stale self-hosted declaration on disk with check, plan and
// apply all reporting converged, because nothing was measuring it.
func TestARepoThatTurnsPublicHasItsLintConfigRemoved(t *testing.T) {
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

	if _, err := os.Stat(target.Path(ci.ActionlintConfigPath)); !os.IsNotExist(err) {
		t.Errorf("%s survived the repo going public", ci.ActionlintConfigPath)
	}
	if again := reconcile.Assess(target, CI{}); len(again.Changes) != 0 {
		t.Errorf("changes after the retraction = %v, want none", changedItems(again.Changes))
	}
}

// forge removes only what it wrote. A lint config with no stamp is the repo's
// own file, whatever runner that repo now takes.
func TestAHandWrittenLintConfigIsNotRemovedFromAPublicRepo(t *testing.T) {
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
