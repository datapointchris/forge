#!/usr/bin/env python3
"""Tests for merge_pyproject_tools.py."""

import io
import tempfile
from contextlib import redirect_stdout
from pathlib import Path

from merge_pyproject_tools import apply_standard, flatten, main, read_managed_paths

import tomlkit


def sync(standard_toml, target_toml):
    """Apply the standard to a target, returning (target, retracted)."""
    standard = tomlkit.parse(standard_toml)
    target = tomlkit.parse(target_toml)
    retracted, _ = apply_standard(standard, target)
    return target, retracted


def sync_conflicts(standard_toml, target_toml):
    """Apply the standard to a target, returning (target, conflicts)."""
    standard = tomlkit.parse(standard_toml)
    target = tomlkit.parse(target_toml)
    _, conflicts = apply_standard(standard, target)
    return target, conflicts


def test_adds_missing_sections():
    """Standard sections are added when target has none."""
    target, _ = sync('[ruff]\nline-length = 140\n', '')

    assert target['ruff']['line-length'] == 140


def test_forces_a_key_the_record_already_claims():
    """Once the record proves forge wrote a key, the template wins outright."""
    target, _ = sync('[ruff]\nline-length = 140\n\n[ruff.lint]\nselect = ["E", "F"]\n', '')
    assert target['ruff']['line-length'] == 140

    # The template moves on a later run. Both keys are recorded by now.
    apply_standard(
        tomlkit.parse('[ruff]\nline-length = 100\n\n[ruff.lint]\nselect = ["ALL"]\n'), target
    )

    assert target['ruff']['line-length'] == 100
    assert target['ruff']['lint']['select'] == ['ALL']


def test_reports_a_conflict_rather_than_inverting_a_project_value():
    """A key the project set, and the record does not claim, is not forge's.

    Built from lambda-durable-functions, which sets `ignore_missing_imports =
    false` under a comment explaining that an unresolved SDK import turns every
    decorated handler into Any. The first sync inverted the value and left the
    comment arguing for the old one, so the file asserted both.
    """
    target, conflicts = sync_conflicts(
        '[mypy]\nignore_missing_imports = true\npretty = true\n',
        '[mypy]\nignore_missing_imports = false\n',
    )

    assert conflicts == [(('mypy', 'ignore_missing_imports'), False, True)]
    assert target['mypy']['ignore_missing_imports'] is False
    # The rest of the standard still lands; one disagreement stops one key.
    assert target['mypy']['pretty'] is True
    # And forge never claims what it did not write, so retraction cannot reach it.
    assert read_managed_paths(target) == [('mypy', 'pretty')]


def test_a_conflicted_key_stays_out_of_the_record_across_repeat_syncs():
    """The conflict does not decay into an adoption on the second run."""
    standard = '[mypy]\nignore_missing_imports = true\n'
    target, _ = sync_conflicts(standard, '[mypy]\nignore_missing_imports = false\n')

    _, conflicts = apply_standard(tomlkit.parse(standard), target)

    assert conflicts == [(('mypy', 'ignore_missing_imports'), False, True)]
    assert target['mypy']['ignore_missing_imports'] is False


def test_adopts_a_key_the_project_already_agrees_on():
    """Agreement is not a conflict, or a repo could never converge."""
    target, conflicts = sync_conflicts('[ruff]\nline-length = 140\n', '[ruff]\nline-length = 140\n')

    assert conflicts == []
    assert read_managed_paths(target) == [('ruff', 'line-length')]


def test_a_project_value_of_false_is_a_value_not_an_absence():
    """`false` is set, so the standard's `true` conflicts rather than adopting.

    tomlkit's boolean is not a bool subclass, so an identity or a truthiness
    test here reads every boolean key as a conflict or none of them.
    """
    _, conflicts = sync_conflicts('[mypy]\nstrict = true\n', '[mypy]\nstrict = false\n')
    assert conflicts == [(('mypy', 'strict'), False, True)]

    _, agreed = sync_conflicts('[mypy]\nstrict = false\n', '[mypy]\nstrict = false\n')
    assert agreed == []


def test_never_deletes_a_key_it_did_not_write():
    """Project config survives, whatever section it sits in.

    Regression for the two incidents REPLACE_SECTIONS caused: a repo's ruff
    `exclude`, then a FastAPI repo's bugbear exemptions, its pydantic mypy
    plugin and an alembic per-file-ignore. Nothing failed at sync time; the
    bugbear loss surfaced as 57 B008 errors in CI on the next push.
    """
    target, retracted = sync(
        '[ruff]\nline-length = 140\n\n[ruff.lint]\nselect = ["E"]\n\n[mypy]\npretty = true\n',
        '[ruff]\nline-length = 120\nexclude = ["migrations"]\n\n'
        '[ruff.lint]\nselect = ["ALL"]\n\n'
        '[ruff.lint.flake8-bugbear]\nextend-immutable-calls = ["fastapi.Depends"]\n\n'
        '[mypy]\nplugins = ["pydantic.mypy"]\n',
    )

    assert retracted == []
    assert target['ruff']['exclude'] == ['migrations']
    assert target['ruff']['lint']['flake8-bugbear']['extend-immutable-calls'] == ['fastapi.Depends']
    assert target['mypy']['plugins'] == ['pydantic.mypy']


def test_retracts_a_key_dropped_from_the_standard():
    """A key forge wrote and the template no longer names is removed."""
    standard = '[ruff.lint.flake8-bugbear]\nextend-immutable-calls = ["fastapi.Depends"]\n'
    target, _ = sync(standard, '')
    assert read_managed_paths(target) == [('ruff', 'lint', 'flake8-bugbear', 'extend-immutable-calls')]

    # The template drops the section entirely on a later run.
    retracted, _ = apply_standard(tomlkit.parse('[ruff]\nline-length = 140\n'), target)

    assert retracted == [('ruff', 'lint', 'flake8-bugbear', 'extend-immutable-calls')]
    # Pruning cascades: the empty flake8-bugbear table takes `lint` with it,
    # leaving only what the new standard writes.
    assert 'lint' not in target['ruff']
    assert target['ruff']['line-length'] == 140


def test_retraction_is_scoped_to_what_forge_recorded():
    """The same key survives when the record does not claim it.

    This is the whole guarantee. ichrisbirch's bugbear exemptions look exactly
    like the ones the template used to inject; only the record distinguishes
    a key forge wrote from one the project added.
    """
    target = tomlkit.parse(
        '[ruff.lint.flake8-bugbear]\nextend-immutable-calls = ["fastapi.Depends"]\n'
    )
    retracted, _ = apply_standard(tomlkit.parse('[ruff]\nline-length = 140\n'), target)

    assert retracted == []
    assert target['ruff']['lint']['flake8-bugbear']['extend-immutable-calls'] == ['fastapi.Depends']


def test_records_every_leaf_the_standard_writes():
    """The record is the flattened standard, including keys holding dots."""
    target, _ = sync(
        '[ruff.lint.per-file-ignores]\n"__init__.py" = ["F401"]\n\n[mypy]\npretty = true\n', ''
    )

    assert read_managed_paths(target) == [
        ('ruff', 'lint', 'per-file-ignores', '__init__.py'),
        ('mypy', 'pretty'),
    ]


def test_preserves_unrelated_sections():
    """Sections not in standard are left untouched."""
    target, _ = sync(
        '[mypy]\npretty = true\n', '[mypy]\nplugins = ["pydantic.mypy"]\n\n[coverage]\nbranch = true\n'
    )

    assert target['mypy']['pretty'] is True
    assert target['mypy']['plugins'] == ['pydantic.mypy']
    assert target['coverage']['branch'] is True


def test_flatten_descends_to_leaves_only():
    leaves = flatten(tomlkit.parse('[a]\nx = 1\n\n[a.b]\ny = 2\n'))

    assert leaves == {('a', 'x'): 1, ('a', 'b', 'y'): 2}


def test_second_sync_is_a_no_op():
    """Idempotence is what the die's SKIP status depends on."""
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff]\nline-length = 140\n')
        target_path.write_text('[project]\nname = "myapp"\n')

        assert main([str(standard_path), str(target_path)]) == 0
        after_first = target_path.read_text()
        assert main([str(standard_path), str(target_path)]) == 0

        assert target_path.read_text() == after_first


def test_leaves_exactly_one_newline_at_eof():
    """The record table is often the last thing in the file.

    Its trailing blank line keeps it off the next header, but at EOF there is no
    next header and pre-commit's end-of-file-fixer strips it — so sync and hook
    rewrite each other on every commit until one of them yields.
    """
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff]\nline-length = 140\n')
        target_path.write_text('[project]\nname = "myapp"\n')

        assert main([str(standard_path), str(target_path)]) == 0

        written = target_path.read_text()
        assert written.endswith(']\n')
        assert not written.endswith('\n\n')


def test_check_reports_without_writing():
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff]\nline-length = 140\n')
        target_path.write_text('[project]\nname = "myapp"\n')
        before = target_path.read_text()

        assert main(['--check', str(standard_path), str(target_path)]) == 0

        assert target_path.read_text() == before


def test_a_conflict_alone_is_reported_under_current():
    """Nothing to write and something to say is a state the status word lacks.

    Every standard key is either already agreed or conflicting, so the file does
    not change. Printing `current` and stopping would report the repo converged
    while a key it disagrees on goes unmentioned.
    """
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.mypy]\nignore_missing_imports = true\npretty = true\n')
        target_path.write_text('[tool.mypy]\nignore_missing_imports = false\n')

        # The first run adopts `pretty` and writes the record, so the file moves.
        assert main([str(standard_path), str(target_path)]) == 0
        settled = target_path.read_text()

        captured = io.StringIO()
        with redirect_stdout(captured):
            assert main([str(standard_path), str(target_path)]) == 0

        lines = captured.getvalue().splitlines()
        assert lines[0] == 'current'
        assert lines[1] == '  conflict\tmypy.ignore_missing_imports\tfalse\ttrue'
        assert target_path.read_text() == settled


def test_full_pyproject_roundtrip():
    """Merge into a realistic pyproject.toml preserves project metadata."""
    target, _ = sync(
        '[ruff]\nline-length = 140\n\n[pyright]\ntypeCheckingMode = "standard"\n\n'
        '[codespell]\ncheck-filenames = true\n',
        '[project]\nname = "myapp"\nversion = "1.0.0"\n\n'
        '[pyright]\nreportAny = false\n\n'
        '[codespell]\nskip = "*.lock"\n\n'
        '[build-system]\nrequires = ["uv-build"]\n',
    )

    assert target['project']['name'] == 'myapp'
    assert target['build-system']['requires'] == ['uv-build']
    assert target['ruff']['line-length'] == 140
    assert target['codespell']['check-filenames'] is True
    assert target['codespell']['skip'] == '*.lock'
    # A hand-added pyright rule is the project's until a migration removes it.
    assert target['pyright']['typeCheckingMode'] == 'standard'
    assert target['pyright']['reportAny'] is False


if __name__ == '__main__':
    test_adds_missing_sections()
    test_forces_a_key_the_record_already_claims()
    test_reports_a_conflict_rather_than_inverting_a_project_value()
    test_a_conflicted_key_stays_out_of_the_record_across_repeat_syncs()
    test_adopts_a_key_the_project_already_agrees_on()
    test_a_project_value_of_false_is_a_value_not_an_absence()
    test_never_deletes_a_key_it_did_not_write()
    test_retracts_a_key_dropped_from_the_standard()
    test_retraction_is_scoped_to_what_forge_recorded()
    test_records_every_leaf_the_standard_writes()
    test_preserves_unrelated_sections()
    test_flatten_descends_to_leaves_only()
    test_second_sync_is_a_no_op()
    test_leaves_exactly_one_newline_at_eof()
    test_check_reports_without_writing()
    test_a_conflict_alone_is_reported_under_current()
    test_full_pyproject_roundtrip()
    print('all tests passed')
