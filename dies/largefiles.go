package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/datapointchris/forge/reconcile"
)

// LargeFiles reports a tracked file big enough that it probably was not meant
// to be in git.
//
// One of four checks that moved here from the audit skill in 2026-08-12. They
// are mechanical, per-repo and deterministic, and they fail
// standards/README.md's *generalizes beyond one repo* test — which is what
// makes them dies rather than a standard something grades against. A model
// reading a repo to judge it against a guideline is the wrong instrument for
// "is any file over a megabyte".
type LargeFiles struct{}

func (LargeFiles) Name() string { return "large-files" }

func (LargeFiles) Description() string {
	return "Report a tracked file large enough that it was probably committed by accident. Never writes: whether a big file belongs is the repo's to say."
}

func (LargeFiles) Tags() []string { return []string{"git", "hygiene", "scorecard"} }

// largeEnoughToQuestion is where a tracked file stops looking deliberate.
//
// A threshold rather than a measurement of what the fleet carries today: the
// biggest file in any repo changes on every commit, and a rule written against
// it would be wrong immediately. A megabyte is far above source, a lockfile or
// a generated config, and far below a dataset or a binary — so anything over it
// is either deliberate and worth a word, or a mistake.
const largeEnoughToQuestion = 1 << 20

type largeFile struct {
	path  string
	bytes int64
}

type largeFilesState struct {
	unversioned bool
	scanned     int
	found       []largeFile
}

func (s largeFilesState) Summary() string {
	if s.unversioned {
		return "not a git checkout"
	}
	return fmt.Sprintf("%s tracked, none over %s", plural(s.scanned, "file", "files"), humanBytes(largeEnoughToQuestion))
}

func (LargeFiles) Observe(t reconcile.Target) (reconcile.Observation, error) {
	if !t.Versioned() {
		return largeFilesState{unversioned: true}, nil
	}
	files, err := trackedFiles(t.Repo.Path)
	if err != nil {
		return nil, err
	}

	state := largeFilesState{scanned: len(files)}
	for _, rel := range files {
		// Lstat, not Stat: a symlink to something huge is not a huge file in
		// this repo, and following it would report the target's size against
		// the wrong repo. Broken symlinks are their own die.
		info, err := os.Lstat(filepath.Join(t.Repo.Path, rel))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() >= largeEnoughToQuestion {
			state.found = append(state.found, largeFile{path: rel, bytes: info.Size()})
		}
	}
	sort.Slice(state.found, func(i, j int) bool { return state.found[i].bytes > state.found[j].bytes })
	return state, nil
}

func (LargeFiles) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(largeFilesState)
	if !ok {
		return nil, fmt.Errorf("large-files: unexpected observation %T", observed)
	}
	if state.unversioned {
		return nil, nil
	}

	changes := make([]reconcile.Change, 0, len(state.found))
	for _, f := range state.found {
		changes = append(changes, reconcile.Change{
			Item: f.path, Verdict: reconcile.Undeclared, Repair: reconcile.ByHand,
			Detail:   "large enough to have been committed by accident; git keeps it forever",
			Observed: humanBytes(f.bytes),
		})
	}
	return changes, nil
}

// Perform is unreachable: nothing this die reports is Actionable. Deleting a
// file from a repo is never forge's call, and rewriting history to remove one
// is not a thing any die should do.
func (LargeFiles) Perform(_ reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	return reconcile.Outcome{
		Change:  change,
		Status:  reconcile.Refused,
		Message: "whether a large file belongs is the repo's to say",
	}, nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
