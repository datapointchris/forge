#!/bin/bash
# Check: a pre-commit config exists and won't fire hooks at every stage.
#
# A hook with no `stages:` and no top-level `default_stages:` runs at EVERY
# installed git hook type. When the config also installs commit-msg,
# prepare-commit-msg, post-commit, or pre-push hooks, those unrestricted hooks
# fire at every one of them — the "hooks ran 3x per commit" bug. Require an
# explicit `default_stages:` whenever the config uses any non-pre-commit stage.

config=""
for f in .pre-commit-config.yaml .pre-commit-config.yml; do
  if [ -f "$f" ]; then
    config="$f"
    break
  fi
done

if [ -z "$config" ]; then
  echo "no .pre-commit-config found"
  exit 1
fi

if grep -qE '^\s*default_stages:' "$config"; then
  echo "found $config (default_stages set)"
  exit 0
fi

# No default_stages — only a problem if the config installs extra hook stages.
extra_stages=$(grep -oE 'stages:[[:space:]]*\[[^]]*\]' "$config" \
  | grep -oE 'commit-msg|prepare-commit-msg|post-commit|post-merge|post-checkout|pre-push|post-rewrite' \
  | sort -u | tr '\n' ' ')

if [ -n "$extra_stages" ]; then
  echo "missing default_stages: [pre-commit] — unrestricted hooks also fire at: ${extra_stages% }"
  exit 1
fi

echo "found $config (single-stage, default_stages not required)"
exit 0
