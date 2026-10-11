package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/goselfupdate/autoupdate"

	"github.com/datapointchris/forge/reconcile"
)

// Other readers of one registry take a wider set than forge does, so a bare
// list of names read as the whole registry and two tools appeared to disagree
// about how many repos it holds.
func TestReposListNamesItsSetAndWhatItLeftOut(t *testing.T) {
	checkout := t.TempDir()
	registry := filepath.Join(t.TempDir(), "repos.json")
	entry := func(name, status, owner string) string {
		return `{"name": "` + name + `", "path": "` + checkout + `", "status": "` + status + `", "owner": "` + owner + `"}`
	}
	body := `{"owner": "me", "repos": [` + strings.Join([]string{
		entry("awake", "active", "me"),
		entry("sleepy", "dormant", "me"),
		entry("dozy", "dormant", "me"),
		entry("upstream", "active", "someone-else"),
		entry("gone", "retired", "me"),
	}, ", ") + `]}`
	if err := os.WriteFile(registry, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	original := os.Args
	os.Args = []string{"forge", "repos", "list", "-c", registry}
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		os.Args = original
		cfgPath = ""
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := run(autoupdate.Config{Suppress: true}); err != nil {
		t.Fatalf("repos list: %v", err)
	}

	if out.String() != "awake\n" {
		t.Errorf("stdout = %q, want the one active owned repo alone", out.String())
	}
	want := "1 repo: the active ones the registry owns.\n" +
		"Left out: 2 dormant, 1 reference clone, 1 retired. -F reaches any but a retired one by name.\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q\nwant      %q", errOut.String(), want)
	}
}

// A plan row is one die on one repo, so a summary of row counts alone reads as
// a count of repos.
func TestTheSummaryNamesHowManyDiesOnHowManyRepos(t *testing.T) {
	results := []reconcile.Result{
		{Repo: "forge", Die: "gitignore", Status: reconcile.Converged},
		{Repo: "forge", Die: "precommit", Status: reconcile.Drift},
		{Repo: "beta", Die: "gitignore", Status: reconcile.Converged},
		{Repo: "beta", Die: "precommit", Status: reconcile.Converged},
	}

	var out bytes.Buffer
	reconcile.RenderSummary(&out, results, repos.coverage(results))

	if !strings.Contains(out.String(), "2 dies on 2 repos:") {
		t.Errorf("summary = %q, want it to lead with the dies and repos it counted over", out.String())
	}
}

func TestPendingSummaryCountsChangesAndDistinctRepos(t *testing.T) {
	planned := []reconcile.Result{
		{Repo: "forge", Die: "gitignore", Pending: 2},
		{Repo: "forge", Die: "precommit", Pending: 1},
		{Repo: "beta", Die: "gitignore", Pending: 3},
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
