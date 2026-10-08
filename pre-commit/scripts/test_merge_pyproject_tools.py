#!/usr/bin/env python3
"""Tests for merge_pyproject_tools.py."""

import io
import json
import tempfile
from contextlib import redirect_stdout
from pathlib import Path

from merge_pyproject_tools import apply_standard, flatten, format_path, format_value, main, read_managed_paths

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

    A truthiness test in place of the presence answer reads an unset key and a
    key set to `false` as the same thing, which is what would let the standard
    adopt over a deliberate `false` without reporting it.
    """
    _, conflicts = sync_conflicts('[mypy]\nstrict = true\n', '[mypy]\nstrict = false\n')
    assert conflicts == [(('mypy', 'strict'), False, True)]

    _, agreed = sync_conflicts('[mypy]\nstrict = false\n', '[mypy]\nstrict = false\n')
    assert agreed == []


def test_a_path_it_cannot_read_is_a_conflict_not_an_absence():
    """A blocked segment is not the same answer as an unset key.

    The standard writes `ruff.lint.select`, and the project has put a string at
    `ruff.lint`. Reading that as absent lets adoption replace the segment, which
    destroys the value and reports nothing — the same loss this ownership rule
    exists to prevent, reached through a different door.
    """
    target, conflicts = sync_conflicts(
        '[ruff.lint]\nselect = ["E"]\n', '[ruff]\nlint = "the project put a value here"\n'
    )

    assert conflicts == [(('ruff', 'lint', 'select'), 'the project put a value here', ['E'])]
    assert target['ruff']['lint'] == 'the project put a value here'
    assert read_managed_paths(target) == []


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

    This is the whole guarantee. Nothing in a key's shape says who wrote it.
    A project's own bugbear exemptions read exactly like template output, so
    the record is the only thing separating a key forge wrote from one the
    project added.
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

        report = run_main([str(standard_path), str(target_path)])
        assert report['status'] == 'current'
        assert report['conflicts'] == [{'key': 'mypy.ignore_missing_imports', 'project': 'false', 'standard': 'true'}]
        assert target_path.read_text() == settled


def run_main(argv):
    """Run main and decode the one JSON object it prints."""
    captured = io.StringIO()
    with redirect_stdout(captured):
        assert main(argv) == 0
    return json.loads(captured.getvalue())


def test_the_report_names_a_retraction_and_carries_the_patch():
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff]\nline-length = 140\n')
        target_path.write_text('[tool.ruff]\nline-length = 140\ngone = 1\n\n[tool.forge]\nmanaged = [["ruff", "gone"]]\n')

        report = run_main(['--check', str(standard_path), str(target_path)])

        assert report['status'] == 'would-update'
        assert report['retracted'] == ['ruff.gone']
        assert report['patch'].startswith(f'--- {target_path} (current)')
        assert '-gone = 1' in report['patch']


def test_a_table_value_is_one_inline_conflict():
    """A table the project set where the standard wants a scalar reads as one value."""
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff]\nline-length = 140\n')
        target_path.write_text('[tool.ruff.line-length]\nmax = 140\n')

        report = run_main(['--check', str(standard_path), str(target_path)])

        assert report['conflicts'] == [{'key': 'ruff.line-length', 'project': '{max = 140}', 'standard': '140'}]


def test_every_value_shape_is_written_on_one_line():
    """An array of tables loses its brackets in block form, one shape over from a table."""
    doc = tomlkit.parse(
        '[t]\nscalar = true\nstrings = ["E", "F"]\nnested = [[1, 2], [3]]\n'
        '[t.table]\nmax = 140\n[[t.tables]]\nselect = ["E"]\n[[t.tables]]\nselect = ["F"]\n'
    )['t']
    assert format_value(doc['scalar']) == 'true'
    assert format_value(doc['strings']) == '["E", "F"]'
    assert format_value(doc['nested']) == '[[1, 2], [3]]'
    assert format_value(doc['table']) == '{max = 140}'
    assert format_value(doc['tables']) == '[{select = ["E"]}, {select = ["F"]}]'


def test_a_key_is_spelled_as_toml_spells_it():
    assert format_path(('ruff', 'lint', 'per-file-ignores', '__init__.py')) == 'ruff.lint.per-file-ignores."__init__.py"'
    assert format_path(('',)) == '""'


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


def test_the_shipped_template_records_a_module_alias_as_one_key_and_resyncs_clean():
    """An alias keyed by a dotted module name is one path segment, and a resync writes nothing."""
    template = (Path(__file__).parent.parent / 'configs' / 'pyproject-tools.toml').read_text()
    target = tomlkit.parse('[project]\nname = "myapp"\n')
    target['tool'] = tomlkit.table()
    apply_standard(tomlkit.parse(template)['tool'], target['tool'])
    first = tomlkit.dumps(target)

    apply_standard(tomlkit.parse(template)['tool'], target['tool'])

    assert tomlkit.dumps(target) == first
    assert ('ruff', 'lint', 'flake8-import-conventions', 'extend-aliases', 'pyspark.sql.functions') in read_managed_paths(
        target['tool']
    )
    assert target['tool']['ruff']['lint']['flake8-import-conventions']['extend-aliases']['pyspark.sql.functions'] == 'sf'


def test_a_table_created_mid_file_is_separated_from_the_next_header():
    """A new table lands inside its parent's block, ahead of whatever header follows."""
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff.lint.conventions]\nbanned = ["datetime"]\n\n[tool.ruff.lint.conventions.aliases]\ndatetime = "dt"\n')
        target_path.write_text('[tool.ruff.lint]\nselect = ["E"]\n\n[tool.ruff.format]\nquote-style = "single"\n')

        assert main([str(standard_path), str(target_path)]) == 0
        written = target_path.read_text()
        assert main([str(standard_path), str(target_path)]) == 0

        assert 'datetime = "dt"\n\n[tool.ruff.format]' in written
        assert 'banned = ["datetime"]\n\n[tool.ruff.lint.conventions.aliases]' in written
        assert target_path.read_text() == written


def merge_with_pin(target_text, *pins, check=False):
    """Run main with `--pin` arguments against a written target, returning (file text, report)."""
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'
        standard_path.write_text('[tool.ruff]\nline-length = 140\n')
        target_path.write_text(target_text)
        arguments = ['--check'] if check else []
        for pin in pins:
            arguments += ['--pin', pin]
        output = io.StringIO()
        with redirect_stdout(output):
            assert main([*arguments, str(standard_path), str(target_path)]) == 0
        return target_path.read_text(), json.loads(output.getvalue())


def test_a_pin_is_added_to_the_dev_group_when_no_list_names_it():
    written, _ = merge_with_pin('[project]\nname = "myapp"\n', 'ruff==0.12.5')
    again, _ = merge_with_pin(written, 'ruff==0.12.5')

    document = tomlkit.parse(written)
    assert document['dependency-groups']['dev'] == ['ruff==0.12.5']
    assert document['tool']['forge']['pinned'] == ['ruff']
    assert '\n\n\n' not in written
    assert again == written


def test_a_pin_rewrites_the_spec_and_leaves_its_neighbors_in_order():
    written, _ = merge_with_pin(
        '[dependency-groups]\ndev = [\n    "pytest>=8.0",\n    # the linter\n    "Ruff>=0.7.0",\n    "mypy",\n]\n',
        'ruff==0.12.5',
    )

    assert '    "pytest>=8.0",\n    # the linter\n    "ruff==0.12.5",\n    "mypy",\n' in written


def test_a_pin_reaches_every_group_and_extra_that_names_it():
    written, _ = merge_with_pin(
        '[project]\nname = "myapp"\ndependencies = ["ruff>=0.1"]\n\n'
        '[project.optional-dependencies]\ndev = ["ruff", "pytest"]\n\n'
        '[dependency-groups]\nlint = ["ruff[lsp]>=0.5; python_version >= \'3.11\'"]\n',
        'ruff==0.12.5',
    )

    document = tomlkit.parse(written)
    assert document['project']['optional-dependencies']['dev'] == ['ruff==0.12.5', 'pytest']
    assert document['dependency-groups']['lint'] == ["ruff[lsp]==0.12.5; python_version >= '3.11'"]
    # A runtime dependency is the product's, and no dev group was created beside it.
    assert document['project']['dependencies'] == ['ruff>=0.1']
    assert 'dev' not in document['dependency-groups']


def test_a_pinned_resync_writes_nothing():
    first, _ = merge_with_pin('[dependency-groups]\ndev = ["pytest", "ruff>=0.7"]\n', 'ruff==0.12.5')

    again, report = merge_with_pin(first, 'ruff==0.12.5', check=True)

    assert again == first
    assert report['status'] == 'current'


def test_a_raised_pin_rewrites_a_spec_forge_already_wrote():
    first, _ = merge_with_pin('[dependency-groups]\ndev = ["ruff"]\n', 'ruff==0.12.5')

    raised, report = merge_with_pin(first, 'ruff==0.13.0', check=True)

    assert raised == first
    assert report['status'] == 'would-update'
    assert '+dev = ["ruff==0.13.0"]' in report['patch']


def test_a_pin_without_a_version_is_refused():
    with tempfile.TemporaryDirectory() as tmp:
        target_path = Path(tmp) / 'pyproject.toml'
        target_path.write_text('')

        assert main(['--pin', 'ruff', str(target_path), str(target_path)]) == 1


if __name__ == '__main__':
    test_adds_missing_sections()
    test_forces_a_key_the_record_already_claims()
    test_reports_a_conflict_rather_than_inverting_a_project_value()
    test_a_conflicted_key_stays_out_of_the_record_across_repeat_syncs()
    test_adopts_a_key_the_project_already_agrees_on()
    test_a_project_value_of_false_is_a_value_not_an_absence()
    test_a_path_it_cannot_read_is_a_conflict_not_an_absence()
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
    test_the_report_names_a_retraction_and_carries_the_patch()
    test_a_table_value_is_one_inline_conflict()
    test_every_value_shape_is_written_on_one_line()
    test_a_key_is_spelled_as_toml_spells_it()
    test_full_pyproject_roundtrip()
    test_the_shipped_template_records_a_module_alias_as_one_key_and_resyncs_clean()
    test_a_table_created_mid_file_is_separated_from_the_next_header()
    test_a_pin_is_added_to_the_dev_group_when_no_list_names_it()
    test_a_pin_rewrites_the_spec_and_leaves_its_neighbors_in_order()
    test_a_pin_reaches_every_group_and_extra_that_names_it()
    test_a_pinned_resync_writes_nothing()
    test_a_raised_pin_rewrites_a_spec_forge_already_wrote()
    test_a_pin_without_a_version_is_refused()
    print('all tests passed')
