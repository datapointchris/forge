package dies

import (
	"context"
	"fmt"
	"sort"

	"github.com/datapointchris/forge/reconcile"
)

// LargeFiles reports a tracked file big enough that it probably was not meant
// to be in git.
//
// Mechanical, per-repo and deterministic, and it fails standards/README.md's
// *generalizes beyond one repo* test — which is what makes it a die rather
// than a standard something grades against. A model reading a repo to judge it
// against a guideline is the wrong instrument for "is any file over a
// megabyte".
//
// Every finding is ByHand. Deleting a file from a repo is never forge's call,
// and rewriting history to remove one is not a thing any die should do.
type LargeFiles struct{ reconcile.ReportOnly }

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
	return fmt.Sprintf("%s tracked, none over %s", reconcile.Plural(s.scanned, "file", "files"), humanBytes(largeEnoughToQuestion))
}

func (LargeFiles) Observe(ctx context.Context, t reconcile.Target) (reconcile.Observation, error) {
	if !t.Versioned() {
		return largeFilesState{unversioned: true}, nil
	}
	files, err := trackedFiles(ctx, t)
	if err != nil {
		return nil, err
	}

	state := largeFilesState{scanned: len(files)}
	for _, f := range files {
		// A symlink to something huge is not a huge file in this repo, so only
		// a regular file counts. Broken symlinks are their own die.
		if f.info == nil || !f.info.Mode().IsRegular() {
			continue
		}
		if f.info.Size() >= largeEnoughToQuestion {
			state.found = append(state.found, largeFile{path: f.path, bytes: f.info.Size()})
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
			Detail:   fmt.Sprintf("%s is %s; expected every tracked file under %s", f.path, humanBytes(f.bytes), humanBytes(largeEnoughToQuestion)),
			Observed: humanBytes(f.bytes),
		})
	}
	return changes, nil
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
