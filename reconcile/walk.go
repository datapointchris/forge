package reconcile

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Measurement is one die's look at one repo, before a lens decides what to keep.
//
// plan and check both fold this, and apply acts on it — the changes apply acts
// on are the ones that were printed, not a second look that may have found
// something different. Perform re-verifies live, which is what makes measuring
// once safe. The one second look is AppliedWrites.Remeasure, for a die that
// reads what other dies write, once one of them has written.
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
func Assess(ctx context.Context, t Target, d Die) (m Measurement) {
	m = Measurement{Target: t, Die: d}

	// Recovering is the Go spelling of catching an exception around each
	// resource: a panic in one die against one
	// repo must not abandon the other forty-seven. The value is carried into
	// the refusal rather than swallowed, so a forge bug still shows up as an
	// Issue on the repo that triggered it.
	defer func() {
		if r := recover(); r != nil {
			m.Changes = nil
			m.Refusal = fmt.Sprintf("the die panicked: %v", r)
		}
	}()

	observed, err := d.Observe(ctx, t)
	if err != nil {
		m.Refusal = fmt.Sprintf("could not measure: %v", err)
		return m
	}

	changes, err := d.Diff(t, observed)
	if err != nil {
		m.Refusal = fmt.Sprintf("could not compare with the standard: %v", err)
		return m
	}

	m.Changes = changes
	m.Summary = observed.Summary()
	return m
}

// Watch hears a walk as it goes. Any field may be nil. Every call arrives on
// the goroutine that called AssessAll, so none needs a lock.
type Watch struct {
	// Started is called as a target's first die begins.
	Started func(Target)
	// Finished is called as a target's last die ends, in the order targets
	// finish rather than the order they were given.
	Finished func(Target)
	// Measured gets one target's measurements once it and every target before
	// it have finished, so the calls arrive in target order.
	Measured func([]Measurement)
}

// AssessAll measures every repo with every die, and returns the measurements
// repo-major in target order.
//
// Repo-major so that a walk over several dies reads as a report on each repo
// rather than interleaving them. The die order is the caller's. Measuring needs
// no order, because no die writes here. apply performs in this order, so a die
// implementing ReadsOutput comes after each die it names.
//
// Up to workers targets are measured at once, because the dies spend most of a
// walk waiting on gh, pre-commit and uv. One target's dies still run in turn.
func AssessAll(ctx context.Context, targets []Target, dies []Die, workers int, watch Watch) []Measurement {
	type event struct {
		index    int
		finished bool
		measured []Measurement
	}
	indexes := make(chan int)
	events := make(chan event)

	var wg sync.WaitGroup
	for range max(1, min(workers, len(targets))) {
		wg.Go(func() {
			for i := range indexes {
				events <- event{index: i}
				measured := make([]Measurement, 0, len(dies))
				for _, d := range dies {
					measured = append(measured, Assess(ctx, targets[i], d))
				}
				events <- event{index: i, finished: true, measured: measured}
			}
		})
	}
	go func() {
		for i := range targets {
			indexes <- i
		}
		close(indexes)
		wg.Wait()
		close(events)
	}()

	byTarget := make([][]Measurement, len(targets))
	finished := make([]bool, len(targets))
	next := 0
	for e := range events {
		if !e.finished {
			if watch.Started != nil {
				watch.Started(targets[e.index])
			}
			continue
		}
		byTarget[e.index] = e.measured
		finished[e.index] = true
		if watch.Finished != nil {
			watch.Finished(targets[e.index])
		}
		for ; next < len(targets) && finished[next]; next++ {
			if watch.Measured != nil {
				watch.Measured(byTarget[next])
			}
		}
	}

	measurements := make([]Measurement, 0, len(targets)*len(dies))
	for _, measured := range byTarget {
		measurements = append(measurements, measured...)
	}
	return measurements
}

// Apply performs the actionable changes of one measurement, in the order the
// die decided them.
//
// Isolation is the same as Assess's and for the same reason: one item failing
// must not abandon the rest, or a run stops silently part-way through and
// nothing says anything about the half that never ran.
func Apply(ctx context.Context, m Measurement) []Outcome {
	if m.Refusal != "" {
		return nil
	}

	var outcomes []Outcome
	for _, change := range m.Changes {
		if !change.Actionable() {
			continue
		}
		outcomes = append(outcomes, perform(ctx, m.Target, m.Die, change))
	}
	return outcomes
}

// AppliedWrites holds which dies wrote to each target during one apply, keyed
// by the target's name and then the die's.
type AppliedWrites map[string]map[string]bool

// Record notes that m's die wrote to m's target when any outcome is Done.
func (w AppliedWrites) Record(m Measurement, outcomes []Outcome) {
	if !slices.ContainsFunc(outcomes, func(o Outcome) bool { return o.Status == Done }) {
		return
	}
	name := m.Target.Repo.Name
	if w[name] == nil {
		w[name] = map[string]bool{}
	}
	w[name][m.Die.Name()] = true
}

// Remeasure measures m's target with m's die again where m's die implements
// ReadsOutput and a die it names wrote to that target during this apply. It
// returns the new measurement and the die that wrote, or m and "".
func (w AppliedWrites) Remeasure(ctx context.Context, m Measurement) (Measurement, string) {
	reader, ok := m.Die.(ReadsOutput)
	if !ok {
		return m, ""
	}
	for _, upstream := range reader.ReadsOutputOf() {
		if w[m.Target.Repo.Name][upstream] {
			return Assess(ctx, m.Target, m.Die), upstream
		}
	}
	return m, ""
}

func perform(ctx context.Context, t Target, d Die, change Change) (out Outcome) {
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{Change: change, Status: Failed, Message: fmt.Sprintf("panic: %v", r)}
		}
	}()

	outcome, err := d.Perform(ctx, t, change)
	if err != nil {
		return Outcome{Change: change, Status: Failed, Message: err.Error()}
	}
	return outcome
}
