package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/datapointchris/forge/reconcile"
)

// BrokenSymlinks reports a tracked symlink whose target does not resolve.
//
// Tracked only, which is the distinction that makes this reportable at all. An
// untracked dangling link is usually a machine fact — a tool not installed
// here, a path that exists on the other laptop — and reporting those would
// bury the finding that matters. A symlink git carries is in every clone, so
// if it dangles here it dangles for everyone.
//
// Every finding is ByHand. A dangling link can be waiting for something not
// installed yet, and deleting one that was correct is the failure to avoid.
type BrokenSymlinks struct{ reconcile.ReportOnly }

func (BrokenSymlinks) Name() string { return "broken-symlinks" }

func (BrokenSymlinks) Description() string {
	return "Report a tracked symlink whose target does not resolve. Never writes: a link may be waiting for something not installed yet."
}

func (BrokenSymlinks) Tags() []string { return []string{"git", "hygiene", "scorecard"} }

type brokenLink struct {
	path   string
	target string
}

type brokenSymlinksState struct {
	unversioned bool
	links       int
	found       []brokenLink
}

func (s brokenSymlinksState) Summary() string {
	if s.unversioned {
		return "not a git checkout"
	}
	if s.links == 0 {
		return "no tracked symlinks"
	}
	return fmt.Sprintf("%s tracked, all resolve", plural(s.links, "symlink", "symlinks"))
}

func (BrokenSymlinks) Observe(t reconcile.Target) (reconcile.Observation, error) {
	if !t.Versioned() {
		return brokenSymlinksState{unversioned: true}, nil
	}
	files, err := trackedFiles(t.Repo.Path)
	if err != nil {
		return nil, err
	}

	state := brokenSymlinksState{}
	for _, rel := range files {
		full := filepath.Join(t.Repo.Path, rel)
		info, err := os.Lstat(full)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		state.links++

		// Stat follows the link; Lstat above proved it is one. A relative
		// target resolves against the link's own directory, which is what
		// Stat on the full path already does — computing it by hand is how a
		// checker reports a working link as broken.
		if _, err := os.Stat(full); err == nil {
			continue
		}
		target, err := os.Readlink(full)
		if err != nil {
			target = "unreadable"
		}
		state.found = append(state.found, brokenLink{path: rel, target: target})
	}
	sort.Slice(state.found, func(i, j int) bool { return state.found[i].path < state.found[j].path })
	return state, nil
}

func (BrokenSymlinks) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(brokenSymlinksState)
	if !ok {
		return nil, fmt.Errorf("broken-symlinks: unexpected observation %T", observed)
	}
	if state.unversioned {
		return nil, nil
	}

	changes := make([]reconcile.Change, 0, len(state.found))
	for _, l := range state.found {
		changes = append(changes, reconcile.Change{
			Item: l.path, Verdict: reconcile.Undeclared, Repair: reconcile.ByHand,
			Detail:   fmt.Sprintf("tracked link %s points at %s, which does not exist; expected a link that resolves", l.path, l.target),
			Observed: l.target,
		})
	}
	return changes, nil
}
