#!/bin/bash
# Verify forge can generate a valid pre-commit config and CI workflow for this
# repo, without writing anything. Run across the portfolio before bumping the
# toolchain manifest — a rollout is only as safe as its worst repo.
#
# Reports what would block a real sync: unmarked custom hooks (which abort
# rather than being destroyed), and an existing hand-written CI workflow.

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

problems=""
notes=""

# --- CI ---
if ci_err=$(forge ci generate --dry-run 2>&1 >"$tmp/validate.yml"); then
  if [ -s "$tmp/validate.yml" ]; then
    # A generator placeholder, not a GitHub Actions expression — ${{ ... }} is
    # ordinary workflow syntax and must not read as a failed substitution.
    if grep -qE '(^|[^$])\{\{' "$tmp/validate.yml"; then
      problems="${problems}ci: unexpanded placeholder; "
    fi
    if command -v actionlint > /dev/null 2>&1; then
      mkdir -p "$tmp/lint/.github/workflows"
      cp "$tmp/validate.yml" "$tmp/lint/.github/workflows/validate.yml"
      (cd "$tmp/lint" && git init -q 2> /dev/null)
      if ! al=$(cd "$tmp/lint" && actionlint .github/workflows/validate.yml 2>&1); then
        problems="${problems}ci: actionlint: $(echo "$al" | head -1); "
      fi
    fi
    jobs=$(grep -cE '^  [a-z0-9-]+:$' "$tmp/validate.yml")
    notes="${notes}ci ${jobs} jobs; "
  else
    problems="${problems}ci: generated nothing; "
  fi
else
  case "$ci_err" in
    *"no components with a CI block"*|*"declares no toolchain.components"*|*"not in the repo registry"*)
      notes="${notes}ci n/a; " ;;
    *) problems="${problems}ci: ${ci_err#Error: }; " ;;
  esac
fi

# An existing workflow forge would refuse to overwrite. Not a failure — it
# means the custom pipeline has to be reconciled before a sync can land.
for existing in ci validate; do
  f=".github/workflows/${existing}.yml"
  [ -f "$f" ] || continue
  grep -q '^# forge-toolchain:' "$f" || notes="${notes}hand-written ${existing}.yml; "
done

# --- pre-commit ---
if pc_err=$(forge precommit generate --dry-run 2>&1 >"$tmp/pre-commit.yaml"); then
  if [ -s "$tmp/pre-commit.yaml" ]; then
    if command -v pre-commit > /dev/null 2>&1; then
      if ! pv=$(pre-commit validate-config "$tmp/pre-commit.yaml" 2>&1); then
        problems="${problems}pre-commit: invalid: $(echo "$pv" | head -1); "
      fi
    fi
  else
    problems="${problems}pre-commit: generated nothing; "
  fi
else
  case "$pc_err" in
    *"declares no toolchain.components"*|*"not in the repo registry"*) notes="${notes}pre-commit n/a; " ;;
    *) problems="${problems}pre-commit: ${pc_err#Error: }; " ;;
  esac
fi

# A config can be schema-valid and still fail on the first commit. Every
# `npm run X` the generator emits has to exist in that component's package.json
# — `pre-commit validate-config` has no idea whether it does.
if [ -s "$tmp/pre-commit.yaml" ] && command -v jq > /dev/null 2>&1; then
  missing=""
  while read -r dir script; do
    [ -n "$dir" ] || continue
    manifest="$dir/package.json"
    if [ ! -f "$manifest" ]; then
      missing="${missing}${dir}:no-package.json "
    elif ! jq -e --arg s "$script" '.scripts[$s]' "$manifest" > /dev/null 2>&1; then
      missing="${missing}${dir}:${script} "
    fi
    # --if-present invocations are deliberately optional; only strict ones count.
  done < <(sed -n "s/.*cd \([^ ]*\) && npm run \([a-z][a-z:._-]*\).*/\1 \2/p" "$tmp/pre-commit.yaml" | sort -u)
  [ -n "$missing" ] && problems="${problems}pre-commit: missing npm scripts: ${missing%% }; "
fi

# Custom hooks with no marker abort a real sync. Surfacing them here is the
# whole point: the fix is adding markers, not letting the sync destroy them.
if [ -f .pre-commit-config.yaml ]; then
  if ! chk=$(forge precommit check 2>&1); then
    problems="${problems}${chk#Error: }; "
  fi
fi

if [ -n "$problems" ]; then
  echo "$problems"
  exit 1
fi

echo "${notes:-nothing to generate}"
[ -n "$notes" ] || exit 2
exit 0
