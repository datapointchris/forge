package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// Layout reports a repo whose declared components fit neither layout a repo may
// take.
//
// A single-purpose repo keeps its code at the root. A multi-subsystem repo
// keeps each subsystem in a directory of its own, and its root holds only
// orchestration. Read from the declared components rather than probed, because
// a layout is which directory holds which module, and the declaration already
// says that.
//
// Every finding is ByHand. Moving a module is a migration, which is why this
// reports and never repairs.
type Layout struct{}

func (Layout) Name() string { return "layout" }

func (Layout) Description() string {
	return "Report a repo keeping a module at its root beside subsystems in directories of their own, or a single-purpose Go tool with internal/, pkg/ or a cmd/<binary>/main.go. Never writes: moving a module is a migration."
}

func (Layout) Tags() []string { return []string{"structure", "scorecard"} }

// orchestrationStacks are the stacks an orchestration-only root may hold:
// compose files and Dockerfiles, deploy infrastructure, workflows and scripts.
// Every other stack is a module, and a module at the root beside subsystems is
// the shape neither layout allows.
var orchestrationStacks = map[string]bool{"docker": true, "terraform": true, "actions": true, "shell": true}

// nestedGoPackages are the paths a single-purpose Go tool never holds. Its
// packages sit flat beside main.go, so each of these is a second layer nothing
// needed. cmd/ alone is allowed, as a package of its own.
var nestedGoPackages = []string{"internal", "pkg", "cmd/*/main.go"}

type layoutState struct {
	declared    bool
	rootModules []string
	// subsystems are "stack in dir" for each module below the root.
	subsystems []string
	// nestedGo are the paths a flat Go tool holds from nestedGoPackages.
	nestedGo []string
}

func (s layoutState) Summary() string {
	switch {
	case !s.declared:
		return "declares no components, so there is no layout to read"
	case len(s.subsystems) > 0 && len(s.rootModules) == 0:
		return "subsystems under an orchestration-only root: " + strings.Join(s.subsystems, ", ")
	case len(s.subsystems) > 0:
		return "a module at the root beside subsystems"
	}
	return "single-purpose, flat at the root"
}

func (Layout) Observe(t reconcile.Target) (reconcile.Observation, error) {
	declared := t.Repo.Toolchain
	if declared == nil || len(declared.Components) == 0 {
		return layoutState{}, nil
	}

	state := layoutState{declared: true}
	goAtRoot := false
	for _, component := range declared.Components {
		if orchestrationStacks[component.Stack] {
			continue
		}
		dir := filepath.Clean(component.Dir)
		if dir == "." {
			state.rootModules = append(state.rootModules, component.Stack)
			goAtRoot = goAtRoot || component.Stack == "go"
			continue
		}
		state.subsystems = append(state.subsystems, component.Stack+" in "+dir)
	}

	if !goAtRoot || len(state.subsystems) > 0 {
		return state, nil
	}
	for _, pattern := range nestedGoPackages {
		matches, err := filepath.Glob(filepath.Join(t.Repo.Path, pattern))
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			rel, err := filepath.Rel(t.Repo.Path, match)
			if err != nil {
				return nil, err
			}
			if info, err := os.Stat(match); err == nil && info.IsDir() {
				rel += "/"
			}
			state.nestedGo = append(state.nestedGo, rel)
		}
	}
	return state, nil
}

func (Layout) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(layoutState)
	if !ok {
		return nil, fmt.Errorf("layout: unexpected observation %T", observed)
	}

	var changes []reconcile.Change
	if len(state.rootModules) > 0 && len(state.subsystems) > 0 {
		changes = append(changes, reconcile.Change{
			Item: "repo root", Verdict: reconcile.Stale, Repair: reconcile.ByHand,
			Detail: strings.Join(state.rootModules, " and ") + " sits at the root beside " +
				strings.Join(state.subsystems, ", ") + ", so the root is not orchestration only — " +
				"move it into a directory of its own beside the others",
		})
	}
	for _, rel := range state.nestedGo {
		changes = append(changes, reconcile.Change{
			Item: rel, Verdict: reconcile.Stale, Repair: reconcile.ByHand,
			Detail: "a single-purpose Go tool keeps its packages flat at the root beside main.go",
		})
	}
	return changes, nil
}

// Perform is unreachable: nothing this die reports is Actionable. It refuses
// rather than moving a module nobody designed the move for.
func (Layout) Perform(_ reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	return reconcile.Outcome{
		Change:  change,
		Status:  reconcile.Refused,
		Message: "moving a module is a migration, which only a person can plan",
	}, nil
}
