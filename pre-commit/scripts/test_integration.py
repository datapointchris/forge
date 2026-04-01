#!/usr/bin/env python3
"""Integration tests for the sync-pre-commit die.

Creates temp repos with specific files to trigger tech stack detection,
runs the generator, and validates the output.
"""

import os
import re
import subprocess
import tempfile
from pathlib import Path

FORGE_ROOT = Path(__file__).resolve().parent.parent.parent
BLOCKS_DIR = FORGE_ROOT / 'pre-commit' / 'blocks'
GENERATOR = FORGE_ROOT / 'pre-commit' / 'scripts' / 'generate_config.py'


def run_generator(repo_dir: Path, detected: str) -> str:
    """Run the generator and return the generated config."""
    result = subprocess.run(
        ['python3', str(GENERATOR), str(BLOCKS_DIR), detected],
        cwd=repo_dir,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, f'Generator failed: {result.stderr}'
    return (repo_dir / '.pre-commit-config.yaml').read_text()


def get_hook_ids(config: str) -> list[str]:
    """Extract all hook IDs from a config string."""
    return re.findall(r'^\s+-\s*id:\s*(\S+)', config, re.MULTILINE)


def get_generated_blocks(config: str) -> list[str]:
    """Extract generated block names from a config string."""
    return re.findall(r'^# generated:(\S+)', config, re.MULTILINE)


def test_python_repo():
    """Python repo gets python blocks, not go/vue/docker."""
    with tempfile.TemporaryDirectory() as tmp:
        config = run_generator(Path(tmp), 'python')
        blocks = get_generated_blocks(config)

        assert 'conventional-commits' in blocks
        assert 'file-checks' in blocks
        assert 'python-format' in blocks
        assert 'python-lint' in blocks
        assert 'go' not in blocks
        assert 'vue' not in blocks
        assert 'docker' not in blocks

        hooks = get_hook_ids(config)
        assert 'ruff-format' in hooks
        assert 'ruff-check' in hooks
        assert 'mypy' in hooks
        assert 'uv-lock' in hooks


def test_go_repo():
    """Go repo gets go blocks, not python."""
    with tempfile.TemporaryDirectory() as tmp:
        config = run_generator(Path(tmp), 'go')
        blocks = get_generated_blocks(config)

        assert 'go' in blocks
        assert 'python-format' not in blocks
        assert 'python-lint' not in blocks

        hooks = get_hook_ids(config)
        assert 'go-fumpt-repo' in hooks
        assert 'golangci-lint-repo-mod' in hooks


def test_full_stack_repo():
    """Repo with everything gets all blocks."""
    with tempfile.TemporaryDirectory() as tmp:
        config = run_generator(Path(tmp), 'python,go,vue,docker,actions,terraform')
        blocks = get_generated_blocks(config)

        assert 'python-format' in blocks
        assert 'python-lint' in blocks
        assert 'go' in blocks
        assert 'vue' in blocks
        assert 'docker' in blocks
        assert 'github-actions' in blocks
        assert 'terraform' in blocks


def test_generic_only_repo():
    """Repo with no detected stack gets only generic blocks."""
    with tempfile.TemporaryDirectory() as tmp:
        config = run_generator(Path(tmp), '')
        blocks = get_generated_blocks(config)

        assert 'conventional-commits' in blocks
        assert 'file-checks' in blocks
        assert 'markdown' in blocks
        assert 'shell' in blocks
        assert 'codespell' in blocks
        assert 'python-format' not in blocks
        assert 'go' not in blocks


def test_no_duplicate_hook_ids():
    """No hook ID should appear twice in any generated config."""
    stacks = ['python', 'go', 'python,vue,docker,actions,terraform', '']
    for stack in stacks:
        with tempfile.TemporaryDirectory() as tmp:
            config = run_generator(Path(tmp), stack)
            hooks = get_hook_ids(config)
            dupes = [h for h in set(hooks) if hooks.count(h) > 1]
            assert not dupes, f'Duplicate hooks for stack "{stack}": {dupes}'


def test_custom_hooks_survive_roundtrip():
    """Custom markers are preserved through generate → regenerate."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)

        # Write initial config with custom markers
        (tmp / '.pre-commit-config.yaml').write_text(
            '# > custom:before:file-checks - Stats capture\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: devstats-capture\n'
            '        name: devstats capture\n'
            '\n'
            '# > custom:after:all - Tests\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: pytest-results\n'
        )

        config1 = run_generator(tmp, 'python')
        config2 = run_generator(tmp, 'python')

        assert config1 == config2, 'Roundtrip should produce identical output'
        assert 'devstats-capture' in config1
        assert 'pytest-results' in config1

        hooks = get_hook_ids(config1)
        assert hooks.index('devstats-capture') < hooks.index('check-yaml')
        assert hooks[-1] == 'pytest-results'


def test_custom_hooks_dedup_standard():
    """Custom hook with same ID as standard removes the standard one."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)

        (tmp / '.pre-commit-config.yaml').write_text(
            '# > custom:after:python-lint - Custom mypy\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: mypy\n'
            '        name: custom mypy\n'
            '        entry: uv run mypy custom-dir\n'
        )

        config = run_generator(tmp, 'python')
        hooks = get_hook_ids(config)

        assert hooks.count('mypy') == 1
        assert 'custom-dir' in config


def test_safety_aborts_on_unknown_hooks():
    """Generator refuses to overwrite config with unrecognized hooks and no markers."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)

        (tmp / '.pre-commit-config.yaml').write_text(
            'repos:\n'
            '  - repo: local\n'
            '    hooks:\n'
            '      - id: check-yaml\n'
            '      - id: my-custom-thing\n'
            '      - id: another-custom\n'
        )

        result = subprocess.run(
            ['python3', str(GENERATOR), str(BLOCKS_DIR), 'python'],
            cwd=tmp,
            capture_output=True,
            text=True,
        )
        assert result.returncode == 1
        assert 'ABORT' in result.stdout
        assert 'my-custom-thing' in result.stdout


if __name__ == '__main__':
    test_python_repo()
    test_go_repo()
    test_full_stack_repo()
    test_generic_only_repo()
    test_no_duplicate_hook_ids()
    test_custom_hooks_survive_roundtrip()
    test_custom_hooks_dedup_standard()
    test_safety_aborts_on_unknown_hooks()
    print('all tests passed')
