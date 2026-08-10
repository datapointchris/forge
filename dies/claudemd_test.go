package dies

import (
	"strings"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

// The old check exited 1 — a failure — for a repo that simply had not been
// given one yet. It is an Issue now: the same severity, said honestly.
func TestClaudeMDMissingIsAnIssueAndNeverAPlan(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	measured := reconcile.Assess(target, ClaudeMD{})

	if check := measured.Fold(reconcile.LensCheck); check.Status != reconcile.Issue {
		t.Errorf("check status = %q, want issue", check.Status)
	}
	if plan := measured.Fold(reconcile.LensPlan); plan.Status != reconcile.Converged {
		t.Errorf("plan status = %q, want converged — forge cannot write one", plan.Status)
	}
}

func TestClaudeMDSubstantialFileIsConverged(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		"CLAUDE.md": strings.Repeat("a line about this repo\n", 40),
	})

	if measured := reconcile.Assess(target, ClaudeMD{}); len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
}

func TestClaudeMDPlaceholderIsReported(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"CLAUDE.md": "# CLAUDE.md\n\nTODO\n"})

	measured := reconcile.Assess(target, ClaudeMD{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1", measured.Changes)
	}
	if measured.Changes[0].Repair != reconcile.ByHand {
		t.Errorf("repair = %q, want by_hand", measured.Changes[0].Repair)
	}
}

// Nothing this die reports is Actionable, so Perform is unreachable from every
// verb. It refuses rather than writing, in case that ever stops being true.
func TestClaudeMDNeverWrites(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	outcome, err := ClaudeMD{}.Perform(target, reconcile.Change{Item: "CLAUDE.md", Verdict: reconcile.Missing, Repair: reconcile.ByHand})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != reconcile.Refused {
		t.Errorf("status = %q, want refused", outcome.Status)
	}
}
