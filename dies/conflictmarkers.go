package dies

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/datapointchris/forge/reconcile"
)

// ConflictMarkers reports a tracked file carrying an unresolved merge conflict.
//
// pre-commit's own check-merge-conflict catches this at commit time, and this
// is not redundant with it: a hook only runs where it is installed and only on
// what is staged. A marker reaches a repo through a commit made with
// --no-verify, a merge resolved in an editor that saved the wrong hunk, or a
// repo that has not had the hook deployed yet — and then it sits there,
// because nothing looks again.
type ConflictMarkers struct{}

func (ConflictMarkers) Name() string { return "conflict-markers" }

func (ConflictMarkers) Description() string {
	return "Report a tracked file holding an unresolved merge conflict. Never writes: which side was wanted is the repo's to say."
}

func (ConflictMarkers) Tags() []string { return []string{"git", "hygiene", "scorecard"} }

// isMarker reports whether a line is one of the three git writes into a
// conflicted file.
//
// Anchored to the start of the line, because these sequences appear mid-line in
// real content — a diff pasted into a README, a heredoc, this file. The two
// that name a ref carry a trailing space; the separator is the whole line, so
// it is compared rather than prefixed, which keeps a row of equals signs used
// as a heading underline from reading as a conflict.
func isMarker(line []byte) bool {
	return bytes.HasPrefix(line, []byte("<<<<<<< ")) ||
		bytes.Equal(line, []byte("=======")) ||
		bytes.HasPrefix(line, []byte(">>>>>>> "))
}

type conflictedFile struct {
	path string
	line int
}

type conflictMarkersState struct {
	unversioned bool
	scanned     int
	found       []conflictedFile
}

func (s conflictMarkersState) Summary() string {
	if s.unversioned {
		return "not a git checkout"
	}
	return fmt.Sprintf("%s tracked, no conflict markers", plural(s.scanned, "file", "files"))
}

func (ConflictMarkers) Observe(t reconcile.Target) (reconcile.Observation, error) {
	if !t.Versioned() {
		return conflictMarkersState{unversioned: true}, nil
	}
	files, err := trackedFiles(t.Repo.Path)
	if err != nil {
		return nil, err
	}

	state := conflictMarkersState{scanned: len(files)}
	for _, rel := range files {
		full := filepath.Join(t.Repo.Path, rel)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		// A file with a NUL byte is binary, and a byte sequence matching a
		// marker inside a PNG is not a merge conflict.
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		if line := firstMarkerLine(data, filepath.Ext(rel) == ".md"); line > 0 {
			state.found = append(state.found, conflictedFile{path: rel, line: line})
		}
	}
	sort.Slice(state.found, func(i, j int) bool { return state.found[i].path < state.found[j].path })
	return state, nil
}

// firstMarkerLine returns the 1-indexed line of the first conflict marker, or 0.
//
// skipFences ignores anything inside a markdown fenced block, and it is not a
// heuristic. git writes conflict markers into a file's content; it cannot write
// them into a fence, because a fence is the author declaring the lines inside
// it to be an example. Without this, every document explaining how to resolve a
// conflict reports as one — `terminal-library/workflows/git-merge-conflicts.md`
// is the live case, and a die that reports it every run is a die nobody reads.
//
// The cost is a real conflict inside a fenced code block, which this misses. It
// takes two people editing the same example in a markdown file, and being wrong
// about that is better than being wrong about every conflict document.
func firstMarkerLine(data []byte, skipFences bool) int {
	inFence := false
	for i, line := range bytes.Split(data, []byte("\n")) {
		if skipFences && (bytes.HasPrefix(line, []byte("```")) || bytes.HasPrefix(line, []byte("~~~"))) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// bytes.Split returns slices into data, so appending a newline here to
		// match a marker would write into the shared backing array.
		if isMarker(bytes.TrimSuffix(line, []byte("\r"))) {
			return i + 1
		}
	}
	return 0
}

func (ConflictMarkers) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(conflictMarkersState)
	if !ok {
		return nil, fmt.Errorf("conflict-markers: unexpected observation %T", observed)
	}
	if state.unversioned {
		return nil, nil
	}

	changes := make([]reconcile.Change, 0, len(state.found))
	for _, f := range state.found {
		changes = append(changes, reconcile.Change{
			Item: f.path, Verdict: reconcile.Undeclared, Repair: reconcile.ByHand,
			Detail:   "an unresolved merge conflict is committed here",
			Observed: fmt.Sprintf("line %d", f.line),
		})
	}
	return changes, nil
}

// Perform is unreachable: nothing this die reports is Actionable. Which side of
// a conflict was wanted is exactly the judgment a machine cannot make.
func (ConflictMarkers) Perform(_ reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	return reconcile.Outcome{
		Change:  change,
		Status:  reconcile.Refused,
		Message: "which side of the conflict was wanted is the repo's to say",
	}, nil
}
