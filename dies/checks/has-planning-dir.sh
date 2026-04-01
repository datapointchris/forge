#!/bin/bash
# Check: does the repo have a .planning directory?

if [ ! -d .planning ]; then
  echo "missing .planning/"
  exit 1
fi

files=$(find .planning -type f | wc -l)
echo ".planning/ ($files files)"
