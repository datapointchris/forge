# Forge

Run commands and reusable scripts across multiple git repositories.

Forge reads a repo list from config and executes operations in each repo's working directory — either ad-hoc commands or managed scripts called **dies**.

## Installation

### From GitHub Releases

Download the latest binary from [Releases](https://github.com/datapointchris/forge/releases). Builds are available for linux and darwin on amd64 and arm64.

### From Source

```bash
go install github.com/datapointchris/forge@latest
```

## Configuration

Forge uses two config files:

### Forge Config

`~/.config/forge/config.yml` — points to the directory containing die scripts.

```yaml
dies_dir: ~/tools/forge/dies
```

### Syncer Config

`~/.config/syncer/datapointchris.json` — defines the repos forge operates on. Override with `-c`.

```json
{
  "owner": "datapointchris",
  "host": "https://github.com",
  "search_paths": ["~/code"],
  "repos": [
    {"name": "forge", "path": "~/tools/forge"},
    {"name": "dotfiles", "path": "~/dotfiles"}
  ]
}
```

## Usage

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
