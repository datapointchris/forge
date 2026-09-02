package cmd

import (
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

func TestPendingSummaryCountsChangesAndDistinctRepos(t *testing.T) {
	planned := []reconcile.Result{
		{Repo: "forge", Die: "gitignore", Pending: 2},
		{Repo: "forge", Die: "precommit", Pending: 1},
		{Repo: "dotfiles", Die: "gitignore", Pending: 3},
		// Attention and unmeasured are not apply's to do, so neither counts.
		{Repo: "refcheck", Die: "claude-md", Attention: 1},
		{Repo: "mimic", Die: "merge-settings", Unmeasured: 1},
	}

	changes, repos := pendingSummary(planned)

	if changes != 6 {
		t.Errorf("changes = %d, want 6", changes)
	}
	if repos != 2 {
		t.Errorf("repos = %d, want 2 — forge counted once across two dies", repos)
	}
}

// Nothing pending means nothing to confirm; prompting on a converged portfolio
// trains the operator to answer without reading.
func TestPendingSummaryIsZeroWhenConverged(t *testing.T) {
	changes, repos := pendingSummary([]reconcile.Result{
		{Repo: "forge", Die: "ci", Status: reconcile.Converged},
	})

	if changes != 0 || repos != 0 {
		t.Errorf("changes = %d repos = %d, want 0 and 0", changes, repos)
	}
}

func TestPluralNamesBothForms(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{1, "1 repo"}, {0, "0 repos"}, {2, "2 repos"}} {
		if got := plural(tc.n, "repo", "repos"); got != tc.want {
			t.Errorf("plural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
