#!/bin/bash
# Check: does the repo have a .gitignore that isn't bloated with generated cruft?

if [ ! -f .gitignore ]; then
  echo "missing .gitignore"
  exit 1
fi

lines=$(wc -l < .gitignore)

# Flag generated gitignores that haven't been cleaned up
if grep -q "toptal.com/developers/gitignore" .gitignore 2>/dev/null; then
  echo "FAIL: generated gitignore ($lines lines) — run sync-gitignore"
  exit 1
fi

# Flag duplicate .planning entries
planning_count=$(grep -c '^\.planning' .gitignore 2>/dev/null || echo 0)
if [ "$planning_count" -gt 1 ]; then
  echo "WARN: $planning_count duplicate .planning entries"
  exit 1
fi

# Flag missing .planning entry
if ! grep -q '^\.planning$' .gitignore 2>/dev/null; then
  echo "WARN: missing .planning entry"
  exit 1
fi

echo "clean ($lines lines)"
