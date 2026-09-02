package dies

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// Pyproject merges the standard [tool.*] sections into a Python repo's
// pyproject.toml.
//
// Split from the pre-commit die on purpose, and it stays split. Adopting one
// better ruff setting through the full sync means also fanning out whatever
// toolchain.yml currently pins, to every Python repo at once — coupling the
// cheap change to the expensive one is what stops it being made.
//
// The merge itself stays Python. tomlkit is the only thing in either ecosystem
// that edits TOML losslessly, and rewriting a repo's pyproject through a
// round-trip that drops its comments and key order is not a merge, it is a
// replacement. So this die is a Go shell around the script, which is the one
// place asset extraction survives the move off bash.
//
// The standard owns exactly the keys it writes, recorded as [tool.forge]
// managed. That record is what makes retraction possible: a key dropped from
// the template is removed everywhere on the next apply, because the record
// proves forge put it there — and a key absent from the record is the
// project's and is unreachable from the delete path.
//
// The record gates adoption too. A key the project already sets, to a value the
// standard disagrees with and the record does not claim, is reported as a
// conflict and left alone. It comes back ByHand, so check surfaces it and apply
// cannot reach it — only a person can say whether the project or the standard
// is right, and the prose around the key usually argues for the project.
type Pyproject struct{}

func (Pyproject) Name() string { return "pyproject" }

func (Pyproject) Description() string {
	return "Merge the standard [tool.*] sections into a Python repo's pyproject.toml, losslessly. Owns only the keys it records, and retracts them when the template drops one."
}

func (Pyproject) Tags() []string {
	return []string{"python", "pyproject", "standardization", "golden-path"}
}

// pyprojectConflict is one key the project sets, the standard disagrees with,
// and the record does not claim. Only a person can settle which value wins.
type pyprojectConflict struct {
	path     string
	project  string
	standard string
}

type pyprojectState struct {
	applicable bool
	reason     string
	patch      string
	drifted    bool
	conflicts  []pyprojectConflict
}

func (s pyprojectState) Summary() string {
	if !s.applicable {
		return s.reason
	}
	if len(s.conflicts) > 0 {
		return fmt.Sprintf("pyproject current, %s the standard disagrees with", plural(len(s.conflicts), "key", "keys"))
	}
	return "pyproject current"
}

// parseMergeOutput splits the script's report into its three parts.
//
// The scan stops at the diff rather than filtering the whole output, because a
// pyproject's own lines travel inside that diff and one of them could carry any
// prefix this looks for.
//
// A conflict record that does not split into three fields is an error rather
// than a line to skip. Dropping it would leave `conflicts` empty and the die
// would report the key converged, which turns "I could not read this" into "I
// measured this and it was fine".
func parseMergeOutput(out string) (status string, conflicts []pyprojectConflict, retracted []string, patch string, err error) {
	lines := strings.Split(out, "\n")
	status = lines[0]

	for i, line := range lines[1:] {
		if strings.HasPrefix(line, "--- ") {
			patch = strings.Join(lines[i+1:], "\n")
			break
		}
		switch {
		case strings.HasPrefix(line, "  conflict\t"):
			// path, project value, standard value — tab-separated so a value
			// holding a space still arrives whole. tomlkit escapes a tab and a
			// newline, so a rendered value cannot split a field or a row.
			fields := strings.Split(strings.TrimPrefix(line, "  conflict\t"), "\t")
			if len(fields) != 3 {
				return "", nil, nil, "", fmt.Errorf("merge reported a conflict in %d fields, want 3: %q", len(fields), line)
			}
			conflicts = append(conflicts, pyprojectConflict{path: fields[0], project: fields[1], standard: fields[2]})
		case strings.HasPrefix(line, "  retracted "):
			retracted = append(retracted, strings.TrimPrefix(line, "  retracted "))
		}
	}
	return status, conflicts, retracted, patch, nil
}

func (Pyproject) Observe(t reconcile.Target) (reconcile.Observation, error) {
	if !slices.Contains(t.Repo.Toolchain.Stacks(), "python") {
		return pyprojectState{reason: "declares no python component"}, nil
	}
	if _, err := os.Stat(t.Path("pyproject.toml")); os.IsNotExist(err) {
		return pyprojectState{reason: "no pyproject.toml"}, nil
	} else if err != nil {
		return nil, err
	}

	// --check is the read. The script prints its status word on the first line
	// and the unified diff below it, and writes nothing.
	out, err := runMergeScript(t, "--check")
	if err != nil {
		return nil, err
	}

	status, conflicts, _, patch, err := parseMergeOutput(out)
	if err != nil {
		return nil, err
	}
	switch status {
	case "current":
		return pyprojectState{applicable: true, conflicts: conflicts}, nil
	case "would-update":
		return pyprojectState{applicable: true, drifted: true, patch: patch, conflicts: conflicts}, nil
	default:
		return nil, fmt.Errorf("unexpected merge output: %q", status)
	}
}

func (Pyproject) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(pyprojectState)
	if !ok {
		return nil, fmt.Errorf("pyproject: unexpected observation %T", observed)
	}
	var changes []reconcile.Change
	if state.drifted {
		changes = append(changes, reconcile.Change{
			Item:    "pyproject.toml",
			Verdict: reconcile.Stale,
			Repair:  reconcile.Automatic,
			Detail:  "the standard [tool.*] sections have drifted",
			// The diff travels with the change so a plan across every Python repo
			// shows the template edit as it will land, which is the whole reason
			// this die was split out.
			Patch: state.patch,
		})
	}

	// ByHand, so apply cannot reach it and check is where it surfaces. Adopting
	// the standard here would overwrite a value the project chose, and the
	// comment explaining that value would survive to contradict the new one.
	for _, c := range state.conflicts {
		changes = append(changes, reconcile.Change{
			Item:     "pyproject.toml " + c.path,
			Verdict:  reconcile.Stale,
			Repair:   reconcile.ByHand,
			Detail:   "the project sets this; the standard wants " + c.standard,
			Observed: c.project,
		})
	}
	return changes, nil
}

func (p Pyproject) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	out, err := runMergeScript(t)
	if err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}

	status, conflicts, retracted, _, err := parseMergeOutput(out)
	if err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}
	switch status {
	case "current":
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	case "updated":
		// Neither a retraction nor a conflict is ever silent. The first names
		// what was removed; the second names a key this run deliberately left
		// alone, which would otherwise read as merged.
		notes := make([]string, 0, len(retracted)+len(conflicts))
		for _, path := range retracted {
			notes = append(notes, "retracted "+path)
		}
		for _, c := range conflicts {
			notes = append(notes, "left "+c.path+" at "+c.project)
		}
		message := "merged"
		if len(notes) > 0 {
			message = "merged — " + strings.Join(notes, "; ")
		}
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: message}, nil
	default:
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: "unexpected merge output: " + status}, nil
	}
}

// runMergeScript materializes the script and the template and runs them.
//
// Extraction survives here and nowhere else. `uv run --no-project` is passed
// because without it uv builds the repo being edited just to run a stdlib
// script, and the build chatter on stderr is long enough to swallow the one
// word the caller reads back.
func runMergeScript(t reconcile.Target, extraArgs ...string) (string, error) {
	dir, err := os.MkdirTemp("", "forge-pyproject-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	script, err := materialize(t.Assets.PreCommit, "scripts/merge_pyproject_tools.py", dir)
	if err != nil {
		return "", err
	}
	standard, err := materialize(t.Assets.PreCommit, "configs/pyproject-tools.toml", dir)
	if err != nil {
		return "", err
	}

	args := append([]string{"run", "--no-project", "--with", "tomlkit", "python", script}, extraArgs...)
	args = append(args, standard, "pyproject.toml")
	return runIn(t.Repo.Path, "uv", args...)
}

func materialize(fsys fs.FS, name, dir string) (string, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", fmt.Errorf("reading embedded %s: %w", name, err)
	}

	path := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
