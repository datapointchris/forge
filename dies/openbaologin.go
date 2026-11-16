package dies

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/datapointchris/forge/reconcile"
)

const (
	// openBaoLoginPath is where the script lands in a repo, the path a workflow step runs.
	openBaoLoginPath = "release/with-openbao-login"
	// openBaoLoginAsset is the template within the embedded ci/scripts tree.
	openBaoLoginAsset = "with-openbao-login"
)

// OpenBaoLogin writes the one script a CI job logs in to OpenBao through.
//
// The login runs before any install, so no package manager can carry it, and a
// copy pasted into each repo is a copy each repo edits. So forge holds the one
// template and writes it whole. A repo opts in by declaring openbao_login.
//
// A copy at the path without the marker is reported rather than replaced,
// because forge cannot tell a local fix from a stale paste.
type OpenBaoLogin struct{}

func (OpenBaoLogin) Name() string { return "openbao-login" }

func (OpenBaoLogin) Description() string {
	return "Write " + openBaoLoginPath + ", the one script a CI job logs in to OpenBao through, into every repo declaring openbao_login, and remove it where the declaration is withdrawn."
}

func (OpenBaoLogin) Tags() []string {
	return []string{"openbao", "secrets", "release", "workflow", "actions", "standardization"}
}

type openBaoLoginState struct {
	declared bool
	// handWritten is a file at the path without forge's marker.
	handWritten bool
	file        generatedFile
}

func (s openBaoLoginState) Summary() string {
	if !s.declared {
		return "declares no openbao_login"
	}
	return openBaoLoginPath + " current"
}

func (OpenBaoLogin) Observe(t reconcile.Target) (reconcile.Observation, error) {
	state := openBaoLoginState{declared: t.Repo.Toolchain != nil && t.Repo.Toolchain.OpenBaoLogin}
	if handWritten(t.Repo.Path, openBaoLoginPath) {
		state.handWritten = true
		return state, nil
	}

	want := ""
	if state.declared {
		content, err := fs.ReadFile(t.Assets.CIScripts, openBaoLoginAsset)
		if err != nil {
			return nil, fmt.Errorf("reading the embedded %s: %w", openBaoLoginAsset, err)
		}
		want = string(content)
	}
	state.file = readGenerated(t.Repo.Path, openBaoLoginPath, want)
	return state, nil
}

func (OpenBaoLogin) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(openBaoLoginState)
	if !ok {
		return nil, fmt.Errorf("openbao-login: unexpected observation %T", observed)
	}
	if state.handWritten {
		if !state.declared {
			return nil, nil
		}
		return []reconcile.Change{blockerWithFix(openBaoLoginPath,
			fmt.Sprintf("%s has no %s line; expected the login forge writes", openBaoLoginPath, managedMark),
			fmt.Sprintf("delete %s, then run `forge repos apply openbao-login`", openBaoLoginPath))}, nil
	}
	if change, ok := state.file.change(); ok {
		return []reconcile.Change{change}, nil
	}
	return nil, nil
}

func (d OpenBaoLogin) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	observed, err := d.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state, ok := observed.(openBaoLoginState)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("openbao-login: unexpected observation %T", observed)
	}

	switch file := state.file; {
	case state.handWritten:
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "a hand-written copy appeared since the plan measured it"}, nil
	case file.retracting():
		if err := removeGenerated(t.Repo.Path, file); err != nil {
			return reconcile.Outcome{}, err
		}
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "removed"}, nil
	case !file.wanted() || file.matches():
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	}

	// Chmod as well, because WriteFile keeps the mode of a file that exists, and a
	// workflow step runs the script by path.
	path := filepath.Join(t.Repo.Path, openBaoLoginPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return reconcile.Outcome{}, err
	}
	if err := os.WriteFile(path, []byte(state.file.want), 0o755); err != nil {
		return reconcile.Outcome{}, err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "written"}, nil
}
