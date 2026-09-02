#!/usr/bin/env python3
"""Merge the standard [tool.*] sections into a repo's pyproject.toml.

The standard owns exactly the keys it writes, and records them under
`[tool.forge] managed`. That record is what makes retraction possible: a key
dropped from the template is removed from every repo, because the record proves
forge put it there. A key absent from the record belongs to the project and is
never deleted.

The record authorizes adoption on the same terms. A key the project already
sets, to a value the standard disagrees with, and which the record does not
claim, is a conflict: the standard is reported and the project's value stays.
Adoption and deletion ask one question between them — did forge write this? —
so neither needs to know what any individual key means.

Forcing a key regardless is what makes a file contradict itself. The value the
project chose is inverted, the comment above it survives arguing for the old
one, and the file then asserts two things of which one is false. Nothing here
can detect that prose, so not writing the value is the only thing that keeps it
true. Once the record claims a key the hazard is gone, because the record is
proof forge chose the value that is sitting there.

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
`would-update` under --check. Under it come the conflicts, then the retracted
keys, then the diff when checking.

A conflict line is tab-separated so a caller can split it without guessing where
a value ends:

    conflict<TAB>mypy.ignore_missing_imports<TAB>false<TAB>true

`current` and a conflict list appear together when the only thing to say about a
repo is a key it disagrees on. Nothing would be written there, and something is
still wrong, so the status word alone cannot carry it.
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


def read_path(tool, path):
    """The value at a leaf path, and whether the project set it at all.

    Absence and a falsy value are different answers here, so the caller gets a
    flag rather than having to read `None` as "not there" — a key legitimately
    set to `false` is exactly the case this whole distinction exists for.
    """
    table = tool
    for key in path[:-1]:
        if key not in table or not isinstance(table[key], dict):
            return None, False
        table = table[key]
    if path[-1] not in table:
        return None, False
    return table[path[-1]], True


def plain(value):
    """A tomlkit item as the Python value it wraps.

    Comparison has to go through this. tomlkit's boolean is not a `bool`
    subclass — Python forbids subclassing it — so a wrapped `False` compares
    unequal to `False` and every boolean key would read as a conflict.
    """
    unwrap = getattr(value, 'unwrap', None)
    return unwrap() if callable(unwrap) else value


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
    """Adopt the keys forge may write, retract the ones the standard dropped.

    Deletion is scoped to the recorded paths by construction, which is the whole
    guarantee: a project's own keys are unreachable from here. Adoption is
    scoped the same way, so a value the project chose is unreachable too.

    Each key the standard names is in one of four states:

        absent from the target        adopt it, and record it
        recorded                      forge wrote it, so the template wins
        set, unrecorded, same value   record it; the file does not change
        set, unrecorded, differs      a conflict — report it, write nothing

    The third state is what lets a repo converge. Agreement is not a conflict,
    and refusing to record it would leave the key outside the standard forever.
    """
    managed = flatten(standard_tool)
    recorded = read_managed_paths(target_tool)

    retracted = []
    for path in recorded:
        if path not in managed and delete_path(target_tool, path):
            retracted.append(path)

    claimed = set(recorded)
    adopted = {}
    conflicts = []
    for path, value in managed.items():
        existing, present = read_path(target_tool, path)
        if present and path not in claimed and plain(existing) != plain(value):
            conflicts.append((path, plain(existing), plain(value)))
            continue
        # Writing an equal value back would re-render the item and can move the
        # trivia attached to it, which turns an agreeing repo into a diff.
        if not present or plain(existing) != plain(value):
            set_path(target_tool, path, value)
        adopted[path] = value

    write_managed_paths(target_tool, adopted)
    return retracted, conflicts


def format_path(path):
    return '.'.join(f'"{segment}"' if '.' in segment else segment for segment in path)


def format_value(value):
    """A value spelled as TOML spells it, so a conflict reads like the file.

    `False` printed as Python's `False` sends a reader looking for a line that
    is not in their pyproject. Never truncated: a conflict on a long list is
    where the reader most needs to see which entries differ.
    """
    if isinstance(value, bool):
        return 'true' if value else 'false'
    if isinstance(value, str):
        return f'"{value}"'
    if isinstance(value, (list, tuple)):
        return '[' + ', '.join(format_value(item) for item in value) + ']'
    return str(value)


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

    retracted, conflicts = apply_standard(standard['tool'], target['tool'])

    # The record table carries a trailing blank line so it does not abut the next
    # header. When it lands at EOF there is no next header, and end-of-file-fixer
    # strips the blank on commit — leaving the sync and the hook to undo each
    # other on every run. Normalising here settles it in the sync's favour.
    merged = tomlkit.dumps(target).rstrip('\n') + '\n'

    changed = merged != original
    print(('would-update' if check else 'updated') if changed else 'current')

    # Conflicts are reported whether or not anything else moved. A repo whose
    # only finding is a key it disagrees on writes nothing, and saying `current`
    # and stopping there would report it converged.
    for path, project_value, standard_value in conflicts:
        print(f'  conflict\t{format_path(path)}\t{format_value(project_value)}\t{format_value(standard_value)}')
    for path in retracted:
        print(f'  retracted {format_path(path)}')

    if not changed:
        return 0

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
