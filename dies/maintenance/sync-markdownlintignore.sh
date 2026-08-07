#!/bin/bash
# Ensure the standard .markdownlintignore entries are present.
#
# Append-only like sync-gitignore: the contents are legitimately per-repo
# (logsift ignores PLANNING.md, theme ignores its analysis docs), so a plain
# file deploy would destroy them.
#
# FORGE_CHECK reports what it would add instead of adding it.

set -euo pipefail

# go-semantic-release rewrites CHANGELOG.md on every release, undoing --fix.
universal=("CHANGELOG.md")

if ! forge precommit stacks >/dev/null 2>&1; then
  echo "not in the repo registry"
  exit 2
fi

creating=""
if [ ! -f .markdownlintignore ]; then
  if [ -n "${FORGE_CHECK:-}" ]; then
    echo "would create with: ${universal[*]}"
    exit 0
  fi
  : >.markdownlintignore
  creating=1
fi

missing=()
for entry in "${universal[@]}"; do
  if ! grep -qxF "$entry" .markdownlintignore; then
    missing+=("$entry")
  fi
done

if [ ${#missing[@]} -eq 0 ]; then
  echo "markdownlintignore current"
  exit 2
fi

if [ -n "${FORGE_CHECK:-}" ]; then
  echo "would add: ${missing[*]}"
  exit 0
fi

# shellcheck disable=SC1003 # valid sed 'append a newline' command, not a bad escape
sed -i -e '$a\' .markdownlintignore

printf '%s\n' "${missing[@]}" >>.markdownlintignore

if [ -n "$creating" ]; then
  echo "created with: ${missing[*]}"
else
  echo "added: ${missing[*]}"
fi
