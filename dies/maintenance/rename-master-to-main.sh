#!/bin/bash
# Rename master branch to main (local + remote + GitHub default)
# Handles partial states from previous attempts idempotently

# Skip if not on master
has_master=$(git rev-parse --verify refs/heads/master 2>/dev/null && echo yes || echo no)
has_main=$(git rev-parse --verify refs/heads/main 2>/dev/null && echo yes || echo no)

if [ "$has_master" = "no" ] && [ "$has_main" = "yes" ]; then
  echo "already on main"
  exit 2
fi

if [ "$has_master" = "no" ] && [ "$has_main" = "no" ]; then
  echo "no master or main branch found"
  exit 1
fi

# Check for uncommitted changes
if [ -n "$(git status --porcelain)" ]; then
  echo "has uncommitted changes, skipping"
  exit 1
fi

# Check for unpushed commits
unpushed=$(git rev-list origin/master..master --count 2>/dev/null || echo 0)
if [ "$unpushed" -gt 0 ]; then
  echo "has $unpushed unpushed commits, skipping"
  exit 1
fi

# Rename local branch
if [ "$has_master" = "yes" ] && [ "$has_main" = "no" ]; then
  git branch -m master main || { echo "local rename failed"; exit 1; }
fi

# Check remote state
remote_refs=$(git ls-remote --heads origin 2>/dev/null)
remote_has_main=$(echo "$remote_refs" | grep -q refs/heads/main && echo yes || echo no)
remote_has_master=$(echo "$remote_refs" | grep -q refs/heads/master && echo yes || echo no)

# Push main if not on remote
if [ "$remote_has_main" = "no" ]; then
  git push -u origin main || { echo "push failed"; exit 1; }
fi

# Set GitHub default branch
repo_name=$(basename "$(git rev-parse --show-toplevel)")
owner=$(git remote get-url origin | sed -n 's|.*[:/]\([^/]*\)/[^/]*\.git$|\1|p; s|.*[:/]\([^/]*\)/[^/]*$|\1|p')
gh repo edit "$owner/$repo_name" --default-branch main || { echo "set GitHub default failed"; exit 1; }

# Delete remote master
if [ "$remote_has_master" = "yes" ]; then
  git push origin --delete master 2>/dev/null
fi

# Update local ref
git remote set-head origin main

echo "renamed master → main"
