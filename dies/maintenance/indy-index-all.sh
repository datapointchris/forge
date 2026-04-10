#!/bin/bash
# Incrementally re-index the current repo in the indy semantic search store.
# Run via: forge dies run indy-index-all
# Only re-embeds files whose content hash has changed since last index.

if ! command -v indy &>/dev/null; then
  echo "indy not installed — skipping"
  exit 2
fi

output=$(indy index "$(pwd)" 2>&1)
exit_code=$?

echo "$output"

[ $exit_code -ne 0 ] && exit 1

# Exit SKIP when no files needed re-embedding
if echo "$output" | grep -q "updated 0,"; then
  exit 2
fi

exit 0
