package cmd

import (
	"testing"

	"github.com/datapointchris/goclikit"

	"github.com/datapointchris/forge/lint"
)

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
