package reconcile

import (
	"strings"
	"testing"
)

func TestSiftClassifiesEachKind(t *testing.T) {
	changes := []Change{
		{Item: "matched", Verdict: Matched, Repair: NoRepair},
		{Item: "fixable", Verdict: Missing, Repair: Automatic},
		{Item: "stale-fixable", Verdict: Stale, Repair: Automatic},
		{Item: "needs-a-person", Verdict: Stale, Repair: ByHand},
		{Item: "not-ours", Verdict: Undeclared, Repair: ByHand},
		{Item: "unreachable", Verdict: Unknown, Repair: NoRepair},
	}

	pending, attention, unmeasured := Sift(changes)

	if len(pending) != 2 {
		t.Errorf("pending = %d, want 2 (the two automatic repairs)", len(pending))
	}
	if len(attention) != 2 {
		t.Errorf("attention = %d, want 2 (by-hand drift)", len(attention))
	}
	if len(unmeasured) != 1 {
		t.Errorf("unmeasured = %d, want 1", len(unmeasured))
	}
}

// An unmeasured item must not move the exit code. One unauthenticated gh would
// otherwise report drift in every repo at once, which is what the Unknown
// verdict exists to prevent.
func TestUnmeasuredIsNeitherLensAndDoesNotDrift(t *testing.T) {
	changes := []Change{{Item: "merge settings", Verdict: Unknown, Repair: NoRepair}}

	plan := Fold("forge", "merge-settings", changes, "settings current", LensPlan)
	if plan.Status != Converged {
		t.Errorf("plan status = %q, want converged", plan.Status)
	}
	if plan.Unmeasured != 1 {
		t.Errorf("unmeasured = %d, want 1", plan.Unmeasured)
	}
	if CodeFor([]Result{plan}) != ExitConverged {
		t.Error("an unmeasurable item moved the exit code")
	}

	check := Fold("forge", "merge-settings", changes, "settings current", LensCheck)
	if check.Status != Converged {
		t.Errorf("check status = %q, want converged", check.Status)
	}
}

func TestPlanAndCheckKeepDifferentChanges(t *testing.T) {
	changes := []Change{
		{Item: ".gitignore", Verdict: Missing, Repair: Automatic, Detail: "would add __pycache__/"},
		{Item: "ci.yml", Verdict: Undeclared, Repair: ByHand, Detail: "hand-written pipeline"},
	}

	plan := Fold("forge", "ci", changes, "", LensPlan)
	if plan.Status != Drift {
		t.Errorf("plan status = %q, want drift", plan.Status)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Item != ".gitignore" {
		t.Errorf("plan kept %v, want only the automatic repair", plan.Changes)
	}

	check := Fold("forge", "ci", changes, "", LensCheck)
	if check.Status != Issue {
		t.Errorf("check status = %q, want issue", check.Status)
	}
	if len(check.Changes) != 1 || check.Changes[0].Item != "ci.yml" {
		t.Errorf("check kept %v, want only the by-hand finding", check.Changes)
	}
}

func TestConvergedRowUsesTheObservationSummary(t *testing.T) {
	result := Fold("forge", "gitignore", []Change{{Verdict: Matched, Repair: NoRepair}}, "gitignore current (12 entries)", LensPlan)

	if result.Status != Converged {
		t.Fatalf("status = %q, want converged", result.Status)
	}
	if result.Detail != "gitignore current (12 entries)" {
		t.Errorf("detail = %q, want the observation's own sentence", result.Detail)
	}
}

// The die's summary describes a repo with nothing to change, so a row holding
// a change, under either lens, must never print it.
func TestARowNamesWhatItHoldsRatherThanTheSummary(t *testing.T) {
	const summary = "branch protection current on main"
	automatic := Change{Item: "main", Verdict: Missing, Repair: Automatic, Detail: "main has no branch protection"}
	byHand := Change{Item: "validate.yml", Verdict: Stale, Repair: ByHand, Detail: "validate.yml has an orphaned custom section"}

	for _, tc := range []struct {
		name   string
		change Change
		lens   Lens
		status Status
		want   string
	}{
		{"check over a pending repair points at plan", automatic, LensCheck, Converged, "plan"},
		{"plan over a by-hand finding points at check", byHand, LensPlan, Converged, "check"},
		{"one pending repair is the row's own text", automatic, LensPlan, Drift, automatic.Detail},
		{"one by-hand finding is the row's own text", byHand, LensCheck, Issue, byHand.Detail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Fold("font", "branch-protection", []Change{tc.change}, summary, tc.lens)
			if result.Status != tc.status {
				t.Errorf("status = %q, want %q", result.Status, tc.status)
			}
			if !strings.Contains(result.Detail, tc.want) || strings.Contains(result.Detail, summary) {
				t.Errorf("detail = %q, want it to carry %q and not the summary", result.Detail, tc.want)
			}
		})
	}
}

// Once apply has written, its row reports the write rather than the drift the
// plan found, or apply's output reads the same as plan's.
func TestAppliedReportsTheWriteRatherThanThePlan(t *testing.T) {
	planned := Result{Repo: "scratch", Die: "gitignore", Status: Drift, Detail: "2 changes apply would make"}
	cases := []struct {
		name     string
		outcomes []Outcome
		want     Status
	}{
		{"written or already true", []Outcome{{Status: Done}, {Status: Skipped}}, Converged},
		{"one refused", []Outcome{{Status: Done}, {Status: Refused}}, Drift},
		{"one failed", []Outcome{{Status: Failed, Message: "permission denied"}, {Status: Done}}, Issue},
	}
	for _, c := range cases {
		if got := Applied(planned, c.outcomes); got.Status != c.want {
			t.Errorf("%s: status = %q, want %q", c.name, got.Status, c.want)
		}
	}
}

func TestIssueOutranksDrift(t *testing.T) {
	results := []Result{
		{Repo: "a", Status: Drift},
		{Repo: "b", Status: Issue},
		{Repo: "c", Status: Converged},
	}
	if got := CodeFor(results); got != ExitIssue {
		t.Errorf("CodeFor = %d, want %d", got, ExitIssue)
	}
}

func TestExitForIsNilOnlyWhenConverged(t *testing.T) {
	if err := ExitFor([]Result{{Status: Converged}}); err != nil {
		t.Errorf("converged returned %v, want nil", err)
	}

	err := ExitFor([]Result{{Status: Drift}})
	exit, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("drift returned %T, want *ExitError", err)
	}
	if exit.Code != ExitDrift {
		t.Errorf("code = %d, want %d", exit.Code, ExitDrift)
	}
}

// A refusal is an Issue under both lenses: check because a die that could not
// run is what it exists to report, and plan because a repo that could not be
// measured cannot be said to have nothing to change.
func TestRefusalIsAnIssueUnderBothLenses(t *testing.T) {
	measured := Measurement{
		Target:  Target{Repo: repoNamed("forge")},
		Die:     &fakeDie{name: "gitignore"},
		Refusal: "not in the repo registry",
	}

	for _, lens := range []Lens{LensPlan, LensCheck} {
		if got := measured.Fold(lens); got.Status != Issue {
			t.Errorf("%s status = %q, want issue", lens, got.Status)
		}
	}
}
