#!/bin/bash
# Synchronize .pre-commit-config.yaml and tool configs from standard templates.
# Detects the repo's tech stack, composes standard blocks, and preserves
# project-specific hooks via >>> project:POSITION / <<< project markers.

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

# --- Detect tech stack ---
detected=""

{ [ -f pyproject.toml ] || [ -f setup.py ] || [ -f requirements.txt ] || [ -f Pipfile ]; } && detected="$detected,python"
[ -f go.mod ] && detected="$detected,go"
[ -f frontend/package.json ] && detected="$detected,vue"
{ [ -f Dockerfile ] || compgen -G "docker-compose*.yml" > /dev/null 2>&1; } && detected="$detected,docker"
[ -d .github/workflows ] && detected="$detected,actions"
{ compgen -G "*.tf" > /dev/null 2>&1 || [ -d terraform ]; } && detected="$detected,terraform"

# Strip leading comma
detected="${detected#,}"

# --- Generate pre-commit config ---
gen_output=$(forge precommit generate --detected "$detected" 2>&1)
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

# Go lint config
if echo "$detected" | grep -q "go"; then
  if [ ! -f .golangci.yml ] || ! diff -q "$configs_dir/golangci.yml" .golangci.yml > /dev/null 2>&1; then
    cp "$configs_dir/golangci.yml" .golangci.yml
    configs_deployed="$configs_deployed golangci"
  fi
fi

# Vue/Frontend configs
if echo "$detected" | grep -q "vue"; then
  if [ ! -f .prettierrc.json ] || ! diff -q "$configs_dir/prettierrc.json" .prettierrc.json > /dev/null 2>&1; then
    cp "$configs_dir/prettierrc.json" .prettierrc.json
    configs_deployed="$configs_deployed prettier"
  fi
  if [ ! -f .stylelintrc.json ] || ! diff -q "$configs_dir/stylelintrc.json" .stylelintrc.json > /dev/null 2>&1; then
    cp "$configs_dir/stylelintrc.json" .stylelintrc.json
    configs_deployed="$configs_deployed stylelint"
  fi
fi

# Python tool configs — merge standard sections into pyproject.toml
if echo "$detected" | grep -q "python" && [ -f pyproject.toml ]; then
  merge_script="$scripts_dir/merge_pyproject_tools.py"
  standard_tools="$configs_dir/pyproject-tools.toml"
  if merge_out=$(uv run --with tomlkit python "$merge_script" "$standard_tools" pyproject.toml 2>/dev/null); then
    [ "$merge_out" = "updated" ] && configs_deployed="$configs_deployed pyproject"
  else
    configs_deployed="$configs_deployed WARN:pyproject-merge-failed"
  fi
fi

# --- Install hooks ---
if command -v pre-commit &> /dev/null; then
  pre-commit install --install-hooks -t pre-commit -t commit-msg 2>&1 | tail -1
fi

# --- Summary ---
if [ "$gen_output" = "no changes" ] && [ -z "$configs_deployed" ]; then
  echo "no changes"
  exit 2
fi

summary="synced: ${detected:-generic-only}"
[ -n "$configs_deployed" ] && summary="$summary |$configs_deployed"
echo "$summary"
