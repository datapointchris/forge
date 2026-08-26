#!/usr/bin/env python3
"""Merge the standard [tool.*] sections into a repo's pyproject.toml.

The standard owns exactly the keys it writes, and records them under
`[tool.forge] managed`. That record is what makes retraction possible: a key
dropped from the template is removed from every repo, because the record proves
forge put it there. A key absent from the record belongs to the project and is
never deleted.

This replaced a hand-maintained REPLACE_SECTIONS set naming whole sections to
overwrite wholesale. Owning a section and setting a floor under one are
different jobs, and a single verb doing both deleted config three times: a
repo's ruff `exclude`, then a FastAPI repo's bugbear exemptions, its pydantic
mypy plugin and an alembic per-file-ignore. Ownership recorded per key rather
than declared per section cannot express that mistake.

Paths are recorded as arrays rather than dotted strings because a segment can
itself contain a dot (`per-file-ignores."__init__.py"`). This is the record
that authorizes deletion; it does not get to depend on quoting being right.

Usage: merge_pyproject_tools.py [--check] <standard.toml> <target-pyproject.toml>

The first line of output is a status word — `current`, `updated`, or
`would-update` under --check. Everything after it is detail for a human:
retracted keys, and the diff when checking.
"""

import difflib
import sys
from pathlib import Path

import tomlkit

MANAGED_TABLE = 'forge'
MANAGED_KEY = 'managed'

MANAGED_COMMENT_LINES = [
    'Keys the shared toolchain standard owns, written by its generator.',
    'Dropping one from the template removes it here on the next sync; a key absent',
    'from this list belongs to the project and is never touched. Do not hand-edit.',
]


def flatten(table, prefix=()):
    """Map every leaf path in a table to its value, depth-first."""
    leaves = {}
    for key, value in table.items():
        path = prefix + (key,)
        if isinstance(value, dict):
            leaves.update(flatten(value, path))
        else:
            leaves[path] = value
    return leaves


def read_managed_paths(tool):
    """The key paths the standard wrote on the last sync, empty on first run."""
    if MANAGED_TABLE not in tool:
        return []
    return [tuple(path) for path in tool[MANAGED_TABLE].get(MANAGED_KEY, [])]


def write_managed_paths(tool, paths):
    """Rebuild the record table from scratch — it is forge's in its entirety.

    Rebuilding rather than patching is what keeps a resync byte-identical: the
    trailing newline the table needs to not abut the next header would otherwise
    accumulate one per run, and the die's SKIP status depends on idempotence.
    """
    record = tomlkit.array().multiline(True)
    for path in paths:
        record.append(list(path))

    table = tomlkit.table()
    for line in MANAGED_COMMENT_LINES:
        table.add(tomlkit.comment(line))
    table[MANAGED_KEY] = record
    table.add(tomlkit.nl())

    if MANAGED_TABLE in tool:
        del tool[MANAGED_TABLE]
    tool[MANAGED_TABLE] = table


def set_path(tool, path, value):
    table = tool
    for key in path[:-1]:
        if key not in table or not isinstance(table[key], dict):
            table[key] = tomlkit.table()
        table = table[key]
    table[path[-1]] = value


def delete_path(tool, path):
    """Remove a leaf, then any table the removal left empty. False if absent."""
    tables = [tool]
    for key in path[:-1]:
        parent = tables[-1]
        if key not in parent or not isinstance(parent[key], dict):
            return False
        tables.append(parent[key])

    if path[-1] not in tables[-1]:
        return False
    del tables[-1][path[-1]]

    for depth in range(len(tables) - 1, 0, -1):
        if tables[depth]:
            break
        del tables[depth - 1][path[depth - 1]]
    return True


def apply_standard(standard_tool, target_tool):
    """Force every key the standard names, retract the ones it no longer does.

    Deletion is scoped to the recorded paths by construction, which is the whole
    guarantee: a project's own keys are unreachable from here.
    """
    managed = flatten(standard_tool)

    retracted = []
    for path in read_managed_paths(target_tool):
        if path not in managed and delete_path(target_tool, path):
            retracted.append(path)

    for path, value in managed.items():
        set_path(target_tool, path, value)

    write_managed_paths(target_tool, managed)
    return retracted


def format_path(path):
    return '.'.join(f'"{segment}"' if '.' in segment else segment for segment in path)


def main(argv):
    check = '--check' in argv
    positional = [arg for arg in argv if arg != '--check']

    if len(positional) != 2:
        print(f'usage: {sys.argv[0]} [--check] <standard.toml> <target.toml>')
        return 1

    standard_file, target_file = Path(positional[0]), Path(positional[1])
    original = target_file.read_text()

    standard = tomlkit.parse(standard_file.read_text())
    target = tomlkit.parse(original)

    if 'tool' not in standard:
        print(f'no [tool] section in {standard_file}')
        return 1

    if 'tool' not in target:
        target['tool'] = tomlkit.table()

    retracted = apply_standard(standard['tool'], target['tool'])

    # The record table carries a trailing blank line so it does not abut the next
    # header. When it lands at EOF there is no next header, and end-of-file-fixer
    # strips the blank on commit — leaving the sync and the hook to undo each
    # other on every run. Normalising here settles it in the sync's favour.
    merged = tomlkit.dumps(target).rstrip('\n') + '\n'

    if merged == original:
        print('current')
        return 0

    print('would-update' if check else 'updated')
    for path in retracted:
        print(f'  retracted {format_path(path)}')

    if check:
        sys.stdout.writelines(
            difflib.unified_diff(
                original.splitlines(keepends=True),
                merged.splitlines(keepends=True),
                fromfile=f'{target_file} (current)',
                tofile=f'{target_file} (synced)',
            )
        )
        return 0

    target_file.write_text(merged)
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
