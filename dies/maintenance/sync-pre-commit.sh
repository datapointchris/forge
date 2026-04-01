#!/bin/bash
# Synchronize .pre-commit-config.yaml and tool configs from standard templates.
# Detects the repo's tech stack and composes applicable blocks.
# Project-specific hooks below "# === PROJECT HOOKS ===" are preserved.

# Resolve forge repo root relative to this script (lives in dies/maintenance/)
forge_root="$(cd "$(dirname "$0")/../.." && pwd)"
blocks_dir="$forge_root/pre-commit/blocks"
configs_dir="$forge_root/pre-commit/configs"

if [ ! -d "$blocks_dir" ]; then
  echo "ERROR: blocks directory not found: $blocks_dir"
  exit 1
fi

# --- Detect tech stack ---
has_python=false
has_go=false
has_vue=false
has_docker=false
has_actions=false
has_terraform=false

{ [ -f pyproject.toml ] || [ -f setup.py ] || [ -f requirements.txt ] || [ -f Pipfile ]; } && has_python=true
[ -f go.mod ] && has_go=true
[ -f frontend/package.json ] && has_vue=true
{ [ -f Dockerfile ] || compgen -G "docker-compose*.yml" > /dev/null 2>&1; } && has_docker=true
[ -d .github/workflows ] && has_actions=true
{ compgen -G "*.tf" > /dev/null 2>&1 || [ -d terraform ]; } && has_terraform=true

# --- Preserve project hooks ---
project_hooks=""
if [ -f .pre-commit-config.yaml ]; then
  project_hooks=$(sed -n '/^# === PROJECT HOOKS ===/,$p' .pre-commit-config.yaml)
fi

# --- Generate pre-commit config ---
config=".pre-commit-config.yaml"

cat > "$config" << 'HEADER'
fail_fast: true
default_stages: [pre-commit]
repos:
HEADER

for block in "$blocks_dir"/[0-9]*.yml; do
  name=$(basename "$block" .yml)
  category="${name#*-}"

  case "$category" in
    python-*) $has_python || continue ;;
    go)       $has_go || continue ;;
    vue)      $has_vue || continue ;;
    docker)   $has_docker || continue ;;
    github-actions) $has_actions || continue ;;
    terraform)      $has_terraform || continue ;;
  esac

  cat "$block" >> "$config"
  printf "\n" >> "$config"
done

hook_path="$HOME/.claude/hooks/prepare-commit-msg"
if [ -x "$hook_path" ]; then
  cat >> "$config" << EOF
  # Strip AI branding from commits
  - repo: local
    hooks:
      - id: prepare-commit-msg
        name: Strip AI branding from commits
        entry: $hook_path
        language: system
        always_run: true
        stages: [prepare-commit-msg]
EOF
fi

if [ -n "$project_hooks" ]; then
  printf "\n" >> "$config"
  echo "$project_hooks" >> "$config"
fi

# --- Deploy tool configs ---
configs_deployed=""

# Markdownlint — always deploy
if [ ! -f .markdownlint.json ] || ! diff -q "$configs_dir/markdownlint.json" .markdownlint.json > /dev/null 2>&1; then
  cp "$configs_dir/markdownlint.json" .markdownlint.json
  configs_deployed="$configs_deployed markdownlint"
fi

# Go lint config
if $has_go; then
  if [ ! -f .golangci.yml ] || ! diff -q "$configs_dir/golangci.yml" .golangci.yml > /dev/null 2>&1; then
    cp "$configs_dir/golangci.yml" .golangci.yml
    configs_deployed="$configs_deployed golangci"
  fi
fi

# Python tool configs — merge standard sections into pyproject.toml
if $has_python && [ -f pyproject.toml ]; then
  merge_script="$forge_root/pre-commit/scripts/merge-pyproject-tools.py"
  standard_tools="$configs_dir/pyproject-tools.toml"
  if uv run --with tomlkit python "$merge_script" "$standard_tools" pyproject.toml 2>/dev/null; then
    configs_deployed="$configs_deployed pyproject"
  else
    configs_deployed="$configs_deployed WARN:pyproject-merge-failed"
  fi
fi

# --- Install hooks ---
if command -v pre-commit &> /dev/null; then
  pre-commit install --install-hooks -t pre-commit -t commit-msg -t prepare-commit-msg 2>&1 | tail -1
fi

# --- Summary ---
detected=""
$has_python && detected="$detected python"
$has_go && detected="$detected go"
$has_vue && detected="$detected vue"
$has_docker && detected="$detected docker"
$has_actions && detected="$detected actions"
$has_terraform && detected="$detected terraform"
[ -n "$project_hooks" ] && detected="$detected +project-hooks"
[ -n "$configs_deployed" ] && detected="$detected |$configs_deployed"

echo "synced:${detected:- generic-only}"
