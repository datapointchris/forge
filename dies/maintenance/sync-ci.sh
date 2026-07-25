#!/bin/bash
# Generate .github/workflows/validate.yml — one job per declared component —
# from standard CI blocks. Repo-specific pipelines stay in their own workflow
# files; this one is additive and callable via workflow_call so a release
# workflow can gate on it.
#
# No stack detection here: components come from toolchain.components in the
# repo registry. Five different conventions for where a Go service lives exist
# across the portfolio, and no probe survives all of them.

output=$(forge ci generate 2>&1)
status=$?

if [ $status -ne 0 ]; then
  # A repo that declares nothing, or declares only stacks with no CI block
  # yet, is not a failure — it is simply out of scope.
  case "$output" in
    *"declares no toolchain.components"*|*"no components with a CI block"*|*"not in the repo registry"*)
      echo "${output#Error: }"
      exit 2
      ;;
  esac
  echo "$output"
  exit 1
fi

if [ "$output" = "no changes" ]; then
  echo "validate.yml current"
  exit 2
fi

echo "$output"
exit 0
