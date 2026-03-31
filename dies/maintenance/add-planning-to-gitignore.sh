#!/bin/bash

pattern=".planning"

if grep -qxF "$pattern" .gitignore 2>/dev/null; then
  echo "already present, skipping"
  exit 2
fi

echo "$pattern" >>.gitignore
git add .gitignore
git commit -m "chore: add .planning to gitignore"
