#!/usr/bin/env python3
"""Generate .pre-commit-config.yaml from standard blocks and custom hooks.

Standard blocks are YAML fragments in the blocks directory, wrapped with:
  # generated:BLOCK_NAME - Description

Custom hooks are preserved from the existing config via markers:
  # > custom:before:BLOCK_NAME - Description
  # > custom:after:BLOCK_NAME - Description
  # > custom:after:all - Description

Each custom marker starts a section. The section ends at the next marker
(custom or generated) or end of file.
"""

import re
import sys
from pathlib import Path

MARKER_RE = re.compile(r'^# > custom:(before|after):(\S+)')
GENERATED_RE = re.compile(r'^# generated:(\S+)')


def extract_custom_sections(config_path: Path) -> dict[str, str]:
    """Extract custom hook sections from existing config, keyed by position."""
    if not config_path.exists():
        return {}

    text = config_path.read_text()
    sections: dict[str, str] = {}
    current_key = None
    current_lines: list[str] = []

    for line in text.splitlines():
        is_custom = MARKER_RE.match(line)
        is_generated = GENERATED_RE.match(line)

        if is_custom or is_generated:
            # Save previous custom section if any
            if current_key is not None:
                # Strip trailing blank lines
                while current_lines and current_lines[-1].strip() == '':
                    current_lines.pop()
                sections[current_key] = '\n'.join(current_lines)

            if is_custom:
                direction = is_custom.group(1)
                block_name = is_custom.group(2)
                current_key = f'{direction}:{block_name}'
                current_lines = [line]
            else:
                current_key = None
                current_lines = []
        elif current_key is not None:
            current_lines.append(line)

    # Save final custom section
    if current_key is not None:
        while current_lines and current_lines[-1].strip() == '':
            current_lines.pop()
        sections[current_key] = '\n'.join(current_lines)

    return sections


def get_block_name(filename: str) -> str:
    """Extract block name from filename: '05-file-checks.yml' -> 'file-checks'."""
    return re.sub(r'^\d+-', '', Path(filename).stem)


def get_block_description(content: str) -> str:
    """Extract description from block's first comment line."""
    for line in content.splitlines():
        line = line.strip()
        if line.startswith('# '):
            return line[2:]
    return ''


def should_include_block(block_name: str, detected: set[str]) -> bool:
    """Check if a block should be included based on detected tech stack."""
    category_map = {
        'python-format': 'python',
        'python-lint': 'python',
        'go': 'go',
        'vue': 'vue',
        'docker': 'docker',
        'github-actions': 'actions',
        'terraform': 'terraform',
    }
    required = category_map.get(block_name)
    return required is None or required in detected


def get_custom_hook_ids(custom_sections: dict[str, str]) -> set[str]:
    """Collect all hook IDs defined in custom sections."""
    ids = set()
    for content in custom_sections.values():
        for line in content.splitlines():
            match = re.match(r'^\s+-\s*id:\s*(\S+)', line)
            if match:
                ids.add(match.group(1))
    return ids


def strip_hooks_from_block(content: str, hook_ids: set[str]) -> str:
    """Remove hooks with given IDs from a block's YAML content.

    Relies on the consistent block format where each hook starts with
    '      - id:' (6-space indent) within a hooks list.
    """
    if not hook_ids:
        return content

    result_lines: list[str] = []
    skip = False

    for line in content.splitlines():
        match = re.match(r'^(\s+)-\s*id:\s*(\S+)', line)
        if match:
            skip = match.group(2) in hook_ids
        elif skip and (not line.strip() or re.match(r'^\s+-\s*(id:|repo:)', line) or re.match(r'^\s+#', line)):
            # New hook, new repo, comment, or blank line — stop skipping
            if re.match(r'^\s+-\s*id:', line):
                skip = re.match(r'^\s+-\s*id:\s*(\S+)', line).group(1) in hook_ids
            else:
                skip = False

        if not skip:
            result_lines.append(line)

    return '\n'.join(result_lines)


def generate_config(
    blocks_dir: Path,
    detected: set[str],
    custom_sections: dict[str, str],
) -> str:
    lines = [
        'fail_fast: true',
        'default_stages: [pre-commit]',
        'repos:',
    ]

    # Collect hook IDs from custom sections to avoid duplicates in standard blocks
    custom_ids = get_custom_hook_ids(custom_sections)

    block_files = sorted(blocks_dir.glob('[0-9]*.yml'))
    applicable_blocks = []

    for block_file in block_files:
        name = get_block_name(block_file.name)
        if should_include_block(name, detected):
            applicable_blocks.append((name, block_file))

    for name, block_file in applicable_blocks:
        content = block_file.read_text().rstrip()
        content = strip_hooks_from_block(content, custom_ids)
        description = get_block_description(content)

        # Insert custom hooks that go BEFORE this block
        before_key = f'before:{name}'
        if before_key in custom_sections:
            lines.append('')
            lines.append(custom_sections[before_key])

        # Insert the standard block (strip leading comment since description is in the header)
        stripped = content
        if description:
            stripped_lines = content.splitlines()
            if stripped_lines and stripped_lines[0].strip() == f'# {description}':
                stripped = '\n'.join(stripped_lines[1:])
        lines.append('')
        lines.append(f'# generated:{name} - {description}')
        lines.append(stripped)

        # Insert custom hooks that go AFTER this block
        after_key = f'after:{name}'
        if after_key in custom_sections:
            lines.append('')
            lines.append(custom_sections[after_key])

    # Insert custom hooks that go after everything
    if 'after:all' in custom_sections:
        lines.append('')
        lines.append(custom_sections['after:all'])

    lines.append('')
    return '\n'.join(lines)


def get_existing_hook_ids(config_path: Path) -> set[str]:
    """Extract all hook IDs from an existing pre-commit config."""
    if not config_path.exists():
        return set()
    ids = set()
    for line in config_path.read_text().splitlines():
        match = re.match(r'^\s+-\s*id:\s*(\S+)', line)
        if match:
            ids.add(match.group(1))
    return ids


def get_standard_hook_ids(blocks_dir: Path) -> set[str]:
    """Extract all hook IDs from standard blocks, plus hooks we intentionally replace."""
    ids = set()
    for block_file in blocks_dir.glob('[0-9]*.yml'):
        for line in block_file.read_text().splitlines():
            match = re.match(r'^\s+-\s*id:\s*(\S+)', line)
            if match:
                ids.add(match.group(1))
    # Hooks that ruff replaces — recognize them so we don't abort on repos that still have them
    ids.update({'bandit', 'pyupgrade', 'refurb', 'prepare-commit-msg'})
    return ids


def main() -> int:
    if len(sys.argv) < 3:
        print(f'Usage: {sys.argv[0]} <blocks_dir> <detected_stack>')
        return 1

    blocks_dir = Path(sys.argv[1])
    detected = set(sys.argv[2].split(',')) if sys.argv[2] else set()

    config_path = Path('.pre-commit-config.yaml')
    custom_sections = extract_custom_sections(config_path)

    # Safety: refuse to overwrite configs with unrecognized hooks unless they have markers
    if config_path.exists() and not custom_sections:
        existing = get_existing_hook_ids(config_path)
        standard = get_standard_hook_ids(blocks_dir)
        unknown = existing - standard
        if unknown:
            print(f'ABORT: {len(unknown)} unrecognized hooks with no custom markers: {", ".join(sorted(unknown))}')
            print('Add # > custom:POSITION markers to preserve them, then re-run.')
            return 1

    config = generate_config(blocks_dir, detected, custom_sections)
    config_path.write_text(config)

    custom_count = len(custom_sections)
    if custom_count:
        print(f'{custom_count} custom sections preserved')

    return 0


if __name__ == '__main__':
    sys.exit(main())
