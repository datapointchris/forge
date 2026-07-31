#!/usr/bin/env python3
"""Merge standard tool config sections into an existing pyproject.toml.

Two merge strategies per section:
- MERGE (default): standard keys overwrite, but project-specific keys are preserved.
  Good for codespell (keep project skip patterns), mypy (keep plugins), ruff.lint
  (force select/ignore, keep a framework's bugbear exemptions).
- REPLACE: section is fully replaced by the standard version.
  Good for pyright, where collapsing the hand-disabled rules into typeCheckingMode
  depends on the ones the standard does not name being removed.

Sections listed in REPLACE_SECTIONS get replaced. Everything else merges.
Sections that only exist in the target (like [tool.coverage]) are left untouched.

Usage: merge-pyproject-tools.py <standard.toml> <target-pyproject.toml>
"""

import sys

import tomlkit

# Sections the standard OWNS: unknown keys are deleted, not kept. Everything
# else is a floor -- the standard's keys win, the project's extras survive.
#
# The split is which job a section is doing, and they are incompatible. Owning
# is what collapses pyright's twelve hand-disabled rules into typeCheckingMode,
# because the eleven the standard does not name have to disappear. Flooring is
# what lets a repo keep mypy `plugins` or a flake8-bugbear exemption its
# framework requires. Listing a floor section here deletes that silently, and
# the failure surfaces much later on an unrelated commit.
REPLACE_SECTIONS = {
    'pyright',
    'ruff.format',
    'ruff.lint.isort',
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
