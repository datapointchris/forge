#!/bin/bash
# Check: does the repo have a pre-commit config?

if [ ! -f .pre-commit-config.yaml ]; then
  echo "missing .pre-commit-config.yaml"
  exit 1
fi

hooks=$(grep -c '^\s*-\s*id:' .pre-commit-config.yaml 2>/dev/null || echo 0)
echo "$hooks hooks configured"
