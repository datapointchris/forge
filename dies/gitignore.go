package dies

import (
	"fmt"
	"slices"
	"strings"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

// generatedMarker is the giveaway left by a gitignore.io / toptal download.
// Those files run to hundreds of lines of every language the site knows about,
// and pruning one is a judgment about what the repo actually builds — so it is
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
// The one removal is a retired entry: a line this die itself asserted in every
// repo, matched whole and literally. That is no repo's own line, and removing it
// here is what reaches every clone rather than only the ones edited by hand.
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

// retiredIgnores are lines this die removes wherever it finds them.
var retiredIgnores = []string{
	// Retired, because a repo's status lives in the sync base rather than in a
	// .planning directory.
	".planning",
}

// Deliberately omitted, because the tool that creates them writes a .gitignore
// containing '*' into the directory itself: .venv (uv), htmlcov (coverage.py),
// .pytest_cache, .ruff_cache, .mypy_cache. Listing them would be dead weight
// that reads as necessary.
var pythonIgnores = []string{
	// Bytecode Python writes beside each module it imports, wherever PYTHONPYCACHEPREFIX is unset.
	"__pycache__/",
	// The coverage data file is a bare file, so unlike htmlcov/ it self-ignores nothing.
	".coverage",
	// The coverage report the CI run writes.
	"coverage.xml",
	// Build output.
	"dist/",
	"*.egg-info/",
}

// rustIgnores is one line per declared Rust directory, for the target/ cargo
// builds into beside that directory's Cargo.toml. A workspace in crates/ builds
// into crates/target, which a root /target does not reach.
//
// The root one is spelled "/target" rather than "target/" because that is the
// exact line `cargo new` writes, and matching it is what keeps this die
// idempotent on a repo that started that way. A different spelling of the same
// rule is a second line the duplicate check cannot see, since it compares
// strings.
//
// Unlike Python's .venv, target/ does not self-ignore — cargo puts the entry in
// the repo's root .gitignore at `cargo new` time and writes nothing inside the
// directory. So a repo whose .gitignore was created any other way has no entry
// at all.
func rustIgnores(components []config.Component) []string {
	var entries []string
	for _, component := range components {
		if component.Stack != "rust" {
			continue
		}
		line := "/target"
		if dir := strings.Trim(component.Dir, "/"); dir != "" && dir != "." {
			line = "/" + dir + "/target"
		}
		if !slices.Contains(entries, line) {
			entries = append(entries, line)
		}
	}
	return entries
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

	var entries []string
	if slices.Contains(t.Repo.Toolchain.Stacks(), "python") {
		entries = append(entries, pythonIgnores...)
	}
	if t.Repo.Toolchain != nil {
		entries = append(entries, rustIgnores(t.Repo.Toolchain.Components)...)
	}

	var changes []reconcile.Change
	for _, entry := range entries {
		switch count := state.count(entry); {
		case count == 0:
			changes = append(changes, reconcile.Change{
				Item:    entry,
				Verdict: reconcile.Missing,
				Repair:  reconcile.Automatic,
				Detail:  "missing .gitignore entry: " + entry,
			})
		case count > 1:
			// Append-only means this cannot be repaired here: removing a line is
			// exactly the operation that would take a repo's own entries with it
			// if the matching were ever wrong.
			changes = append(changes, reconcile.Change{
				Item:     entry,
				Verdict:  reconcile.Stale,
				Repair:   reconcile.ByHand,
				Detail:   fmt.Sprintf(".gitignore lists %s %d times; expected once", entry, count),
				Observed: fmt.Sprintf("%d occurrences", count),
			})
		}
	}

	for _, entry := range retiredIgnores {
		if count := state.count(entry); count > 0 {
			changes = append(changes, reconcile.Change{
				Item:     entry,
				Verdict:  reconcile.Stale,
				Repair:   reconcile.Automatic,
				Detail:   fmt.Sprintf(".gitignore lists %s, which forge retired; expected no such entry", entry),
				Observed: plural(count, "occurrence", "occurrences"),
			})
		}
	}

	if state.generated {
		changes = append(changes, reconcile.Change{
			Item:     ".gitignore",
			Verdict:  reconcile.Stale,
			Repair:   reconcile.ByHand,
			Detail:   fmt.Sprintf(".gitignore is a %d-line gitignore.io download; expected only the entries this repo builds", len(state.lines)),
			Observed: fmt.Sprintf("%d lines", len(state.lines)),
		})
	}

	return changes, nil
}

func (g Gitignore) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	// Re-read rather than trust the plan: Observe ran before the plan was
	// printed, an earlier change in this same batch may have created the file,
	// and the operator may have read the plan for a while before applying it.
	observed, err := g.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}

	if slices.Contains(retiredIgnores, change.Item) {
		if observed.(gitignoreState).count(change.Item) == 0 {
			return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already gone"}, nil
		}
		if err := removeLine(t.Path(".gitignore"), change.Item); err != nil {
			return reconcile.Outcome{}, err
		}
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "removed"}, nil
	}

	if observed.(gitignoreState).count(change.Item) > 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already present"}, nil
	}

	if err := appendBlock(t.Path(".gitignore"), []string{change.Item}); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "added"}, nil
}
