package dies

import (
	"testing"

	"github.com/datapointchris/forge/v6/reconcile"
)

// initRepo makes the fixture a real git repo with one commit on the named branch.
func initRepo(t *testing.T, target reconcile.Target, branch string) {
	t.Helper()
	dir := target.Repo.Path
	for _, args := range [][]string{
		{"init", "-q", "-b", branch},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "-A"},
		{"commit", "-qm", "initial"},
	} {
		if _, err := runIn(dir, "git", args...); err != nil {
			t.Fatalf("git %v: %s", args, err)
		}
	}
}

func TestDefaultBranchSaysNothingAboutARepoOnMain(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"README.md": "# fixture\n"})
	initRepo(t, target, "main")

	if measured := reconcile.Assess(target, DefaultBranch{}); len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
}

// A repo whose default branch is neither master nor main chose a third name.
// This die renames one to another and has nothing to say about that.
func TestDefaultBranchIgnoresARepoWithNeitherName(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"README.md": "# fixture\n"})
	initRepo(t, target, "trunk")

	if measured := reconcile.Assess(target, DefaultBranch{}); len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
}

// The script exited 1 — FAIL — for this, so a repo in an entirely ordinary
// state was reported as a failure and a sweep was mostly red for no fault.
func TestDefaultBranchReportsADirtyTreeAsByHandNotAsFailure(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"README.md": "# fixture\n"})
	initRepo(t, target, "master")
	if err := writeFile(target.Path("uncommitted.txt"), "work in progress\n"); err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, DefaultBranch{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1", measured.Changes)
	}
	if measured.Changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand", measured.Changes[0].Repair)
	}
	if plan := measured.Fold(reconcile.LensPlan); plan.Status != reconcile.Converged {
		t.Errorf("plan status = %q, want converged — apply must not touch this", plan.Status)
	}
	if check := measured.Fold(reconcile.LensCheck); check.Status != reconcile.Issue {
		t.Errorf("check status = %q, want issue", check.Status)
	}
}

// Perform re-verifies, and this is the die where it matters most: every step is
// hard to reverse.
func TestDefaultBranchRefusesWhenTheRepoStoppedBeingSafe(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"README.md": "# fixture\n"})
	initRepo(t, target, "master")

	change := reconcile.Change{Item: "master", Verdict: reconcile.Stale, Repair: reconcile.Automatic}
	if err := writeFile(target.Path("appeared-after-the-plan.txt"), "oops\n"); err != nil {
		t.Fatal(err)
	}

	outcome, err := DefaultBranch{}.Perform(target, change)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != reconcile.Refused {
		t.Errorf("status = %q, want refused", outcome.Status)
	}
	if !branchExists(target.Repo.Path, "master") {
		t.Error("a refused rename still renamed the branch")
	}
}
