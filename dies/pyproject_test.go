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
