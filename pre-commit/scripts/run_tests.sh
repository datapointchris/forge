#!/bin/bash
# Run the pyproject merge script's tests.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

uv run --with tomlkit python3 test_merge_pyproject_tools.py
