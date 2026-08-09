#!/bin/bash
# Make "never force-push main" mechanical instead of a rule to remember.
#
# Rebasing a feature branch requires force-pushing it, so force-push cannot be
# banned outright — `~/dev/standards/git-workflow.md` § "A branch catches up by
# rebasing" carries the reasoning. What must never happen is a force aimed at the
# default branch, and a server-side rule is the only thing that still holds when
# the local hook is bypassed, when the push comes from another machine, or when
# the person at the keyboard is in a hurry.
#
# Deliberately the two narrowest rules and nothing else. Required reviews deadlock
# a solo repo outright — GitHub will not let you approve your own PR — and
# requiring a PR would forbid the direct-to-main commits this workflow depends on
# for small work. Force-push and deletion are the only two operations that lose
# history rather than add to it, and they are the whole target here.
#
# enforce_admins is what makes it real rather than decorative: without it the sole
# admin, which is the only account that ever pushes to these repos, bypasses every
# rule above.
#
# The branch is read from the API rather than assumed to be main, because
# rename-master-to-main.sh exists precisely because that was not always true.
#
# Settings, not files: this leaves the working tree untouched and commits nothing.
# FORGE_CHECK reports what it would change instead of changing it.

set -euo pipefail

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

if ! info=$(gh repo view --json nameWithOwner,defaultBranchRef \
  -q '"\(.nameWithOwner) \(.defaultBranchRef.name)"' 2>/dev/null); then
  echo "gh could not identify this repo (check 'gh auth status')"
  exit 2
fi

read -r slug branch <<<"$info"

if [ -z "$branch" ] || [ "$branch" = "null" ]; then
  echo "no default branch yet (empty repo)"
  exit 2
fi

# A branch with no protection 404s here, which is a state to fix rather than an
# error to report. The assignment must be overwritten inside the failure branch
# rather than with `|| echo`: gh prints API errors to stdout, so the 404 body would
# otherwise be captured and then have the fallback appended to it.
if ! current=$(gh api "repos/$slug/branches/$branch/protection" \
  --jq '"\(.allow_force_pushes.enabled) \(.allow_deletions.enabled) \(.enforce_admins.enabled)"' 2>/dev/null); then
  current="unprotected"
fi

changes=()
if [ "$current" = "unprotected" ]; then
  changes+=("$branch: unprotected → force-push and deletion blocked")
else
  read -r force_pushes deletions admins <<<"$current"
  [ "$force_pushes" != "false" ] && changes+=("allow_force_pushes: $force_pushes → false")
  [ "$deletions" != "false" ] && changes+=("allow_deletions: $deletions → false")
  [ "$admins" != "true" ] && changes+=("enforce_admins: $admins → true")
fi

if [ ${#changes[@]} -eq 0 ]; then
  echo "branch protection current on $branch"
  exit 2
fi

if [ -n "${FORGE_CHECK:-}" ]; then
  echo "would set ${changes[*]}"
  exit 0
fi

# Classic branch protection rather than a ruleset: the nulls below are how the API
# spells "configure nothing else", and a ruleset would need every unwanted rule
# named explicitly instead.
if ! gh api -X PUT "repos/$slug/branches/$branch/protection" --input - >/dev/null 2>&1 <<'JSON'; then
{
  "required_status_checks": null,
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
  echo "could not set protection on $slug ($branch) — private repos need a paid plan"
  exit 2
fi

echo "set ${changes[*]}"
