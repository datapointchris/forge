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

// The measured failure this fix is for.
//
// lintWorkflow builds a throwaway repo, and nothing a real repo carries on disk
// reaches it. So a workflow naming the pool was rejected there as an unknown
// label — refusing `forge repos plan ci` for every private repo at once, before
// the generated file could be inspected at all.
func TestTheDiesOwnLintRejectsThePoolLabelWithoutTheConfigAndAcceptsItWithOne(t *testing.T) {
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint is not installed, so runValidator returns no finding either way")
	}

	assets := testAssets(t)
	workflow, err := ci.Generate(assets.CI, assets.Manifest,
		[]config.Component{{Stack: "go", Dir: "."}}, nil, false, ci.SelfHosted)
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
// workflow, and that is exactly the state homelab is in.
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
