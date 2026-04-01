#!/usr/bin/env python3
"""Merge standard tool config sections into an existing pyproject.toml.

Two merge strategies per section:
- MERGE (default): standard keys overwrite, but project-specific keys are preserved.
  Good for codespell (keep project skip patterns), pytest (keep markers), etc.
- REPLACE: section is fully replaced by the standard version.
  Good for pyright (collapse 12 verbose keys into typeCheckingMode), ruff (standardize rules).

Sections listed in REPLACE_SECTIONS get replaced. Everything else merges.
Sections that only exist in the target (like [tool.coverage]) are left untouched.

Usage: merge-pyproject-tools.py <standard.toml> <target-pyproject.toml>
"""

import sys

import tomlkit

# These [tool.*] sections get fully replaced by the standard.
# Everything else is deep-merged (project extras preserved).
REPLACE_SECTIONS = {
    'pyright',
    'ruff',
    'ruff.format',
    'ruff.lint',
    'ruff.lint.isort',
    'ruff.lint.per-file-ignores',
    'mypy',
}


def deep_merge(standard, target, path=''):
    """Merge standard into target in-place."""
    for key, value in standard.items():
        current_path = f'{path}.{key}' if path else key

        if current_path in REPLACE_SECTIONS:
            target[key] = value
        elif key not in target:
            target[key] = value
        elif isinstance(value, dict) and isinstance(target[key], dict):
            deep_merge(value, target[key], current_path)
        else:
            target[key] = value


def main():
    if len(sys.argv) != 3:
        print(f'Usage: {sys.argv[0]} <standard.toml> <target.toml>')
        return 1

    standard_path = sys.argv[1]
    target_path = sys.argv[2]

    with open(standard_path) as f:
        standard = tomlkit.parse(f.read())

    with open(target_path) as f:
        target = tomlkit.parse(f.read())

    if 'tool' not in standard:
        print('no [tool] section in standard')
        return 1

    if 'tool' not in target:
        target['tool'] = tomlkit.table()

    deep_merge(standard['tool'], target['tool'])

    new_content = tomlkit.dumps(target)

    with open(target_path) as f:
        if f.read() == new_content:
            return 0

    with open(target_path, 'w') as f:
        f.write(new_content)

    print('updated')
    return 0


if __name__ == '__main__':
    sys.exit(main())
