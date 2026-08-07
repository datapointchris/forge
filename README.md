# Forge

Run commands and reusable scripts across multiple git repositories.

Forge reads a repo list from config and executes operations in each repo's working directory — either ad-hoc commands or managed scripts called **dies**.

## Installation

### From GitHub Releases

Download the latest binary from [Releases](https://github.com/datapointchris/forge/releases). Builds are available for linux and darwin on amd64 and arm64.

### From Source

```bash
go install github.com/datapointchris/forge/v3@latest
```

### Self-Update

```bash
forge update
```

Downloads and installs the latest release binary from GitHub. No Go toolchain required.

## Configuration

### Repo Registry

`$XDG_DATA_HOME/forge/repos.json` — defines the repos forge operates on. Override
a single run with `-c <path>`.

Forge reads the registry from its own data directory and does not know where the
file is really maintained; point it at a shared registry with a symlink:

```bash
mkdir -p "${XDG_DATA_HOME:-$HOME/.local/share}/forge"
ln -sfn /path/to/repos.json "${XDG_DATA_HOME:-$HOME/.local/share}/forge/repos.json"
```

For development, set `FORGE_DIES_DIR` to use filesystem dies instead of embedded. The `.envrc` in the repo root handles this via direnv.

```json
{
  "owner": "datapointchris",
  "host": "https://github.com",
  "search_paths": ["~/code"],
  "repos": [
    {"name": "forge", "path": "~/tools/forge", "status": "active", "description": "Go CLI for cross-repo operations."},
    {"name": "old-project", "path": "~/code/old", "status": "retired"}
  ]
}
```

Repos with `"status": "retired"` are skipped. Valid statuses: `active` (default), `dormant`, `retired`. The optional `description` field is shown by `forge status`.

## Usage

### Cross-project status

```bash
# Show repos with planning content (status.md, design docs)
forge status

# Include all active repos (even description-only)
forge status --all

# Filter to specific repos
forge status -F ichrisbirch,homelab

# Machine-readable output
forge status --json
```

`status.md` is printed verbatim — the docs are kept short at the source (a
current-state snapshot, not a changelog), so there is nothing to summarize.

### Session brief

`forge brief` composes a single, text-dense briefing to prime an AI coding
session: each repo's planning status and design docs, your ordered ichrisbirch
project roadmap and open items (via the `icb` CLI), the Computer-category `icb`
task list surfaced as a capture inbox to triage into the right project, and each
repo's open GitHub issues (via `gh`). The `icb` and `gh` layers degrade
gracefully — a missing or unauthenticated tool is noted under warnings rather
than failing the brief.

This is the AI's dev brief across **repos** and computer work. It is a different
tool from the human single pane of glass (`menu dashboard`, in dotfiles), which is
a glance across all life apps — tasks, habits, next book, current learning. Two
audiences, two scopes; neither is built to cover the other.

```bash
# Full brief across all repos with planning content
forge brief

# Filter, or skip the remote layers
forge brief -F dotfiles,indy
forge brief --no-issues --no-tasks

# Machine-readable output
forge brief --json
```

### Sync planning directories

```bash
# Create .planning/ symlinks to ~/dev/repos/ for Syncthing sync
forge dies run maintenance/sync-planning.sh
```

### Execute commands across repos

```bash
# Inline command
forge exec -- git status --short

# Script file
forge exec -f ./cleanup.sh

# Filter to specific repos
forge exec -F dotfiles,homelab -- git pull

# Dry run
forge exec -n -- git status
```

### Manage and run dies

Dies are reusable bash scripts organized by category (subdirectory). They use exit codes to report status: **0** = OK, **2** = skip (nothing to do), anything else = fail.

```bash
# List available dies
forge dies list
forge dies list checks

# Run a die across repos
forge dies run maintenance/add-planning-to-gitignore.sh

# Run on specific repos only
forge dies run checks/pre-commit-config.sh -F forge,dotfiles

# Dry run
forge dies run checks/pre-commit-config.sh -n

# Search dies by name, description, or tags
forge dies search gitignore

# Show details and last run info
forge dies show maintenance/add-planning-to-gitignore.sh

# View execution history
forge dies stats
forge dies stats checks/pre-commit-config.sh
```

### Pre-commit config generation

```bash
# Generate .pre-commit-config.yaml for the current repo's tech stack
forge precommit generate --detected python,go
```

### Version and update

```bash
# Show version info
forge version

# Self-update to latest release
forge update
```

## Writing Dies

Create a bash script in the dies directory under a category subdirectory:

```bash
#!/bin/bash

# Exit 2 to skip (nothing to do)
if [ -f ".tool-versions" ]; then
  echo "already exists"
  exit 2
fi

# Do work...
echo "missing .tool-versions"
exit 1
```

Optionally register metadata in `dies/registry.yml`:

```yaml
dies:
  checks/tool-versions.sh:
    description: "Check that .tool-versions exists in the repo."
    tags: [checks, asdf, setup]
```
