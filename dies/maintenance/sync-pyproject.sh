#!/bin/bash
# Merge the standard [tool.*] sections into a Python repo's pyproject.toml.
#
# Split out of sync-pre-commit, which also regenerates .pre-commit-config.yaml
# from toolchain.yml. That coupling made "adopt a better ruff rule" indivisible
# from a toolchain rollout across every repo at once, so the cheap change never
# got made and the settings drifted instead.
#
# Writes nothing but pyproject.toml. Running it across the portfolio doubles as
# the drift report: OK means the repo had drifted, SKIP means it was current.

if [ -n "${FORGE_DATA_DIR:-}" ]; then
  configs_dir="$FORGE_DATA_DIR/pre-commit/configs"
  scripts_dir="$FORGE_DATA_DIR/pre-commit/scripts"
else
  forge_root="$(cd "$(dirname "$0")/../.." && pwd)"
  configs_dir="$forge_root/pre-commit/configs"
  scripts_dir="$forge_root/pre-commit/scripts"
fi

# Declared, never probed — the same source the generators read. A repo missing
# from the registry is out of scope, not a failure.
if ! stacks=$(forge precommit stacks 2>&1); then
  echo "not in the repo registry"
  exit 2
fi

if ! echo "$stacks" | grep -qx "python"; then
  echo "declares no python component"
  exit 2
fi

if [ ! -f pyproject.toml ]; then
  echo "no pyproject.toml"
  exit 2
fi

# --no-project or uv builds the repo being edited, and its build chatter lands
# on stderr — merged into stdout that would drown the one word this reads back.
# Stderr goes to a file rather than /dev/null so a real failure still reports.
merge_err=$(mktemp)
trap 'rm -f "$merge_err"' EXIT

if ! merge_out=$(uv run --no-project --with tomlkit python \
  "$scripts_dir/merge_pyproject_tools.py" \
  "$configs_dir/pyproject-tools.toml" pyproject.toml 2> "$merge_err"); then
  echo "merge failed: $(tail -3 "$merge_err")"
  exit 1
fi

if [ "$merge_out" != "updated" ]; then
  echo "pyproject current"
  exit 2
fi

echo "pyproject updated"
