package dies

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/datapointchris/forge/v7/reconcile"
)

// DefaultBranch renames a repo's master branch to main, locally, on the remote,
// and as the GitHub default.
//
// Scoped to repos that still have a `master`. A repo whose default branch is
// something else entirely is not drift — this die renames one name to another
// and has nothing to say about a repo that chose a third.
//
// The preconditions are the reason this needed a verdict rather than an exit
// code. The script exited 1 — FAIL — for "has uncommitted changes, skipping",
// so a repo in an entirely ordinary state was reported as a failure, and a
// portfolio sweep was mostly red for reasons that were nobody's fault. They are
// ByHand now: real drift that apply must not touch until a person deals with
// the working tree.
type DefaultBranch struct{}

func (DefaultBranch) Name() string { return "default-branch" }

func (DefaultBranch) Description() string {
	return "Rename master to main — local branch, remote, and the GitHub default. Refuses while the working tree is dirty or commits are unpushed."
}

func (DefaultBranch) Tags() []string {
	return []string{"git", "branches", "maintenance"}
}

type defaultBranchState struct {
	hasMaster       bool
	hasMain         bool
	dirty           bool
	unpushed        int
	remoteHasMain   bool
	remoteHasMaster bool
	repo            ghRepo
	unreachable     string
	unversioned     bool
}

func (s defaultBranchState) Summary() string {
	if s.unversioned {
		return "not a git repo"
	}
	if s.hasMain && !s.hasMaster {
		return "on main"
	}
	if !s.hasMaster {
		return "no master branch to rename"
	}
	return "master renamed"
}

func (DefaultBranch) Observe(t reconcile.Target) (reconcile.Observation, error) {
	// Without this the two branchExists calls below both answer false and the
	// row reads "no master branch to rename", which is true of a directory that
	// has no branches at all and says nothing about why.
	if !t.Versioned() {
		return defaultBranchState{unversioned: true}, nil
	}

	state := defaultBranchState{}

	state.hasMaster = branchExists(t.Repo.Path, "master")
	state.hasMain = branchExists(t.Repo.Path, "main")
	if !state.hasMaster {
		return state, nil
	}

	status, err := runIn(t.Repo.Path, "git", "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	state.dirty = status != ""

	// A master with no upstream counts as nothing unpushed rather than as an
	// error: the branch has simply never been pushed, and the push below is
	// what the rename does anyway.
	if count, err := runIn(t.Repo.Path, "git", "rev-list", "--count", "origin/master..master"); err == nil {
		state.unpushed, _ = strconv.Atoi(count)
	}

	refs, err := runIn(t.Repo.Path, "git", "ls-remote", "--heads", "origin")
	if err != nil {
		state.unreachable = "could not read the remote"
		return state, nil
	}
	state.remoteHasMain = strings.Contains(refs, "refs/heads/main")
	state.remoteHasMaster = strings.Contains(refs, "refs/heads/master")

	repo, unknown, summary, err := observeGitHub(t, "master")
	if err != nil {
		return nil, err
	}
	if unknown != nil || summary != "" {
		// Renaming locally is still worth doing where GitHub cannot be reached,
		// so this records the gap rather than refusing the whole measurement.
		state.unreachable = "the GitHub default branch cannot be set from here"
	}
	state.repo = repo

	return state, nil
}

func branchExists(dir, name string) bool {
	_, err := runIn(dir, "git", "rev-parse", "--verify", "refs/heads/"+name)
	return err == nil
}

func (DefaultBranch) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(defaultBranchState)
	if !ok {
		return nil, fmt.Errorf("default-branch: unexpected observation %T", observed)
	}
	if !state.hasMaster {
		return nil, nil
	}

	change := reconcile.Change{Item: "master", Verdict: reconcile.Stale, Repair: reconcile.Automatic}

	switch {
	case state.dirty:
		change.Repair = reconcile.ByHand
		change.Detail = "would rename to main, but the working tree is dirty — commit or stash first"
	case state.unpushed > 0:
		change.Repair = reconcile.ByHand
		change.Detail = fmt.Sprintf("would rename to main, but %s unpushed — push first, or the rename strands them",
			plural(state.unpushed, "commit is", "commits are"))
	case state.hasMain:
		change.Repair = reconcile.ByHand
		change.Detail = "both master and main exist — which one is current is a person's call"
	case state.unreachable != "":
		change.Repair = reconcile.ByHand
		change.Detail = state.unreachable
	default:
		change.Detail = "rename to main, locally, on the remote, and as the GitHub default"
	}

	return []reconcile.Change{change}, nil
}

func (d DefaultBranch) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	// Re-verified live, and this is the die where that matters most: every step
	// below is hard to reverse, and the plan may have been read for a while.
	observed, err := d.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(defaultBranchState)

	switch {
	case !state.hasMaster:
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already renamed"}, nil
	case state.dirty, state.unpushed > 0, state.hasMain, state.unreachable != "":
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the repo stopped being safe to rename since the plan"}, nil
	}

	dir := t.Repo.Path
	if _, err := runIn(dir, "git", "branch", "-m", "master", "main"); err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}
	if !state.remoteHasMain {
		if _, err := runIn(dir, "git", "push", "-u", "origin", "main"); err != nil {
			return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
		}
	}
	if _, err := runIn(dir, "gh", "repo", "edit", state.repo.slug, "--default-branch", "main"); err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}
	// Deleting the old remote branch is last: until the default has moved,
	// GitHub refuses to delete the branch the default points at.
	if state.remoteHasMaster {
		if _, err := runIn(dir, "git", "push", "origin", "--delete", "master"); err != nil {
			return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
		}
	}
	if _, err := runIn(dir, "git", "remote", "set-head", "origin", "main"); err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}

	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "renamed master to main"}, nil
}
