#!/bin/bash
# Ensure the standard .gitignore entries are present, per declared stack.
#
# has-clean-gitignore has told operators to "run sync-gitignore" since it was
# written, and there was no such die. This is it.
#
# Append-only, never a rewrite. A .gitignore is legitimately per-repo — the
# entries a project adds for its own artifacts are not drift and must survive.
# So this asserts that a baseline set is PRESENT and leaves everything else
# alone, which is the ensure-line-present operation repo-structure.md § Known
# gaps describes. It is also why this cannot be a plain file deploy like
# .editorconfig or .markdownlint.json.
#
# Deliberately omitted, because the tool that creates them writes a .gitignore
# containing '*' into the directory itself: .venv (uv), htmlcov (coverage.py),
# .pytest_cache, .ruff_cache, .mypy_cache. Listing them here would be dead
# weight that reads as necessary. Only artifacts with no such protection are
# below — the ones `task clean` removes.
#
# Does not commit. sync-pre-commit and sync-ci both leave the repo dirty for the
# operator to review, and a fleet-wide run that committed to 70 repos would put
# the review after the irretractable step. add-planning-to-gitignore commits
# because it predates that convention.
#
# FORGE_CHECK (set by `forge dies run --check`) reports the lines it would add
# instead of adding them.

set -euo pipefail

# Every repo, whatever it holds. has-clean-gitignore fails a repo missing this.
universal=(".planning")

# Declared, never probed — the same source the generators read. A repo missing
# from the registry is out of scope, not a failure.
if ! stacks=$(forge precommit stacks 2>&1); then
  echo "not in the repo registry"
  exit 2
fi

wanted=("${universal[@]}")

if echo "$stacks" | grep -qx "python"; then
  # dist and *.egg-info are build output; .coverage and coverage.xml are the
  # coverage data files, which unlike htmlcov/ are bare files and self-ignore
  # nothing.
  wanted+=(".coverage" "coverage.xml" "dist/" "*.egg-info/")
fi

if [ ! -f .gitignore ]; then
  echo "no .gitignore"
  exit 2
fi

missing=()
for entry in "${wanted[@]}"; do
  # -x so 'dist/' does not match a repo's existing 'somedist/thing', and -F so
  # the '*' in '*.egg-info/' is a literal rather than a pattern.
  if ! grep -qxF "$entry" .gitignore; then
    missing+=("$entry")
  fi
done

if [ ${#missing[@]} -eq 0 ]; then
  echo "gitignore current"
  exit 2
fi

if [ -n "${FORGE_CHECK:-}" ]; then
  echo "would add: ${missing[*]}"
  exit 0
fi

# Guarantee the file ends in a newline before appending, or the first entry
# lands glued to whatever the last line was.
# shellcheck disable=SC1003 # valid sed 'append a newline' command, not a bad escape
sed -i -e '$a\' .gitignore

printf '%s\n' "${missing[@]}" >>.gitignore

echo "added: ${missing[*]}"
