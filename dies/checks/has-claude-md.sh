#!/bin/bash
# Check: does the repo have a CLAUDE.md?

if [ ! -f CLAUDE.md ]; then
  echo "missing CLAUDE.md"
  exit 1
fi

lines=$(wc -l < CLAUDE.md)
echo "CLAUDE.md ($lines lines)"
