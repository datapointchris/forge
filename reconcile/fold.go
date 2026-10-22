package reconcile

import (
	"fmt"
	"slices"
	"strings"
)

// Lens is which question is being asked of one measurement.
//
// plan and check measure the same repo and differ only in what they keep, so
// they are two folds over one walk rather than two walks. The split uses a
// field that already had to exist: Repair says who can fix a Change, so plan
// keeps what apply can and check keeps what it cannot.
type Lens string

const (
	// LensPlan keeps what apply would change: automatic repairs of a missing
	// or stale item.
	LensPlan Lens = "plan"

	// LensCheck keeps what is wrong — findings apply cannot fix. A hand-written
	// ci.yml, unmarked custom hooks that would abort a sync, a synced
	// directory diverged from its copy in the sync base, a missing CLAUDE.md. Plus
	// anything that refused to be measured.
	LensCheck Lens = "check"
)

// Status is one repo's verdict for one die, rolled up from its changes.
//
// Drift and Issue are different kinds, not degrees. Drift is expected and
// benign — the repo differs from the standard, which is what apply is for. An
// Issue is something wrong. Collapsing them is what makes an exit code
// meaningless: the measured cost was a scheduled unit sitting permanently
// failed on a machine whose only fault was being a version behind.
type Status string

const (
	Converged Status = "converged"
	Drift     Status = "drift"
	Issue     Status = "issue"
)

// Result is one die's answer for one repo.
type Result struct {
	Repo   string `json:"repo"`
	Die    string `json:"die"`
	Status Status `json:"status"`
	Detail string `json:"detail"`

	// Changes are the ones this lens kept — what the rows under the summary
	// showed. The counts below cover the ones it did not.
	Changes []Change `json:"changes,omitempty"`

	Pending    int `json:"pending"`
	Attention  int `json:"attention"`
	Unmeasured int `json:"unmeasured"`

	// Refusal is set when the die could not be run against this repo at all.
	// Distinct from finding nothing: a die that crashed has not discovered that
	// the repo is converged, and a reader treating the two alike would report a
	// clean portfolio on the strength of a broken checker.
	Refusal string `json:"refusal,omitempty"`
}

// Sift splits one die's changes into what each lens keeps, and what neither does.
//
// An item nobody could measure is in neither. Nothing about it is known to
// differ, and no die crashed, so it is counted and named rather than rendered
// as a row and rather than moving the exit code. Without that, one unauthenticated
// `gh` would make every repo report drift at once.
func Sift(changes []Change) (pending, attention, unmeasured []Change) {
	for _, c := range changes {
		switch {
		case c.Verdict == Unknown && c.Repair == NoRepair:
			unmeasured = append(unmeasured, c)
		case c.Actionable():
			pending = append(pending, c)
		case c.Drifted():
			attention = append(attention, c)
		}
	}
	return pending, attention, unmeasured
}

// Fold turns one die's changes for one repo into the row its verb prints.
func Fold(repo, die string, changes []Change, summary string, lens Lens) Result {
	pending, attention, unmeasured := Sift(changes)

	kept := pending
	if lens == LensCheck {
		kept = attention
	}

	result := Result{
		Repo:       repo,
		Die:        die,
		Changes:    kept,
		Pending:    len(pending),
		Attention:  len(attention),
		Unmeasured: len(unmeasured),
	}

	gap := ""
	if len(unmeasured) > 0 {
		var reasons []string
		for _, c := range unmeasured {
			if c.Detail != "" && !slices.Contains(reasons, c.Detail) {
				reasons = append(reasons, c.Detail)
			}
		}
		gap = fmt.Sprintf("; %d could not be measured", len(unmeasured))
		if len(reasons) > 0 {
			gap += " (" + strings.Join(reasons, "; ") + ")"
		}
	}

	switch {
	// A die writes its summary for a repo with nothing to change, so under check
	// it would call a pending repair current.
	case len(kept) == 0 && len(pending) > 0:
		result.Status = Converged
		result.Detail = "no problems; plan has " + plural(len(pending), "change", "changes") + gap
	// Under plan the summary would call current a file a by-hand finding holds
	// back from its write.
	case len(kept) == 0 && len(attention) > 0:
		result.Status = Converged
		result.Detail = "no changes; check has " + plural(len(attention), "problem", "problems") + gap
	case len(kept) == 0:
		result.Status = Converged
		result.Detail = summary + gap
	case lens == LensPlan:
		result.Status = Drift
		result.Detail = plural(len(kept), "change", "changes") + " apply would make" + gap
	default:
		result.Status = Issue
		result.Detail = plural(len(kept), "problem", "problems") + " to fix by hand" + gap
	}
	// A lone change is the row, because its own line says more than a count of one.
	if len(kept) == 1 && kept[0].Detail != "" {
		result.Detail = kept[0].Detail + gap
	}

	return result
}

// plural is n with whichever noun agrees with it.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Applied is the row apply prints once it has acted on r's changes. A failed
// write is an Issue. A refused change was never written, so the row stays
// drift. Otherwise every change was written or already true, and it converged.
func Applied(r Result, outcomes []Outcome) Result {
	var applied, refused int
	for _, o := range outcomes {
		switch o.Status {
		case Failed:
			r.Status, r.Detail = Issue, o.Message
			return r
		case Refused:
			refused++
		case Done, Skipped:
			applied++
		}
	}
	if refused > 0 {
		r.Status = Drift
		r.Detail = fmt.Sprintf("%s applied, %d refused", plural(applied, "change", "changes"), refused)
		return r
	}
	r.Status = Converged
	r.Detail = plural(applied, "change", "changes") + " applied"
	return r
}

// Refuse is the Result for a die that could not examine a repo.
//
// An Issue under either lens: check because a die that could not run is exactly
// what it exists to report, and plan because a repo that could not be measured
// cannot be said to have nothing to change.
func Refuse(repo, die, reason string) Result {
	return Result{Repo: repo, Die: die, Status: Issue, Detail: reason, Refusal: reason}
}

// ExitCode is what a caller branches on, and is this command's machine-readable
// contract.
type ExitCode int

const (
	// ExitConverged means nothing to do.
	ExitConverged ExitCode = 0
	// ExitDrift means changes are pending, following `terraform plan
	// -detailed-exitcode`. Only plan returns it; drift is not check's subject.
	ExitDrift ExitCode = 1
	// ExitUsage means the command line was wrong, the one failure worth
	// retrying with different arguments. Returned by goclikit, never here.
	ExitUsage ExitCode = 2
	// ExitIssue means something is wrong. Three exists so that check cannot
	// report "a die crashed" as "no drift".
	ExitIssue ExitCode = 3
)

// CodeFor folds many results into the one number a caller branches on. An
// Issue outranks drift.
func CodeFor(results []Result) ExitCode {
	code := ExitConverged
	for _, r := range results {
		switch r.Status {
		case Issue:
			return ExitIssue
		case Drift:
			code = ExitDrift
		case Converged:
		}
	}
	return code
}

// ExitError carries a non-zero verdict up to main without a message.
//
// The verb has already printed its rows, so there is nothing left to say; what
// is left is the number. cmd.Execute unwraps this before its error printer, so
// a plan that found drift exits 1 silently rather than printing an error for
// something that is not one.
type ExitError struct{ Code ExitCode }

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// ExitFor returns the error a verb should return, or nil when converged.
func ExitFor(results []Result) error {
	if code := CodeFor(results); code != ExitConverged {
		return &ExitError{Code: code}
	}
	return nil
}
