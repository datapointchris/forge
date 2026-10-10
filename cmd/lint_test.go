package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"

	"github.com/datapointchris/forge/lint"
)

// `forge lint sleepy` where sleepy is dormant, like most of a registry, linted
// nothing and exited 0, and so did `forge lint no-such-repo`. A script running
// `forge lint <repo> && deploy` read both as the repo passing.
func TestANamedDormantRepoIsLintedAndANameMatchingNothingExits2(t *testing.T) {
	checkout := t.TempDir()
	registry := filepath.Join(t.TempDir(), "repos.json")
	body := `{"owner": "me", "repos": [
		{"name": "sleepy", "path": "` + checkout + `", "status": "dormant", "owner": "me"},
		{"name": "awake", "path": "` + checkout + `", "owner": "me"}]}`
	if err := os.WriteFile(registry, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfgPath, lintJSON = "", false })

	for _, verb := range []string{"lint", "test"} {
		err := runLine(t, verb, "-c", registry, "awake", "no-such-repo")
		if !errors.Is(err, goclikit.ErrUsage) {
			t.Errorf("%s awake no-such-repo = %v, want a usage error, which exits 2", verb, err)
		} else if !strings.HasSuffix(err.Error(), "no repos matched: no-such-repo") {
			t.Errorf("%s answered %q, want it to name the one name that matched nothing", verb, err)
		}
	}

	var out bytes.Buffer
	os.Args = []string{"forge", "lint", "-c", registry, "--json", "sleepy"}
	rootCmd.SetOut(&out)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	if err := run(autoupdate.Config{Suppress: true}); err != nil {
		t.Fatalf("lint sleepy: %v", err)
	}
	var results []lint.Result
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("lint --json printed %q: %v", out.String(), err)
	}
	if len(results) != 1 || results[0].Repo != "sleepy" {
		t.Errorf("lint sleepy reported %+v, want the dormant repo it named", results)
	}
}

// A machine missing one linter would otherwise fail every repo using it, and a
// repo with no hooks is not failing.
func TestOnlyAFailedHookFailsTheLintRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcomes []lint.Outcome
		fails    bool
	}{
		{"all passed", []lint.Outcome{lint.Passed, lint.Passed}, false},
		{"no hooks", []lint.Outcome{lint.NoHooks}, false},
		{"a tool missing here", []lint.Outcome{lint.Unknown, lint.Passed}, false},
		{"one failure", []lint.Outcome{lint.Passed, lint.Unknown, lint.Failed}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var results []lint.Result
			for _, outcome := range tc.outcomes {
				results = append(results, lint.Result{Repo: "demo", Outcome: outcome})
			}
			err := lintVerdict(results)
			if tc.fails && err != goclikit.ErrReported {
				t.Errorf("err = %v, want ErrReported: the failure is already printed", err)
			}
			if !tc.fails && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}
