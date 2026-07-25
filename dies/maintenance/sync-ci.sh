#!/bin/bash
# Generate .github/workflows/validate.yml — the baseline checks every repo
# should run — from standard CI blocks. Repo-specific pipelines stay in their
# own workflow files; this one is additive and callable via workflow_call so a
# release workflow can gate on it.
#
# Aborts rather than overwriting a validate.yml that has no forge-toolchain
# header, on the assumption it was hand-written.

# --- Detect tech stack ---
detected=""

{ [ -f pyproject.toml ] || [ -f setup.py ] || [ -f requirements.txt ] || [ -f Pipfile ]; } && detected="$detected,python"
[ -f go.mod ] && detected="$detected,go"
[ -f frontend/package.json ] && detected="$detected,vue"

detected="${detected#,}"

# Nothing forge knows how to validate — a docs or config repo.
if [ -z "$detected" ]; then
  echo "no supported stack detected"
  exit 2
fi

output=$(forge ci generate --detected "$detected" 2>&1)
status=$?

if [ $status -ne 0 ]; then
  echo "$output"
  exit 1
fi

if [ "$output" = "no changes" ]; then
  echo "validate.yml current ($detected)"
  exit 2
fi

echo "$output ($detected)"
exit 0
