#!/bin/bash
# Run all pre-commit script tests.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "=== generate_config tests ==="
python3 test_generate_config.py

echo "=== merge_pyproject_tools tests ==="
uv run --with tomlkit python3 test_merge_pyproject_tools.py

echo "=== integration tests ==="
python3 test_integration.py

echo ""
echo "all test suites passed"
