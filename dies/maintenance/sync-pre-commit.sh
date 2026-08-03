#!/bin/bash
# Synchronize .pre-commit-config.yaml and tool configs from standard templates.
# Composes standard blocks for the repo's declared components and preserves
# project-specific hooks via # > custom:POSITION markers.

# Resolve asset directories: FORGE_DATA_DIR (embedded binary) or script-relative (dev)
if [ -n "${FORGE_DATA_DIR:-}" ]; then
  blocks_dir="$FORGE_DATA_DIR/pre-commit/blocks"
  configs_dir="$FORGE_DATA_DIR/pre-commit/configs"
  scripts_dir="$FORGE_DATA_DIR/pre-commit/scripts"
else
  forge_root="$(cd "$(dirname "$0")/../.." && pwd)"
  blocks_dir="$forge_root/pre-commit/blocks"
  configs_dir="$forge_root/pre-commit/configs"
  scripts_dir="$forge_root/pre-commit/scripts"
fi

if [ ! -d "$blocks_dir" ]; then
  echo "ERROR: blocks directory not found: $blocks_dir"
  exit 1
fi

# --- Declared stacks ---
# Read from the registry, never probed. Detection was wrong in both directions:
# `-f frontend/package.json` missed every repo whose frontend is in web/ or
# client/, and it cannot say which directory a stack lives in.
if ! stacks=$(forge precommit stacks 2>&1); then
  echo "$stacks"
  exit 1
fi
declared=$(echo "$stacks" | tr '\n' ',' | sed 's/,$//')

# --- Generate pre-commit config ---
gen_output=$(forge precommit generate 2>&1)
gen_rc=$?
if [ $gen_rc -ne 0 ]; then
  echo "$gen_output"
  exit 1
fi

# --- Deploy tool configs ---
configs_deployed=""

# Markdownlint — always deploy
if [ ! -f .markdownlint.json ] || ! diff -q "$configs_dir/markdownlint.json" .markdownlint.json > /dev/null 2>&1; then
  cp "$configs_dir/markdownlint.json" .markdownlint.json
  configs_deployed="$configs_deployed markdownlint"
fi

# EditorConfig — always deploy, because the shell block is generic and shfmt
# reads its style from here. A repo that got the hook without this file would
# be formatted at shfmt's default of tab indentation.
if [ ! -f .editorconfig ] || ! diff -q "$configs_dir/editorconfig.ini" .editorconfig > /dev/null 2>&1; then
  cp "$configs_dir/editorconfig.ini" .editorconfig
  configs_deployed="$configs_deployed editorconfig"
fi

# Go lint config
if echo "$declared" | grep -q "go"; then
  if [ ! -f .golangci.yml ] || ! diff -q "$configs_dir/golangci.yml" .golangci.yml > /dev/null 2>&1; then
    cp "$configs_dir/golangci.yml" .golangci.yml
    configs_deployed="$configs_deployed golangci"
  fi
fi

# Vue/Frontend configs
if echo "$declared" | grep -q "vue"; then
  if [ ! -f .prettierrc.json ] || ! diff -q "$configs_dir/prettierrc.json" .prettierrc.json > /dev/null 2>&1; then
    cp "$configs_dir/prettierrc.json" .prettierrc.json
    configs_deployed="$configs_deployed prettier"
  fi
fi

# SQL lint config — deployed wherever a dialect is declared, since the block
# passes the dialect but the ruleset lives in the config.
if echo "$declared" | grep -q "sql"; then
  if [ ! -f .sqlfluff ] || ! diff -q "$configs_dir/sqlfluff.ini" .sqlfluff > /dev/null 2>&1; then
    cp "$configs_dir/sqlfluff.ini" .sqlfluff
    configs_deployed="$configs_deployed sqlfluff"
  fi
fi

# Python tool configs — merge standard sections into pyproject.toml
if echo "$declared" | grep -q "python" && [ -f pyproject.toml ]; then
  merge_script="$scripts_dir/merge_pyproject_tools.py"
  standard_tools="$configs_dir/pyproject-tools.toml"
  # --no-project or uv builds the repo being edited just to run a stdlib script
  if merge_out=$(uv run --no-project --with tomlkit python "$merge_script" "$standard_tools" pyproject.toml 2>/dev/null); then
    [ "$merge_out" = "updated" ] && configs_deployed="$configs_deployed pyproject"
  else
    configs_deployed="$configs_deployed WARN:pyproject-merge-failed"
  fi
fi

# --- Install hooks ---
# Every stage any block uses has to be installed, or its hooks silently never
# run — a config that declares prepare-commit-msg without the git hook in place
# looks correct and does nothing.
if command -v pre-commit &> /dev/null; then
  pre-commit install --install-hooks -t pre-commit -t commit-msg -t prepare-commit-msg -t post-commit 2>&1 | tail -1
fi

# --- Summary ---
if [ "$gen_output" = "no changes" ] && [ -z "$configs_deployed" ]; then
  echo "no changes"
  exit 2
fi

summary="synced: ${declared:-generic-only}"
[ -n "$configs_deployed" ] && summary="$summary |$configs_deployed"
echo "$summary"
