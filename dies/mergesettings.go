package dies

import (
	"fmt"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// MergeSettings makes a merged PR read as the work, not as "Merge pull request
// #1 from ...".
//
// GitHub's default is merge_commit_title=MERGE_MESSAGE, which produces exactly
// that line and buries the PR title underneath it. Setting title=PR_TITLE and
// message=PR_BODY makes the merge commit the best commit in the history rather
// than the worst: `git log --oneline` shows the change, and the full body — the
// decisions, what was rejected, what to look at — lands in the log where it
// outlives the PR page.
//
// This is what makes `--merge` usable, which is the point. Squashing throws
// away the commits inside the PR; rebasing throws away the branch, and the
// branch is what shows an initiative as one piece of work. Neither is wanted,
// so the merge commit had to stop being noise instead.
//
// Squash is off for that reason. `git-workflow.md` § "A PR lands as a merge
// commit, not a squash or a rebase" already forbids pressing it, and a button
// that must never be pressed is better removed than documented.
//
// Rebase is left enabled and is not asserted on. GitHub accepts merge alone —
// `allow_squash_merge=false` and `allow_rebase_merge=false` together are a
// legal request, measured against a live repo — so this is a choice rather
// than a limit.
//
// Settings, not files: this leaves the working tree untouched and commits
// nothing.
type MergeSettings struct{}

func (MergeSettings) Name() string { return "merge-settings" }

func (MergeSettings) Description() string {
	return "Set the GitHub merge commit title to the PR title and its body to the PR body, turn the squash button off, and delete the branch on merge, so a merged PR reads as the work."
}

func (MergeSettings) Tags() []string {
	return []string{"github", "pr", "merge", "standardization"}
}

const (
	wantMergeTitle   = "PR_TITLE"
	wantMergeMessage = "PR_BODY"
)

type mergeSettingsState struct {
	repo    ghRepo
	changes []reconcile.Change
	summary string
}

func (s mergeSettingsState) Summary() string {
	if s.summary != "" {
		return s.summary
	}
	return "merge settings current"
}

func (MergeSettings) Observe(t reconcile.Target) (reconcile.Observation, error) {
	repo, unknown, summary, err := observeGitHub(t, "merge settings")
	if err != nil || summary != "" || unknown != nil {
		return mergeSettingsState{changes: unknown, summary: summary}, err
	}

	out, err := runIn(t.Repo.Path, "gh", "api", "repos/"+repo.slug,
		"--jq", `"\(.merge_commit_title) \(.merge_commit_message) \(.delete_branch_on_merge) \(.allow_merge_commit) \(.allow_squash_merge)"`)
	if err != nil {
		return mergeSettingsState{
			repo:    repo,
			changes: []reconcile.Change{unknownChange("merge settings", "could not read settings for "+repo.slug)},
		}, nil
	}

	fields := strings.Fields(out)
	if len(fields) != 5 {
		return mergeSettingsState{
			repo:    repo,
			changes: []reconcile.Change{unknownChange("merge settings", "unreadable settings for "+repo.slug)},
		}, nil
	}

	var changes []reconcile.Change
	if fields[0] != wantMergeTitle {
		changes = append(changes, settingChange("merge_commit_title", fields[0], wantMergeTitle))
	}
	if fields[1] != wantMergeMessage {
		changes = append(changes, settingChange("merge_commit_message", fields[1], wantMergeMessage))
	}
	if fields[2] != "true" {
		changes = append(changes, settingChange("delete_branch_on_merge", fields[2], "true"))
	}
	// GitHub rejects the two title/message fields outright (HTTP 422) when merge
	// commits are disabled, so this is not an extra opinion bolted on — it is
	// what makes the rest of the request legal. Two repos failed exactly this
	// way.
	if fields[3] != "true" {
		changes = append(changes, settingChange("allow_merge_commit", fields[3], "true"))
	}
	if fields[4] != "false" {
		changes = append(changes, settingChange("allow_squash_merge", fields[4], "false"))
	}

	return mergeSettingsState{repo: repo, changes: changes}, nil
}

func (MergeSettings) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(mergeSettingsState)
	if !ok {
		return nil, fmt.Errorf("merge-settings: unexpected observation %T", observed)
	}
	return state.changes, nil
}

// Perform writes all five fields at once rather than one per Change.
//
// The API rejects the title and message fields when merge commits are off, so
// they have to travel in the same request as allow_merge_commit. A per-field
// write would fail on exactly the repos that need it most; the re-read below
// is what keeps the batching honest, reporting Skipped once the first Change
// has already carried its siblings.
func (m MergeSettings) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := m.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(mergeSettingsState)
	if !hasItem(state.changes, change.Item) {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already set"}, nil
	}

	if _, err := runIn(t.Repo.Path, "gh", "api", "-X", "PATCH", "repos/"+state.repo.slug,
		"-F", "allow_merge_commit=true",
		"-f", "merge_commit_title="+wantMergeTitle,
		"-f", "merge_commit_message="+wantMergeMessage,
		"-F", "delete_branch_on_merge=true",
		"-F", "allow_squash_merge=false",
		"--silent"); err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}

	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "set"}, nil
}

func hasItem(changes []reconcile.Change, item string) bool {
	for _, change := range changes {
		if change.Item == item {
			return true
		}
	}
	return false
}
