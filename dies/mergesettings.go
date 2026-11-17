package dies

import (
	"context"
	"fmt"
	"slices"
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
// away the commits inside the PR. Rebasing lands every commit under a new sha.
// That breaks any record keyed on a commit's sha, because main then holds the
// commit under a sha the branch it was written on never had. Neither is wanted,
// so the merge commit had to stop being noise instead.
//
// So squash and rebase are both off, and merge is the one button. A button
// that must never be pressed is better removed than documented. GitHub accepts
// `allow_squash_merge=false` and `allow_rebase_merge=false` together.
//
// Settings, not files: this leaves the working tree untouched and commits
// nothing.
type MergeSettings struct{}

func (MergeSettings) Name() string { return "merge-settings" }

func (MergeSettings) Description() string {
	return "Set the GitHub merge commit title to the PR title and its body to the PR body, turn the squash and rebase buttons off, and delete the branch on merge, so a merged PR reads as the work and every commit keeps its sha."
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
	if anyUnmeasured(s.changes) {
		return "merge settings not checked"
	}
	return "merge settings current"
}

func (MergeSettings) Observe(ctx context.Context, t reconcile.Target) (reconcile.Observation, error) {
	repo, unknown, summary, err := observeGitHub(ctx, t, "merge settings")
	if err != nil || summary != "" || unknown != nil {
		return mergeSettingsState{changes: unknown, summary: summary}, err
	}

	out, err := runIn(ctx, t.Repo.Path, "gh", "api", "repos/"+repo.slug,
		"--jq", `"\(.merge_commit_title) \(.merge_commit_message) \(.delete_branch_on_merge) \(.allow_merge_commit) \(.allow_squash_merge) \(.allow_rebase_merge)"`)
	if err != nil {
		return mergeSettingsState{
			repo:    repo,
			changes: []reconcile.Change{unknownChange("merge settings", "could not read settings for "+repo.slug)},
		}, nil
	}

	fields := strings.Fields(out)
	if len(fields) != 6 {
		return mergeSettingsState{
			repo:    repo,
			changes: []reconcile.Change{unknownChange("merge settings", "unreadable settings for "+repo.slug)},
		}, nil
	}
	// GitHub answers a token that can only read the repository without its
	// merge settings, and jq renders each one it left out as null. Applying
	// on such a token would be refused besides.
	if slices.Contains(fields, "null") {
		return mergeSettingsState{
			repo:    repo,
			changes: []reconcile.Change{unknownChange("merge settings", "the token reading "+repo.slug+" cannot see its merge settings")},
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
	if fields[5] != "false" {
		changes = append(changes, settingChange("allow_rebase_merge", fields[5], "false"))
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

// Perform writes all six fields at once rather than one per Change.
//
// The API rejects the title and message fields when merge commits are off, so
// they have to travel in the same request as allow_merge_commit. A per-field
// write would fail on exactly the repos that need it most; the re-read below
// is what keeps the batching honest, reporting Skipped once the first Change
// has already carried its siblings.
func (m MergeSettings) Perform(ctx context.Context, t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	observed, err := m.Observe(ctx, t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(mergeSettingsState)
	if !hasItem(state.changes, change.Item) {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already set"}, nil
	}

	if _, err := runIn(ctx, t.Repo.Path, "gh", "api", "-X", "PATCH", "repos/"+state.repo.slug,
		"-F", "allow_merge_commit=true",
		"-f", "merge_commit_title="+wantMergeTitle,
		"-f", "merge_commit_message="+wantMergeMessage,
		"-F", "delete_branch_on_merge=true",
		"-F", "allow_squash_merge=false",
		"-F", "allow_rebase_merge=false",
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
