#!/bin/bash

pattern=".planning"

if grep -qxF "$pattern" .gitignore 2>/dev/null; then
  echo "already present, skipping"
  exit 2
fi

# Ensure trailing newline before appending so the entry lands on its own line
# shellcheck disable=SC1003 # This is a valid sed 'append newline' command, not an unescaped quote
sed -i -e '$a\' .gitignore 2>/dev/null
echo "$pattern" >>.gitignore
git add .gitignore
git commit -m "chore: add .planning to gitignore"
