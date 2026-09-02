package dies

import (
	"fmt"
	"slices"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// generatedMarker is the giveaway left by a gitignore.io / toptal download.
// Those files run to hundreds of lines of every language the site knows about,
// and pruning one is a judgement about what the repo actually builds — so it is
// reported, never rewritten.
const generatedMarker = "toptal.com/developers/gitignore"

// Gitignore asserts that the standard entries are present, and reports what
// only a person can settle.
//
// Append-only, never a rewrite. A .gitignore is legitimately per-repo — the
// entries a project adds for its own artifacts are not drift and must survive —
// so this asserts a baseline is PRESENT and leaves everything else alone. It is
// also why this cannot be a plain file deploy like .editorconfig.
//
// Absent, it creates. That does not weaken the rule: append-only exists to
// protect a repo's own entries, and a file that does not exist has none.
// Skipping instead made a new repo the one case the golden path did not cover,
// which is where it is needed most.
//
// This is has-clean-gitignore and sync-gitignore in one. The check die ended by
// printing "run sync-gitignore", which is a die whose output is an instruction
// to run another die — the same desired-state list written twice, in two files,
// free to disagree.
type Gitignore struct{}

func (Gitignore) Name() string { return "gitignore" }

func (Gitignore) Description() string {
	return "Ensure the standard .gitignore entries are present for the declared stack, and report a generated file or duplicate entries that only a person can settle."
}

func (Gitignore) Tags() []string {
	return []string{"gitignore", "python", "standardization", "golden-path"}
}

// wanted is one entry the standard asserts, with why it is there.
//
// The reason travels into the plan rather than sitting in a comment here: a
// reader looking at forty repos about to gain a line deserves to know what the
// line is for without opening the source.
type wanted struct {
	line   string
	reason string
}

// universal applies whatever the repo holds.
var universalIgnores = []wanted{
	{".planning", "planning docs are local-only and synced out of band, never committed"},
}

// Deliberately omitted, because the tool that creates them writes a .gitignore
// containing '*' into the directory itself: .venv (uv), htmlcov (coverage.py),
// .pytest_cache, .ruff_cache, .mypy_cache. Listing them would be dead weight
// that reads as necessary.
var pythonIgnores = []wanted{
	{".coverage", "coverage data file — a bare file, so unlike htmlcov/ it self-ignores nothing"},
	{"coverage.xml", "coverage report, written by the CI run"},
	{"dist/", "build output"},
	{"*.egg-info/", "build output"},
}

// Spelled "/target" rather than "target/" because that is the exact line
// `cargo new` writes, and matching it is what keeps this die idempotent on a
// repo that started that way. A different spelling of the same rule is a second
// line the duplicate check cannot see, since it compares strings.
//
// Unlike Python's .venv, target/ does not self-ignore — cargo puts the entry in
// the repo's root .gitignore at `cargo new` time and writes nothing inside the
// directory. Verified 2026-08-12. So a repo whose .gitignore was created any
// other way has no entry at all.
var rustIgnores = []wanted{
	{"/target", "cargo build output"},
}

type gitignoreState struct {
	lineFile
	generated bool
}

func (s gitignoreState) Summary() string {
	if !s.exists {
		return "no .gitignore"
	}
	return fmt.Sprintf("gitignore current (%s)", plural(len(s.lines), "line", "lines"))
}

func (Gitignore) Observe(t reconcile.Target) (reconcile.Observation, error) {
	file, err := readLineFile(t.Path(".gitignore"))
	if err != nil {
		return nil, err
	}

	return gitignoreState{
		lineFile:  file,
		generated: slices.ContainsFunc(file.lines, func(line string) bool { return strings.Contains(line, generatedMarker) }),
	}, nil
}

func (Gitignore) Diff(t reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(gitignoreState)
	if !ok {
		return nil, fmt.Errorf("gitignore: unexpected observation %T", observed)
	}

	entries := slices.Clone(universalIgnores)
	if slices.Contains(t.Repo.Toolchain.Stacks(), "python") {
		entries = append(entries, pythonIgnores...)
	}
	if slices.Contains(t.Repo.Toolchain.Stacks(), "rust") {
		entries = append(entries, rustIgnores...)
	}

	var changes []reconcile.Change
	for _, entry := range entries {
		switch count := state.count(entry.line); {
		case count == 0:
			changes = append(changes, reconcile.Change{
				Item:    entry.line,
				Verdict: reconcile.Missing,
				Repair:  reconcile.Automatic,
				Detail:  entry.reason,
			})
		case count > 1:
			// Append-only means this cannot be repaired here: removing a line is
			// exactly the operation that would take a repo's own entries with it
			// if the matching were ever wrong.
			changes = append(changes, reconcile.Change{
				Item:     entry.line,
				Verdict:  reconcile.Stale,
				Repair:   reconcile.ByHand,
				Detail:   "listed more than once",
				Observed: fmt.Sprintf("%d occurrences", count),
			})
		}
	}

	if state.generated {
		changes = append(changes, reconcile.Change{
			Item:     ".gitignore",
			Verdict:  reconcile.Stale,
			Repair:   reconcile.ByHand,
			Detail:   "downloaded from gitignore.io — prune it to what this repo builds",
			Observed: fmt.Sprintf("%d lines", len(state.lines)),
		})
	}

	return changes, nil
}

func (g Gitignore) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{
			Change:  change,
			Status:  reconcile.Refused,
			Message: "only a person can settle this one",
		}, nil
	}

	// Re-read rather than trust the plan: Observe ran before the plan was
	// printed, an earlier change in this same batch may have created the file,
	// and the operator may have read the plan for a while before applying it.
	observed, err := g.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	if observed.(gitignoreState).count(change.Item) > 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already present"}, nil
	}

	if err := appendBlock(t.Path(".gitignore"), []string{change.Item}); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "added"}, nil
}
