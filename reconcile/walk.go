package reconcile

import "fmt"

// Measurement is one die's look at one repo, before a lens decides what to keep.
//
// plan and check both fold this, and apply acts on it — the changes apply acts
// on are the ones that were printed, not a second look that may have found
// something different. Perform re-verifies live, which is what makes measuring
// once safe.
type Measurement struct {
	Target  Target
	Die     Die
	Changes []Change
	Summary string
	Refusal string
}

// Fold applies a lens to this measurement.
func (m Measurement) Fold(lens Lens) Result {
	if m.Refusal != "" {
		return Refuse(m.Target.Repo.Name, m.Die.Name(), m.Refusal)
	}
	return Fold(m.Target.Repo.Name, m.Die.Name(), m.Changes, m.Summary, lens)
}

// Assess measures one repo with one die. Reads only; never writes.
//
// The whole of plan and check, and the first half of apply. A die that cannot
// answer produces a Refusal and the caller carries on, because "one die
// crashed" and "nothing to do" must not look the same to whatever folds this.
func Assess(t Target, d Die) (m Measurement) {
	m = Measurement{Target: t, Die: d}

	// Recovering is the Go spelling of catching an exception around each
	// resource: a panic in one die against one
	// repo must not abandon the other forty-seven. The value is carried into
	// the refusal rather than swallowed, so a forge bug still shows up as an
	// Issue on the repo that triggered it.
	defer func() {
		if r := recover(); r != nil {
			m.Changes = nil
			m.Refusal = fmt.Sprintf("%s panicked on %s: %v", d.Name(), t.Repo.Name, r)
		}
	}()

	observed, err := d.Observe(t)
	if err != nil {
		m.Refusal = fmt.Sprintf("%s could not examine %s: %v", d.Name(), t.Repo.Name, err)
		return m
	}

	changes, err := d.Diff(t, observed)
	if err != nil {
		m.Refusal = fmt.Sprintf("%s could not diff %s: %v", d.Name(), t.Repo.Name, err)
		return m
	}

	m.Changes = changes
	m.Summary = observed.Summary()
	return m
}

// AssessAll measures every repo with every die, repo-major.
//
// Repo-major so that a walk over several dies reads as a report on each repo
// rather than interleaving them. The die order is the caller's, and it is
// presentation only — forge's dies are independent of each other, unlike a
// resource order where each step depends on the one before.
func AssessAll(targets []Target, dies []Die) []Measurement {
	measurements := make([]Measurement, 0, len(targets)*len(dies))
	for _, t := range targets {
		for _, d := range dies {
			measurements = append(measurements, Assess(t, d))
		}
	}
	return measurements
}

// Apply performs the actionable changes of one measurement, in the order the
// die decided them.
//
// Isolation is the same as Assess's and for the same reason: one item failing
// must not abandon the rest, or a run stops silently part-way through and
// nothing says anything about the half that never ran.
func Apply(m Measurement) []Outcome {
	if m.Refusal != "" {
		return nil
	}

	var outcomes []Outcome
	for _, change := range m.Changes {
		if !change.Actionable() {
			continue
		}
		outcomes = append(outcomes, perform(m.Target, m.Die, change))
	}
	return outcomes
}

func perform(t Target, d Die, change Change) (out Outcome) {
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{Change: change, Status: Failed, Message: fmt.Sprintf("panic: %v", r)}
		}
	}()

	outcome, err := d.Perform(t, change)
	if err != nil {
		return Outcome{Change: change, Status: Failed, Message: err.Error()}
	}
	return outcome
}
