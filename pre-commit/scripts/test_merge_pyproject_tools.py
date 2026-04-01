#!/usr/bin/env python3
"""Tests for merge_pyproject_tools.py."""

import tempfile
from pathlib import Path

from merge_pyproject_tools import REPLACE_SECTIONS, deep_merge

import tomlkit


def test_adds_missing_sections():
    """Standard sections are added when target has none."""
    standard = tomlkit.parse('[ruff]\nline-length = 140\n')
    target = tomlkit.parse('')
    deep_merge(standard, target)

    assert target['ruff']['line-length'] == 140


def test_replaces_replace_sections():
    """Sections in REPLACE_SECTIONS are fully replaced, not merged."""
    assert 'pyright' in REPLACE_SECTIONS

    standard = tomlkit.parse(
        '[pyright]\ntypeCheckingMode = "basic"\nreportPossiblyUnboundVariable = false\n'
    )
    target = tomlkit.parse(
        '[pyright]\n'
        'analyzeUnannotatedFunctions = true\n'
        'reportAny = false\n'
        'reportImplicitOverride = false\n'
        'reportMissingParameterType = false\n'
        'reportUnknownArgumentType = false\n'
    )
    deep_merge(standard, target)

    pyright = target['pyright']
    assert pyright['typeCheckingMode'] == 'basic'
    assert pyright['reportPossiblyUnboundVariable'] is False
    # Old verbose keys should be gone
    assert 'analyzeUnannotatedFunctions' not in pyright
    assert 'reportAny' not in pyright


def test_merges_non_replace_sections():
    """Sections NOT in REPLACE_SECTIONS preserve target-specific keys."""
    assert 'codespell' not in REPLACE_SECTIONS

    standard = tomlkit.parse('[codespell]\ncheck-filenames = true\n')
    target = tomlkit.parse('[codespell]\nskip = "*.css.map"\nignore-words-list = "colour"\n')
    deep_merge(standard, target)

    codespell = target['codespell']
    assert codespell['check-filenames'] is True
    assert codespell['skip'] == '*.css.map'
    assert codespell['ignore-words-list'] == 'astroid'


def test_preserves_unrelated_sections():
    """Sections not in standard are left untouched."""
    standard = tomlkit.parse('[mypy]\npretty = true\n')
    target = tomlkit.parse('[mypy]\npretty = false\n[coverage]\nbranch = true\n')
    deep_merge(standard, target)

    assert target['mypy']['pretty'] is True
    assert target['coverage']['branch'] is True


def test_full_pyproject_roundtrip():
    """Merge into a realistic pyproject.toml preserves project metadata."""
    with tempfile.TemporaryDirectory() as tmp:
        standard_path = Path(tmp) / 'standard.toml'
        target_path = Path(tmp) / 'pyproject.toml'

        standard_path.write_text(
            '[ruff]\nline-length = 140\n\n'
            '[pyright]\ntypeCheckingMode = "basic"\n\n'
            '[codespell]\ncheck-filenames = true\n'
        )
        target_path.write_text(
            '[project]\nname = "myapp"\nversion = "1.0.0"\n\n'
            '[project.dependencies]\nfastapi = ">=0.100"\n\n'
            '[pyright]\nreportAny = false\nreportUnknownArgumentType = false\n\n'
            '[codespell]\nskip = "*.lock"\n\n'
            '[build-system]\nrequires = ["uv-build"]\n'
        )

        with open(standard_path) as f:
            standard = tomlkit.parse(f.read())
        with open(target_path) as f:
            target = tomlkit.parse(f.read())

        deep_merge(standard, target)

        # Project metadata untouched
        assert target['project']['name'] == 'myapp'
        assert target['project']['version'] == '1.0.0'
        assert target['build-system']['requires'] == ['uv-build']

        # Ruff added
        assert target['ruff']['line-length'] == 140

        # Pyright replaced (old keys gone)
        assert target['pyright']['typeCheckingMode'] == 'basic'
        assert 'reportAny' not in target['pyright']

        # Codespell merged (project skip preserved)
        assert target['codespell']['check-filenames'] is True
        assert target['codespell']['skip'] == '*.lock'


if __name__ == '__main__':
    test_adds_missing_sections()
    test_replaces_replace_sections()
    test_merges_non_replace_sections()
    test_preserves_unrelated_sections()
    test_full_pyproject_roundtrip()
    print('all tests passed')
