package cmd

import (
	"testing"

	"github.com/datapointchris/forge/suites"
	"github.com/datapointchris/goselfupdate/cobracmd"
)

func results(outcomes ...suites.Outcome) []suites.Result {
	out := make([]suites.Result, 0, len(outcomes))
	for i, outcome := range outcomes {
		out = append(out, suites.Result{Repo: "demo", Dir: ".", Outcome: outcome, ExitCode: i})
	}
	return out
}

// The regression this exists for: the --json branch returned before reaching the
// verdict, so `forge test --json` exited 0 on a failing suite while the text form
// exited 1. A caller reading the exit code got the opposite answer depending on a
// formatting flag, and `fleet test` stored that wrong answer on its first run.
func TestTheVerdictIsTheSameWhicheverFormWasRendered(t *testing.T) {
	failing := results(suites.Passed, suites.Failed)

	for _, asJSON := range []bool{false, true} {
		testJSON = asJSON
		if verdict(failing) == nil {
			t.Errorf("a failing suite must fail the run, and did not with --json=%v", asJSON)
		}
	}
	testJSON = false
}

func TestOnlyAFailureFailsTheRun(t *testing.T) {
	// `unknown` must not move the exit code: an unmeasurable component is not
	// drift, and a machine missing one runner would otherwise report a screen of
	// failures. `no_suite` is a repo with no tests yet, which is not failing.
	cases := []struct {
		name     string
		outcomes []suites.Outcome
		fails    bool
	}{
		{"all passed", []suites.Outcome{suites.Passed, suites.Passed}, false},
		{"nothing to run", []suites.Outcome{suites.NoSuite}, false},
		{"could not measure", []suites.Outcome{suites.Unknown, suites.Passed}, false},
		{"one failure among many", []suites.Outcome{suites.Passed, suites.Unknown, suites.Failed}, true},
		{"nothing at all", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verdict(results(tc.outcomes...))
			if tc.fails && err == nil {
				t.Error("expected the run to fail")
			}
			if !tc.fails && err != nil {
				t.Errorf("expected the run to pass, got %v", err)
			}
			if tc.fails && err != cobracmd.ErrReported {
				t.Errorf("a failure is already printed, so it must be ErrReported; got %v", err)
			}
		})
	}
}
