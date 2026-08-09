package dies

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datapointchris/forge/v5/reconcile"
)

// Pyproject merges the standard [tool.*] sections into a Python repo's
// pyproject.toml.
//
// Split from the pre-commit die on purpose, and it stays split. Adopting one
// better ruff setting through the full sync means also fanning out whatever
// toolchain.yml currently pins, to every Python repo at once — so the cheap
// change stopped being made and the settings drifted instead.
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
type Pyproject struct{}

func (Pyproject) Name() string { return "pyproject" }

func (Pyproject) Description() string {
	return "Merge the standard [tool.*] sections into a Python repo's pyproject.toml, losslessly. Owns only the keys it records, and retracts them when the template drops one."
}

func (Pyproject) Tags() []string {
	return []string{"python", "pyproject", "standardization", "golden-path"}
}

type pyprojectState struct {
	applicable bool
	reason     string
	patch      string
	drifted    bool
}

func (s pyprojectState) Summary() string {
	if !s.applicable {
		return s.reason
	}
	return "pyproject current"
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

	status, detail, _ := strings.Cut(out, "\n")
	switch status {
	case "current":
		return pyprojectState{applicable: true}, nil
	case "would-update":
		return pyprojectState{applicable: true, drifted: true, patch: detail}, nil
	default:
		return nil, fmt.Errorf("unexpected merge output: %q", status)
	}
}

func (Pyproject) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(pyprojectState)
	if !ok {
		return nil, fmt.Errorf("pyproject: unexpected observation %T", observed)
	}
	if !state.drifted {
		return nil, nil
	}

	return []reconcile.Change{{
		Item:    "pyproject.toml",
		Verdict: reconcile.Stale,
		Repair:  reconcile.Automatic,
		Detail:  "the standard [tool.*] sections have drifted",
		// The diff travels with the change so a plan across every Python repo
		// shows the template edit as it will land, which is the whole reason
		// this die was split out.
		Patch: state.patch,
	}}, nil
}

func (p Pyproject) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	out, err := runMergeScript(t)
	if err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}

	status, detail, _ := strings.Cut(out, "\n")
	switch status {
	case "current":
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	case "updated":
		message := "merged"
		// A retraction is never silent: the script names the keys it removed.
		if detail != "" {
			message = "merged — " + strings.ReplaceAll(strings.TrimSpace(detail), "\n", "; ")
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
