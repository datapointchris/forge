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
#
# FORGE_CHECK (set by `forge dies run --check`) prints the diff each repo would
# take instead of writing it. A template edit fans out to every Python repo at
# once, so seeing that before it lands is the difference between a review and an
# archaeology session.

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

check_flag=()
if [ -n "${FORGE_CHECK:-}" ]; then
  check_flag=(--check)
fi

if ! merge_out=$(uv run --no-project --with tomlkit python \
  "$scripts_dir/merge_pyproject_tools.py" "${check_flag[@]}" \
  "$configs_dir/pyproject-tools.toml" pyproject.toml 2> "$merge_err"); then
  echo "merge failed: $(tail -3 "$merge_err")"
  exit 1
fi

# First line is the status word, the rest is detail worth showing — retracted
# keys, and the diff under --check. A retraction is never silent.
status=$(printf '%s\n' "$merge_out" | head -1)
detail=$(printf '%s\n' "$merge_out" | tail -n +2)

case "$status" in
current)
  echo "pyproject current"
  exit 2
  ;;
updated)
  echo "pyproject updated"
  ;;
would-update)
  echo "pyproject would change"
  ;;
*)
  echo "unexpected merge output: $status"
  exit 1
  ;;
esac

if [ -n "$detail" ]; then
  printf '%s\n' "$detail"
fi
