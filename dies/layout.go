package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// Layout reports a single-purpose Go tool whose packages are not flat at the
// root.
//
// A repo whose only module is Go at the root is a single-purpose tool, and the
// declared components say so. Where a multi-component repo puts its components
// is not checked, because its root may hold any of them.
//
// Every finding is ByHand. Moving a package is a migration, which is why this
// reports and never repairs.
type Layout struct{}

func (Layout) Name() string { return "layout" }

func (Layout) Description() string {
	return "Report a single-purpose Go tool with internal/, pkg/ or a cmd/<binary>/main.go. Never writes: moving a package is a migration."
}

func (Layout) Tags() []string { return []string{"structure", "scorecard"} }

// orchestrationStacks are the stacks that are no module of their own: compose
// files and Dockerfiles, deploy infrastructure, workflows and scripts. A Go
// tool declaring one below its root is still single-purpose.
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
	case len(s.subsystems) > 0:
		placed := make([]string, 0, len(s.rootModules)+len(s.subsystems))
		for _, stack := range s.rootModules {
			placed = append(placed, stack+" at the root")
		}
		return strings.Join(append(placed, s.subsystems...), ", ")
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
	for _, rel := range state.nestedGo {
		changes = append(changes, reconcile.Change{
			Item: rel, Verdict: reconcile.Stale, Repair: reconcile.ByHand,
			Detail: "a single-purpose Go tool keeps its packages flat at the root beside main.go",
		})
	}
	return changes, nil
}

// Perform is unreachable: nothing this die reports is Actionable. It refuses
// rather than moving a package nobody designed the move for.
func (Layout) Perform(_ reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	return reconcile.Outcome{
		Change:  change,
		Status:  reconcile.Refused,
		Message: "moving a package is a migration, which only a person can plan",
	}, nil
}
