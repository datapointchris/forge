package reconcile

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/datapointchris/forge/config"
)

func repoNamed(name string) config.Repo {
	return config.Repo{Name: name, Path: "/tmp/" + name}
}

type fakeObservation struct{ summary string }

func (o fakeObservation) Summary() string { return o.summary }

type fakeDie struct {
	name         string
	changes      []Change
	observeErr   error
	diffErr      error
	performErr   error
	observePanic bool
	performed    []string
}

func (d *fakeDie) Name() string        { return d.name }
func (d *fakeDie) Description() string { return "a fake" }
func (d *fakeDie) Tags() []string      { return nil }

func (d *fakeDie) Observe(Target) (Observation, error) {
	if d.observePanic {
		panic("registry index out of range")
	}
	if d.observeErr != nil {
		return nil, d.observeErr
	}
	return fakeObservation{summary: "nothing to do"}, nil
}

func (d *fakeDie) Diff(Target, Observation) ([]Change, error) {
	if d.diffErr != nil {
		return nil, d.diffErr
	}
	return d.changes, nil
}

func (d *fakeDie) Perform(_ Target, c Change) (Outcome, error) {
	if d.performErr != nil {
		return Outcome{}, d.performErr
	}
	d.performed = append(d.performed, c.Item)
	return Outcome{Change: c, Status: Done}, nil
}

func TestAssessCarriesChangesAndSummary(t *testing.T) {
	die := &fakeDie{name: "gitignore", changes: []Change{{Item: ".gitignore", Verdict: Missing, Repair: Automatic}}}

	m := Assess(Target{Repo: repoNamed("forge")}, die)

	if m.Refusal != "" {
		t.Fatalf("unexpected refusal: %s", m.Refusal)
	}
	if len(m.Changes) != 1 {
		t.Errorf("changes = %d, want 1", len(m.Changes))
	}
	if m.Summary != "nothing to do" {
		t.Errorf("summary = %q, want the observation's", m.Summary)
	}
}

func TestObserveFailureBecomesARefusalNamingTheRepo(t *testing.T) {
	die := &fakeDie{name: "merge-settings", observeErr: errors.New("gh: not logged in")}

	m := Assess(Target{Repo: repoNamed("mimic")}, die)

	if m.Refusal == "" {
		t.Fatal("an observe failure produced no refusal")
	}
	for _, want := range []string{"merge-settings", "mimic", "not logged in"} {
		if !strings.Contains(m.Refusal, want) {
			t.Errorf("refusal %q does not name %q", m.Refusal, want)
		}
	}
	if m.Changes != nil {
		t.Error("a refused measurement carried changes")
	}
}

func TestDiffFailureBecomesARefusal(t *testing.T) {
	die := &fakeDie{name: "precommit", diffErr: errors.New("block names an unmanaged repo")}

	if m := Assess(Target{Repo: repoNamed("forge")}, die); m.Refusal == "" {
		t.Error("a diff failure produced no refusal")
	}
}

// One die panicking on one repo must not abandon the other forty-seven, and the
// panic value has to survive into the refusal or a forge bug looks like a clean
// portfolio.
func TestPanicBecomesARefusalRatherThanEndingTheWalk(t *testing.T) {
	dies := []Die{&fakeDie{name: "boom", observePanic: true}, &fakeDie{name: "fine"}}
	targets := []Target{{Repo: repoNamed("forge")}, {Repo: repoNamed("beta")}}

	measurements := AssessAll(targets, dies, 2, Watch{})

	if len(measurements) != 4 {
		t.Fatalf("measurements = %d, want 4 (2 repos × 2 dies)", len(measurements))
	}

	var refused int
	for _, m := range measurements {
		if m.Refusal == "" {
			continue
		}
		refused++
		if !strings.Contains(m.Refusal, "index out of range") {
			t.Errorf("refusal %q lost the panic value", m.Refusal)
		}
	}
	if refused != 2 {
		t.Errorf("refused = %d, want 2 (the panicking die on each repo)", refused)
	}
}

// gatedDie holds every target but open until release closes, so a test decides
// which target finishes first.
type gatedDie struct {
	open    string
	release chan struct{}
}

func (*gatedDie) Name() string        { return "gated" }
func (*gatedDie) Description() string { return "a fake" }
func (*gatedDie) Tags() []string      { return nil }

func (d *gatedDie) Observe(t Target) (Observation, error) {
	if t.Repo.Name == d.open {
		return fakeObservation{}, nil
	}
	select {
	case <-d.release:
		return fakeObservation{}, nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("never released: the walk measured one target at a time")
	}
}

func (*gatedDie) Diff(Target, Observation) ([]Change, error) { return nil, nil }

func (*gatedDie) Perform(_ Target, c Change) (Outcome, error) {
	return Outcome{Change: c, Status: Done}, nil
}

// Rows print in registry order although several repos are measured at once.
// alpha cannot finish until beta has, so a serial walk fails here too.
func TestMeasuredArrivesInTargetOrderWhenALaterTargetFinishesFirst(t *testing.T) {
	gate := &gatedDie{open: "beta", release: make(chan struct{})}
	targets := []Target{{Repo: repoNamed("alpha")}, {Repo: repoNamed("beta")}}

	var finished, measured []string
	AssessAll(targets, []Die{gate}, 2, Watch{
		Finished: func(t Target) {
			finished = append(finished, t.Repo.Name)
			if t.Repo.Name == "beta" {
				close(gate.release)
			}
		},
		Measured: func(ms []Measurement) {
			measured = append(measured, ms[0].Target.Repo.Name)
			if ms[0].Refusal != "" {
				t.Error(ms[0].Refusal)
			}
		},
	})

	if !slices.Equal(finished, []string{"beta", "alpha"}) {
		t.Errorf("finished %v, want beta before alpha", finished)
	}
	if !slices.Equal(measured, []string{"alpha", "beta"}) {
		t.Errorf("measured %v, want registry order", measured)
	}
}

func TestApplyPerformsOnlyActionableChanges(t *testing.T) {
	die := &fakeDie{name: "gitignore", changes: []Change{
		{Item: "fixable", Verdict: Missing, Repair: Automatic},
		{Item: "needs-a-person", Verdict: Stale, Repair: ByHand},
		{Item: "not-ours", Verdict: Undeclared, Repair: ByHand},
		{Item: "unreachable", Verdict: Unknown, Repair: NoRepair},
	}}

	outcomes := Apply(Assess(Target{Repo: repoNamed("forge")}, die))

	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outcomes))
	}
	if len(die.performed) != 1 || die.performed[0] != "fixable" {
		t.Errorf("performed %v, want only the actionable change", die.performed)
	}
}

func TestApplyDoesNothingForARefusedMeasurement(t *testing.T) {
	die := &fakeDie{name: "ci", observeErr: errors.New("unreadable")}

	if outcomes := Apply(Assess(Target{Repo: repoNamed("forge")}, die)); outcomes != nil {
		t.Errorf("outcomes = %v, want none for a refused measurement", outcomes)
	}
}

func TestPerformFailureIsAFailedOutcomeNotAPanic(t *testing.T) {
	die := &fakeDie{
		name:       "gitignore",
		changes:    []Change{{Item: ".gitignore", Verdict: Missing, Repair: Automatic}},
		performErr: errors.New("permission denied"),
	}

	outcomes := Apply(Assess(Target{Repo: repoNamed("forge")}, die))

	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outcomes))
	}
	if outcomes[0].Status != Failed {
		t.Errorf("status = %q, want failed", outcomes[0].Status)
	}
}
