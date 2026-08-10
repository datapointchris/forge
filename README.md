# Forge

Run commands and reusable scripts across multiple git repositories.

Forge reads a repo list from config and executes operations in each repo's working directory — either ad-hoc commands or managed scripts called **dies**.

**Forge's unit of work is one repo.** Running an operation across the whole portfolio is machinery
for reaching many repos, not a different kind of operation. Questions about the fleet as a whole
belong to [fleet](https://github.com/datapointchris/fleet), whose unit is the fleet itself.

The test is what a command operates on, not whether it writes: a per-repo read still belongs here,
and a fleet-level question belongs in fleet even if answering it means writing something.

`status` and `brief` predate that line and are on the wrong side of it — both answer a question
about the portfolio, and `fleet status` already covers the first.

## Installation

### From GitHub Releases

Download the latest binary from [Releases](https://github.com/datapointchris/forge/releases). Builds are available for linux and darwin on amd64 and arm64.

### From Source

```bash
go install github.com/datapointchris/forge/v6@latest
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

Dies are Go, compiled into the binary, so a development build is the current dies — there is no filesystem mode to switch into.

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

Valid statuses: `active` (the default when the field is absent), `dormant`, `retired`. Only `active` repos are swept implicitly — `dormant` is reachable by naming it with `-F`, `retired` is not reachable at all. The optional `description` field is shown by `forge status`.

### Machine config

`$XDG_CONFIG_HOME/forge/config.yml` — the directories forge maintains that git does
not version. A Syncthing folder, a home directory, anything held to the same
standard without a remote.

These are declared here rather than in the registry on purpose. A repo registry is
usually read by more than one tool, and each of them takes an entry to be a git
repo with a remote — a key only forge can act on does not just add a concept the
others ignore, it changes what iterating the collection means for all of them.

```yaml
maintained_directories:
  - name: claude
    path: ~/.claude
    description: Editor and agent configuration. Synced, not versioned.
    toolchain:
      components:
        - {stack: python, dir: .}
        - {stack: shell, dir: .}

  # An empty components list is not the same as none: it asks for the generic
  # blocks and nothing else, where omitting toolchain skips the directory.
  - name: notes
    path: ~/notes
    toolchain:
      components: []
```

`forge config show` prints what resolved and which layer set it. A missing file is not
an error, and an unknown key is — a misspelled key would leave a directory
undeclared, and an undeclared directory reads as a converged one.

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

### Reconcile the repos

Three verbs over one measurement, Terraform-shaped. `plan` is `apply` minus its last
step — the same walk, stopping before the write — so there is no `--dry-run` for
`apply` to be the opposite of.

```bash
# What is wrong: findings apply cannot fix
forge repos check

# What apply would change, writing nothing
forge repos plan
forge repos plan precommit -F forge,dotfiles

# Make it so. Naming a die applies that one; omitting it applies them all,
# after a confirmation showing the count (--yes to skip, required off a TTY).
forge repos apply gitignore -F refcheck
forge repos apply -F refcheck

# Which repos a verb would visit
forge repos list

# Anything that is not a die
forge repos exec -- git status --short
forge repos exec -f ./one-off.sh
```

Exit codes: `0` converged, `1` changes pending (`plan` only), `2` usage, `3` something
is wrong. `check` never returns 1 — a repo behind the standard is drift, not a fault.

### Reconcile the maintained directories

The same verbs, spelled the same way, over the targets git does not version.

```bash
forge directories list
forge directories check
forge directories plan -F claude
forge directories apply precommit -F claude
```

Dies that read a remote, a branch or a workflow report themselves not-applicable
rather than being hidden, so a row says why it found nothing.

`run` is the verb repos do not have. A repo's config is executed twice already —
by the git hook on every commit, and by CI on every pull request — while a
directory has neither, so its generated config would otherwise never run at all.

```bash
forge directories run
forge directories run -F claude
forge directories run --rebuild --json
```

pre-commit needs a git index to know which files exist. A directory that git does
not version has none, so forge builds a throwaway one in its cache: a bare
repository outside the tree, with the directory as its work tree. Nothing is
written inside the directory itself, because a `.git` in a file-synced folder
conflicts on every peer. Which files are examined is decided the ordinary way, by
the directory's `.gitignore`.

Exit codes: `0` everything passed, `1` a hook failed or rewrote a file, `3` the run
could not happen.

### Browse the dies

`forge dies` is the library and executes nothing.

```bash
forge dies list
forge dies show precommit
forge dies search gitignore
forge dies stats
forge dies stats precommit
```

### The version manifest

```bash
forge toolchain show           # what is pinned now
forge toolchain plan           # what upstream has released since
forge toolchain apply          # take it, and bump the manifest version
```

Then roll out to one repo before fanning out:
`forge repos apply precommit -F <repo>`.

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
