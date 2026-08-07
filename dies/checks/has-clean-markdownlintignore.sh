#!/bin/bash
# Check: is a generated CHANGELOG.md kept out of markdownlint's way?
#
# The drift this catches is narrow on purpose. A CHANGELOG.md that exists and is
# not ignored is broken in every repo: semantic-release rewrites it on each
# release, undoing --fix, which resurfaces as a rebase conflict the next time a
# local commit lands on top of a release commit.
#
# A repo with no CHANGELOG.md is a SKIP, not a failure. The entry is preventive
# there, and how widely to seed it is an open question — failing 50-odd repos
# that will never cut a release is how a scorecard trains skimming.
#
# Literal match, because that is what sync-markdownlintignore writes and every
# repo carrying the entry today spells it that way.

if [ ! -f CHANGELOG.md ]; then
  echo "no CHANGELOG.md"
  exit 2
fi

if [ ! -f .markdownlintignore ]; then
  echo "FAIL: CHANGELOG.md present, no .markdownlintignore — run sync-markdownlintignore"
  exit 1
fi

if ! grep -qxF 'CHANGELOG.md' .markdownlintignore; then
  echo "FAIL: CHANGELOG.md not ignored — run sync-markdownlintignore"
  exit 1
fi

echo "CHANGELOG.md ignored"
