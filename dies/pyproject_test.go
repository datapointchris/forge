package dies

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

func requireUV(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not installed")
	}
}

func TestPyprojectIgnoresARepoDeclaringNoPython(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n"})

	measured := reconcile.Assess(target, Pyproject{})

	if len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
	if measured.Summary != "declares no python component" {
		t.Errorf("summary = %q, want it to say why", measured.Summary)
	}
}

func TestPyprojectIgnoresAPythonRepoWithNoPyproject(t *testing.T) {
	target := fixture(t, stacks("python"), nil)

	if measured := reconcile.Assess(target, Pyproject{}); len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
}

// The diff travels with the change, which is the reason this die was split out
// of the pre-commit sync: a template edit fans out to every Python repo at
// once, and seeing it before it lands is the difference between reviewing a
// change and reconstructing one.
func TestPyprojectCarriesTheDiffIntoThePlan(t *testing.T) {
	requireUV(t)
	target := fixture(t, stacks("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0.1.0\"\n",
	})

	measured := reconcile.Assess(target, Pyproject{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1", measured.Changes)
	}
	if measured.Changes[0].Patch == "" {
		t.Error("the change carries no diff, so a plan shows nothing to review")
	}
	if !strings.Contains(measured.Changes[0].Patch, "tool.ruff") {
		t.Errorf("the diff does not show the standard sections:\n%s", measured.Changes[0].Patch)
	}
}

// The read verb runs the same script with --check, so this is the case the
// no-writes property is really guarding.
func TestPyprojectReadLeavesThePyprojectByteIdentical(t *testing.T) {
	requireUV(t)
	original := "[project]\nname = \"fixture\"\nversion = \"0.1.0\"\n"
	target := fixture(t, stacks("python"), map[string]string{"pyproject.toml": original})

	reconcile.Assess(target, Pyproject{})

	if got := readFile(t, target.Path("pyproject.toml")); got != original {
		t.Errorf("observe wrote to pyproject.toml:\n%q", got)
	}
}

func TestPyprojectApplyThenPlanIsClean(t *testing.T) {
	requireUV(t)
	target := fixture(t, stacks("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0.1.0\"\n",
	})

	outcomes := applyAll(t, target, Pyproject{})
	if len(outcomes) != 1 || outcomes[0].Status != reconcile.Done {
		t.Fatalf("outcomes = %v, want one done", outcomes)
	}

	if measured := reconcile.Assess(target, Pyproject{}); len(measured.Changes) != 0 {
		t.Errorf("a second look found %v — the merge is not idempotent", measured.Changes)
	}
}

// A project's own settings are what per-key ownership exists to protect: the
// verb that owned whole sections deleted a repo's ruff exclude, a FastAPI
// repo's bugbear exemptions, and an alembic per-file-ignore.
func TestPyprojectKeepsTheProjectsOwnSettings(t *testing.T) {
	requireUV(t)
	target := fixture(t, stacks("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0.1.0\"\n\n" +
			"[tool.ruff]\nexclude = [\"migrations\"]\n",
	})

	applyAll(t, target, Pyproject{})

	if got := readFile(t, target.Path("pyproject.toml")); !strings.Contains(got, `exclude = ["migrations"]`) {
		t.Errorf("the merge deleted a setting forge does not own:\n%s", got)
	}
}

// The key is `mypy.ignore_missing_imports` because the real template sets it.
// A synthetic key would prove the conflict machinery runs and nothing about the
// standard this die actually deploys — testing.md § "A gate over a
// configuration probes a member of the population it selects".
//
// Built from lambda-durable-functions, which sets it to false under a comment
// explaining that an unresolved SDK import turns every decorated handler into
// Any and hides the call-arity errors basedpyright reports.
func TestPyprojectReportsADisagreementItMayNotRepair(t *testing.T) {
	requireUV(t)
	target := fixture(t, stacks("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0.1.0\"\n\n" +
			"[tool.mypy]\nignore_missing_imports = false\n",
	})

	measured := reconcile.Assess(target, Pyproject{})

	var conflict *reconcile.Change
	for i, change := range measured.Changes {
		if strings.Contains(change.Item, "ignore_missing_imports") {
			conflict = &measured.Changes[i]
		}
	}
	if conflict == nil {
		t.Fatalf("no change names the key the project disagrees on: %v", measured.Changes)
	}
	if conflict.Actionable() {
		t.Error("the conflict is actionable, so apply would invert the project's value")
	}
	if conflict.Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand so check reports it", conflict.Repair)
	}
	if conflict.Observed != "false" {
		t.Errorf("observed = %q, want the project's value spelled as TOML spells it", conflict.Observed)
	}
}

// Apply is the verb that did the damage, so the guarantee is asserted against
// apply rather than against the classification alone.
func TestPyprojectApplyLeavesADisagreedValueAlone(t *testing.T) {
	requireUV(t)
	target := fixture(t, stacks("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"fixture\"\nversion = \"0.1.0\"\n\n" +
			"[tool.mypy]\n# An unresolved import must fail rather than become Any.\n" +
			"ignore_missing_imports = false\n",
	})

	applyAll(t, target, Pyproject{})

	got := readFile(t, target.Path("pyproject.toml"))
	if !strings.Contains(got, "ignore_missing_imports = false") {
		t.Errorf("apply inverted a value the project chose:\n%s", got)
	}
	// The comment argues for the value beneath it. Inverting one and keeping the
	// other is what left a file asserting two contradictory things.
	if !strings.Contains(got, "must fail rather than become Any") {
		t.Errorf("apply dropped the comment explaining the value:\n%s", got)
	}
	// Never recorded, so retraction cannot reach it on a later run either.
	if strings.Contains(got, `["mypy", "ignore_missing_imports"]`) {
		t.Errorf("forge claimed a key it did not write:\n%s", got)
	}
}

// The diff carries a repo's own pyproject lines, and one of them can begin with
// anything. Scanning the whole output for the conflict prefix would read a line
// of somebody's file as a finding.
func TestParseMergeOutputStopsScanningAtTheDiff(t *testing.T) {
	out := strings.Join([]string{
		"would-update",
		"  conflict\tmypy.ignore_missing_imports\tfalse\ttrue",
		"  retracted ruff.lint.select",
		"--- pyproject.toml (current)",
		"+++ pyproject.toml (synced)",
		"   conflict\tnot.a.finding\tx\ty",
	}, "\n")

	status, conflicts, retracted, patch := parseMergeOutput(out)

	if status != "would-update" {
		t.Errorf("status = %q", status)
	}
	if len(conflicts) != 1 || conflicts[0].path != "mypy.ignore_missing_imports" {
		t.Fatalf("conflicts = %v, want exactly the one above the diff", conflicts)
	}
	if conflicts[0].project != "false" || conflicts[0].standard != "true" {
		t.Errorf("conflict values = %q/%q, want false/true", conflicts[0].project, conflicts[0].standard)
	}
	if len(retracted) != 1 || retracted[0] != "ruff.lint.select" {
		t.Errorf("retracted = %v", retracted)
	}
	if !strings.HasPrefix(patch, "--- pyproject.toml (current)") {
		t.Errorf("patch does not start at the diff header:\n%s", patch)
	}
}
