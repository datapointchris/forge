#!/usr/bin/env python3
"""Tests for generate-config.py."""

import tempfile
from pathlib import Path

from generate_config import (
    extract_custom_sections,
    generate_config,
    get_block_name,
    get_existing_hook_ids,
    get_standard_hook_ids,
    should_include_block,
)


def make_blocks(tmp: Path) -> Path:
    """Create minimal test blocks."""
    blocks = tmp / 'blocks'
    blocks.mkdir()

    (blocks / '00-conventional-commits.yml').write_text(
        '  # Conventional commits\n'
        '  - repo: https://example.com/commits\n'
        '    hooks:\n'
        '      - id: conventional-pre-commit\n'
    )
    (blocks / '05-file-checks.yml').write_text(
        '  # File format checks\n'
        '  - repo: https://example.com/hooks\n'
        '    hooks:\n'
        '      - id: check-yaml\n'
        '      - id: check-toml\n'
    )
    (blocks / '30-python-format.yml').write_text(
        '  # Python: formatting\n'
        '  - repo: https://example.com/ruff\n'
        '    hooks:\n'
        '      - id: ruff-format\n'
    )
    (blocks / '40-go.yml').write_text(
        '  # Go\n'
        '  - repo: https://example.com/go\n'
        '    hooks:\n'
        '      - id: go-vet\n'
    )
    return blocks


def test_get_block_name():
    assert get_block_name('05-file-checks.yml') == 'file-checks'
    assert get_block_name('30-python-format.yml') == 'python-format'
    assert get_block_name('00-conventional-commits.yml') == 'conventional-commits'


def test_should_include_block():
    detected = {'python', 'actions'}
    assert should_include_block('conventional-commits', detected)
    assert should_include_block('file-checks', detected)
    assert should_include_block('python-format', detected)
    assert not should_include_block('go', detected)
    assert not should_include_block('vue', detected)


def test_generate_simple_config():
    """Config with no custom sections includes only detected blocks."""
    with tempfile.TemporaryDirectory() as tmp:
        blocks = make_blocks(Path(tmp))
        config = generate_config(blocks, {'python'}, {})

        assert '# generated:conventional-commits' in config
        assert '# generated:file-checks' in config
        assert '# generated:python-format' in config
        assert 'go-vet' not in config
        assert 'custom' not in config


def test_custom_sections_preserved():
    """Custom hooks survive a regeneration cycle."""
    with tempfile.TemporaryDirectory() as tmp:
        blocks = make_blocks(Path(tmp))

        custom = {
            'before:file-checks': (
                '# > custom:before:file-checks - Stats capture\n'
                '  - repo: local\n'
                '    hooks:\n'
                '      - id: devstats-capture'
            ),
            'after:python-format': (
                '# > custom:after:python-format - Custom linter\n'
                '  - repo: local\n'
                '    hooks:\n'
                '      - id: my-linter'
            ),
            'after:all': (
                '# > custom:after:all - Tests\n'
                '  - repo: local\n'
                '    hooks:\n'
                '      - id: pytest-results'
            ),
        }

        config = generate_config(blocks, {'python'}, custom)
        lines = config.splitlines()

        # Find positions
        capture_idx = next(i for i, l in enumerate(lines) if 'devstats-capture' in l)
        file_checks_idx = next(i for i, l in enumerate(lines) if 'generated:file-checks' in l)
        linter_idx = next(i for i, l in enumerate(lines) if 'my-linter' in l)
        python_idx = next(i for i, l in enumerate(lines) if 'generated:python-format' in l)
        pytest_idx = next(i for i, l in enumerate(lines) if 'pytest-results' in l)

        # Verify ordering
        assert capture_idx < file_checks_idx, 'custom:before:file-checks should come before file-checks'
        assert linter_idx > python_idx, 'custom:after:python-format should come after python-format'
        assert pytest_idx > python_idx, 'custom:after:all should be at the end'


def test_extract_custom_sections():
    """Markers are correctly extracted from an existing config."""
    with tempfile.TemporaryDirectory() as tmp:
        config = Path(tmp) / '.pre-commit-config.yaml'
        config.write_text(
            'repos:\n'
            '# > custom:before:file-checks - Stats capture\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: devstats-capture\n'
            '\n'
            '# generated:file-checks - File checks\n'
            '  - repo: https://example.com\n'
            '\n'
            '# > custom:after:all - Tests\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: pytest-results\n'
        )

        sections = extract_custom_sections(config)
        assert 'before:file-checks' in sections
        assert 'after:all' in sections
        assert 'devstats-capture' in sections['before:file-checks']
        assert 'pytest-results' in sections['after:all']


def test_roundtrip_preserves_custom():
    """Generate → extract → regenerate preserves custom sections."""
    with tempfile.TemporaryDirectory() as tmp:
        blocks = make_blocks(Path(tmp))

        custom = {
            'before:file-checks': (
                '# > custom:before:file-checks - Capture\n'
                '  - repo: local\n'
                '    hooks:\n'
                '      - id: devstats-capture'
            ),
        }

        # First generation
        config1 = generate_config(blocks, {'python'}, custom)

        # Write and re-extract
        config_path = Path(tmp) / '.pre-commit-config.yaml'
        config_path.write_text(config1)
        extracted = extract_custom_sections(config_path)

        # Second generation from extracted
        config2 = generate_config(blocks, {'python'}, extracted)

        assert config1 == config2, 'Roundtrip should produce identical output'


def test_safety_check_blocks_unknown_hooks():
    """Unknown hooks without markers should be detected."""
    with tempfile.TemporaryDirectory() as tmp:
        blocks = make_blocks(Path(tmp))

        config_path = Path(tmp) / '.pre-commit-config.yaml'
        config_path.write_text(
            'repos:\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: check-yaml\n'
            '      - id: devstats-capture\n'
            '      - id: my-secret-hook\n'
        )

        existing = get_existing_hook_ids(config_path)
        standard = get_standard_hook_ids(blocks)
        unknown = existing - standard

        assert 'devstats-capture' in unknown
        assert 'my-secret-hook' in unknown
        assert 'check-yaml' not in unknown


def test_custom_hooks_not_duplicated_in_standard():
    """If a custom section defines a hook ID that exists in a standard block, the standard one is stripped."""
    with tempfile.TemporaryDirectory() as tmp:
        blocks = make_blocks(Path(tmp))

        # Custom section overrides ruff-format from the python-format block
        custom = {
            'after:python-format': (
                '# > custom:after:python-format - Custom formatter\n'
                '  - repo: local\n'
                '    hooks:\n'
                '      - id: ruff-format\n'
                '        name: custom ruff-format\n'
                '        entry: custom-ruff'
            ),
        }

        config = generate_config(blocks, {'python'}, custom)

        # ruff-format should appear exactly once (the custom one)
        count = config.count('id: ruff-format')
        assert count == 1, f'Expected 1 ruff-format, got {count}'
        assert 'custom-ruff' in config


if __name__ == '__main__':
    test_get_block_name()
    test_should_include_block()
    test_generate_simple_config()
    test_custom_sections_preserved()
    test_extract_custom_sections()
    test_roundtrip_preserves_custom()
    test_safety_check_blocks_unknown_hooks()
    test_custom_hooks_not_duplicated_in_standard()
    print('all tests passed')
