package dies

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
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
//
// Every finding is ByHand. Which side of a conflict was wanted is exactly the
// judgment a machine cannot make.
type ConflictMarkers struct{ reconcile.ReportOnly }

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
	return fmt.Sprintf("%s tracked, no conflict markers", reconcile.Plural(s.scanned, "file", "files"))
}

func (ConflictMarkers) Observe(ctx context.Context, t reconcile.Target) (reconcile.Observation, error) {
	if !t.Versioned() {
		return conflictMarkersState{unversioned: true}, nil
	}
	files, err := trackedFiles(ctx, t.Repo.Path)
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
		if line := fileMarkerLine(full, filepath.Ext(rel) == ".md"); line > 0 {
			state.found = append(state.found, conflictedFile{path: rel, line: line})
		}
	}
	sort.Slice(state.found, func(i, j int) bool { return state.found[i].path < state.found[j].path })
	return state, nil
}

// binarySniffSize is how much of a file's start is read to decide whether it
// is binary, the same amount git reads to answer that question.
const binarySniffSize = 8000

// fileMarkerLine returns the 1-indexed line of the first conflict marker in the
// file at path, or 0 where it has none, is binary, or cannot be read.
//
// The file is streamed through one buffer of binarySniffSize bytes rather than
// read whole, so a walk running across many repos at once holds that buffer
// per worker instead of each repo's largest tracked file.
func fileMarkerLine(path string, markdown bool) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, binarySniffSize)
	// A file with a NUL byte near its start is binary, and a byte sequence
	// matching a marker inside a PNG is not a merge conflict.
	head, _ := r.Peek(binarySniffSize)
	if bytes.IndexByte(head, 0) >= 0 {
		return 0
	}
	return firstMarkerLine(r, markdown)
}

// firstMarkerLine returns the 1-indexed line of the first conflict marker, or 0.
//
// skipFences ignores anything inside a markdown fenced block, and it is not a
// heuristic. git writes conflict markers into a file's content; it cannot write
// them into a fence, because a fence is the author declaring the lines inside
// it to be an example. Without this, every document explaining how to resolve a
// conflict reports as one. A die that reports such a document on every run
// becomes a die that nobody reads.
//
// The cost is a real conflict inside a fenced code block, which this misses. It
// takes two people editing the same example in a markdown file, and being wrong
// about that is better than being wrong about every conflict document.
//
// A line longer than the reader's buffer arrives in several fragments. Only the
// first is tested, because every marker and fence is decided by how its line
// starts, and a line that long is never the bare separator.
func firstMarkerLine(r io.Reader, skipFences bool) int {
	br := bufio.NewReader(r)
	inFence := false
	line := 0
	continued := false
	for {
		fragment, isPrefix, err := br.ReadLine()
		if err != nil {
			return 0
		}
		starts := !continued
		continued = isPrefix
		if !starts {
			continue
		}
		line++
		if skipFences && (bytes.HasPrefix(fragment, []byte("```")) || bytes.HasPrefix(fragment, []byte("~~~"))) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if isMarker(bytes.TrimSuffix(fragment, []byte("\r"))) {
			return line
		}
	}
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
			Detail:   fmt.Sprintf("%s has a merge conflict marker at line %d; expected none", f.path, f.line),
			Observed: fmt.Sprintf("line %d", f.line),
		})
	}
	return changes, nil
}
