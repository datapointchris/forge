#!/bin/bash
# Show recent git commits (last 2 weeks)

commits=$(git log --oneline --since="2 weeks ago" --no-merges -10 2>/dev/null)

if [ -z "$commits" ]; then
  echo "no recent commits"
  exit 2
fi

count=$(echo "$commits" | wc -l | tr -d ' ')
echo "$count commits in last 2 weeks"
echo "$commits"
