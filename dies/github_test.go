package dies

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/v5/reconcile"
)

// githubDies are the ones whose answer depends on reaching GitHub, and which
// therefore have to distinguish "nothing to do" from "could not tell".
func githubDies() []reconcile.Die { return []reconcile.Die{MergeSettings{}, BranchProtection{}} }

// withRemote points the fixture repo at an origin without needing a real one.
func withRemote(t *testing.T, target reconcile.Target, url string) {
	t.Helper()
	if _, err := runIn(target.Repo.Path, "git", "init", "-q"); err != nil {
		t.Fatalf("git init: %s", err)
	}
	if _, err := runIn(target.Repo.Path, "git", "remote", "add", "origin", url); err != nil {
		t.Fatalf("git remote add: %s", err)
	}
}

// A repo on Bitbucket is not a repo that has fallen behind — it is one whose
// provider has its own settings and its own tool.
func TestGitHubDiesTreatAnotherProviderAsOutOfScope(t *testing.T) {
	for _, die := range githubDies() {
		t.Run(die.Name(), func(t *testing.T) {
			target := fixture(t, stacks("go"), nil)
			withRemote(t, target, "git@bitbucket.org:someone/thing.git")

			measured := reconcile.Assess(target, die)

			if len(measured.Changes) != 0 {
				t.Errorf("changes = %v, want none", measured.Changes)
			}
			if measured.Summary != "not a GitHub remote" {
				t.Errorf("summary = %q, want it to say why there is nothing to do", measured.Summary)
			}
			if got := measured.Fold(reconcile.LensPlan); got.Status != reconcile.Converged {
				t.Errorf("status = %q, want converged", got.Status)
			}
		})
	}
}

// One unauthenticated gh must not report the whole portfolio as drifted, nor
// offer apply as the fix for a login problem. Unverified is not permission.
func TestGitHubDiesReportUnmeasuredRatherThanDriftWhenGhIsMissing(t *testing.T) {
	empty := t.TempDir()
	gitOnly := filepath.Join(empty, "bin")
	if err := os.MkdirAll(gitOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	// git stays reachable so the remote check still runs; only gh is gone.
	linkTool(t, gitOnly, "git")
	t.Setenv("PATH", gitOnly)

	for _, die := range githubDies() {
		t.Run(die.Name(), func(t *testing.T) {
			target := fixture(t, stacks("go"), nil)
			withRemote(t, target, "git@github.com:datapointchris/thing.git")

			measured := reconcile.Assess(target, die)

			if len(measured.Changes) != 1 {
				t.Fatalf("changes = %v, want 1", measured.Changes)
			}
			if measured.Changes[0].Verdict != reconcile.Unknown {
				t.Errorf("verdict = %q, want unknown", measured.Changes[0].Verdict)
			}

			plan := measured.Fold(reconcile.LensPlan)
			if plan.Status != reconcile.Converged {
				t.Errorf("plan status = %q, want converged — an unreachable gh is not drift", plan.Status)
			}
			if plan.Unmeasured != 1 {
				t.Errorf("unmeasured = %d, want 1", plan.Unmeasured)
			}
			if reconcile.CodeFor([]reconcile.Result{plan}) != reconcile.ExitConverged {
				t.Error("an unreachable gh moved the exit code")
			}
		})
	}
}

// A repo with no origin at all cannot be asked about its settings, and is not
// drift for it.
func TestGitHubDiesSkipARepoWithNoRemote(t *testing.T) {
	for _, die := range githubDies() {
		t.Run(die.Name(), func(t *testing.T) {
			target := fixture(t, stacks("go"), nil)

			if measured := reconcile.Assess(target, die); len(measured.Changes) != 0 {
				t.Errorf("changes = %v, want none", measured.Changes)
			}
		})
	}
}

func linkTool(t *testing.T, dir, name string) {
	t.Helper()
	source, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	if err := os.Symlink(source, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}
