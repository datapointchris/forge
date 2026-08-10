// Package reconcile is the contract every die implements, and the walk that
// drives it.
//
// Three methods rather than two, so `plan` is a prefix of `apply`'s call graph
// rather than `apply` with a flag turned off:
//
//	plan:   Observe → Diff → render
//	apply:  Observe → Diff → render → Perform
//
// No method takes a dry-run parameter. Diff is pure and cannot write, Observe
// reads, and Perform is the only writer — unreachable from the read verbs
// because neither calls it. There is no branch inside a die asking whether it
// is allowed to write, so there is no branch that can be wrong.
//
// That is the whole point of the package. What it replaces was a FORGE_CHECK
// environment variable read just before each die's write, opted into per die
// with `supports_check: true` and verified by nothing — so a die that ignored
// the variable would write to every repo while the operator believed they were
// previewing.
//
// Ported from dotfiles' resource protocol (src/dotfiles/resources/__init__.py),
// which solved the same problem for one machine's worth of state.
package reconcile

// Verdict is what one item turned out to be.
//
// Unknown is first-class because the alternative is worse than useless: a die
// that cannot reach `gh`, or read a repo that is not in the registry, has not
// discovered that everything matches. Falling through an unmeasured item into
// "would fix" is the wrong answer with no way to tell it from a measured one.
// Unverified is not permission.
type Verdict string

const (
	Matched    Verdict = "matched"
	Missing    Verdict = "missing"
	Stale      Verdict = "stale"
	Undeclared Verdict = "undeclared"
	Unknown    Verdict = "unknown"
)

// Repair is who can fix this, which is not always us.
//
// A repo with unmarked custom hooks, a hand-written ci.yml, or a .planning
// directory that has diverged from its synced copy is real drift that apply
// must not silently swallow and cannot itself repair. Saying so on the Change
// is what lets `check` report it without `apply` reporting a failure for work
// it was never able to do — the collision that had rename-master-to-main exit
// 1 (FAIL) for a repo whose only fault was an uncommitted file.
type Repair string

const (
	Automatic Repair = "automatic"
	ByHand    Repair = "by_hand"
	NoRepair  Repair = "none"
)

// Change is one unit of work, decided but not performed.
//
// The whole contract between the two halves: the read verbs render these and
// stop, apply renders them and hands each back to Perform. Nothing else crosses
// the line, which is why a die never needs to know which verb invoked it.
type Change struct {
	Item     string  `json:"item"`
	Verdict  Verdict `json:"verdict"`
	Repair   Repair  `json:"repair"`
	Detail   string  `json:"detail,omitempty"`
	Observed string  `json:"observed,omitempty"`

	// Patch is the unified diff a file-shaped change would apply, when the die
	// can produce one. Named Patch rather than Diff so it does not read as the
	// output of the method next to it.
	//
	// Rendered in full by `plan`, deliberately: a template edit fans out to
	// every repo at once, and seeing that change across the portfolio before
	// applying it is the entire reason this package exists. A long plan is what
	// a plan looks like.
	Patch string `json:"patch,omitempty"`
}

// Drifted reports whether the repo differs from the standard at all.
func (c Change) Drifted() bool { return c.Verdict != Matched }

// Actionable reports whether apply has something it can do about it.
//
// Undeclared is deliberately never actionable. Forge does not delete what it
// did not put there — the same reason sync-gitignore was append-only — so an
// unrecognized hook or a stray config is something to report, never something
// to reconcile away.
func (c Change) Actionable() bool {
	return c.Repair == Automatic && (c.Verdict == Missing || c.Verdict == Stale)
}

// OutcomeStatus is what happened when apply acted on one Change.
type OutcomeStatus string

const (
	Done OutcomeStatus = "done"

	// Refused means a precondition failed at apply time and nothing was
	// written. Perform re-verifies live rather than trusting what Diff saw,
	// because Observe ran before the plan was printed and the operator may have
	// read it for a while; a plan that has gone stale is a reported outcome and
	// not a bad write.
	Refused OutcomeStatus = "refused"

	// Failed means a write was attempted and the world said no.
	Failed OutcomeStatus = "failed"

	// Skipped means the change was already true by the time it was reached.
	Skipped OutcomeStatus = "skipped"
)

// Outcome is what Perform did with one Change.
type Outcome struct {
	Change  Change        `json:"change"`
	Status  OutcomeStatus `json:"status"`
	Message string        `json:"message,omitempty"`
}

// OK reports whether the outcome should count against the run.
func (o Outcome) OK() bool { return o.Status != Failed }
