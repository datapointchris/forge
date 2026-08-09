#!/bin/bash
# Make a merged PR read as the work, not as "Merge pull request #1 from ...".
#
# GitHub's default is merge_commit_title=MERGE_MESSAGE, which produces exactly
# that line and buries the PR title one line down. Setting title=PR_TITLE and
# message=PR_BODY makes the merge commit the best commit in the history rather
# than the worst: `git log --oneline` shows the change, and the full body — the
# decisions, what was rejected, what to look at — lands in the log where it
# outlives the PR page.
#
# This is what makes `--merge` usable, which is the point. Squashing throws away
# the commits inside the PR; rebasing throws away the branch, and the branch is
# what shows an initiative as one piece of work. Neither is wanted here, so the
# merge commit had to stop being noise instead.
#
# delete_branch_on_merge because a merged branch has no further use and
# remembering --delete-branch is a step that gets skipped.
#
# Non-GitHub remotes are skipped rather than failed. A repo hosted on Bitbucket
# is not drift — it is a repo whose provider has its own settings and its own
# tool, and the remote is the only thing that actually knows which.
#
# Settings, not files: this leaves the working tree untouched and commits
# nothing. FORGE_CHECK reports what it would change instead of changing it.

set -euo pipefail

WANT_TITLE="PR_TITLE"
WANT_MESSAGE="PR_BODY"

command -v gh >/dev/null 2>&1 || {
  echo "gh is not installed"
  exit 2
}

remote=$(git config --get remote.origin.url 2>/dev/null || true)
if [ -z "$remote" ]; then
  echo "no origin remote"
  exit 2
fi

case "$remote" in
  *github.com*) ;;
  *)
    echo "not a GitHub remote"
    exit 2
    ;;
esac

# Ask gh for the slug rather than parsing the URL: it handles ssh, https, and
# the .git suffix, and it is already the thing that has to agree with the API.
if ! slug=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null); then
  echo "gh could not identify this repo (check 'gh auth status')"
  exit 2
fi

if ! current=$(gh api "repos/$slug" \
  --jq '"\(.merge_commit_title) \(.merge_commit_message) \(.delete_branch_on_merge)"' 2>/dev/null); then
  echo "could not read settings for $slug"
  exit 2
fi

read -r title message delete_branch <<<"$current"

changes=()
[ "$title" != "$WANT_TITLE" ] && changes+=("title: $title → $WANT_TITLE")
[ "$message" != "$WANT_MESSAGE" ] && changes+=("message: $message → $WANT_MESSAGE")
[ "$delete_branch" != "true" ] && changes+=("delete_branch_on_merge: $delete_branch → true")

if [ ${#changes[@]} -eq 0 ]; then
  echo "merge settings current"
  exit 2
fi

if [ -n "${FORGE_CHECK:-}" ]; then
  echo "would set ${changes[*]}"
  exit 0
fi

gh api -X PATCH "repos/$slug" \
  -f merge_commit_title="$WANT_TITLE" \
  -f merge_commit_message="$WANT_MESSAGE" \
  -F delete_branch_on_merge=true \
  --silent

echo "set ${changes[*]}"
