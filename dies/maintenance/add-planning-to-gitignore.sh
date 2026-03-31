#!/bin/bash
# Add .planning/ to .gitignore if not already present, then commit.
# Intended to run via: forge exec -f ./add-planning-to-gitignore.sh

pattern=".planning/"

if grep -qxF "$pattern" .gitignore 2>/dev/null; then
    echo "already present, skipping"
    exit 0
fi

echo "$pattern" >> .gitignore
git add .gitignore
git commit -m "chore: add .planning to gitignore"
