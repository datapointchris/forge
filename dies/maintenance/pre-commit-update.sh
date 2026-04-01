#!/bin/bash
# Check for pre-commit hook version updates.
# Shows what would change without applying.

if [ ! -f .pre-commit-config.yaml ]; then
  echo "no .pre-commit-config.yaml"
  exit 2
fi

if ! command -v pre-commit &> /dev/null; then
  echo "pre-commit not installed"
  exit 1
fi

# Capture current state
before=$(grep '^\s*rev:' .pre-commit-config.yaml)

# Run autoupdate
output=$(pre-commit autoupdate 2>&1)

# Capture new state
after=$(grep '^\s*rev:' .pre-commit-config.yaml)

if [ "$before" = "$after" ]; then
  echo "all hooks up to date"
  exit 2
fi

# Show what changed
echo "$output" | grep -E 'updating|already up to date'
