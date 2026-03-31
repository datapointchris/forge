# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Forge

Forge is a Go CLI tool that runs commands and scripts ("dies") across multiple git repositories. It reads a repo list from a syncer config and executes operations in each repo's working directory.

## Commands

```bash
go build -o forge .        # Build binary
go test ./...              # Run all tests
golangci-lint run          # Lint (13 linters, see .golangci.yml)
go run . <subcommand>      # Run without building
```

Pre-commit hooks run gofumpt, go vet, go build, go test, golangci-lint, and enforce conventional commits on every commit.

## Architecture

**CLI layer** (`cmd/`) uses Cobra. Two top-level commands:

- `exec` — run an inline command or script file across repos
- `dies` — manage and run dies (reusable scripts with metadata and stats tracking)
  - Subcommands: `list`, `run`, `show`, `search`, `stats`

**Internal packages** (`internal/`):

- `config` — loads two config files:
  - **Forge config** (`~/.config/forge/config.yml`, YAML): points to the `dies_dir` where die scripts live
  - **Syncer config** (`~/.config/syncer/datapointchris.json`, JSON): defines the list of repos (`name` + `path` pairs)
  - The `-c` persistent flag overrides the syncer config path only
- `dies` — registry (filesystem scan of dies_dir, merged with optional `registry.yml` metadata) and stats (JSONL append log at `~/.local/share/forge/stats.jsonl`)
- `runner` — executes commands in each repo directory, handles output capture, colored results, and filtering

**Data flow for `dies run`:** load forge config → find dies_dir → load registry → validate die exists → load syncer config → get repos → filter by `-F` flag → execute script in each repo via bash → print colored results → append stats record.

## Die Scripts

Dies are bash scripts in `dies_dir`, organized by category subdirectory (`checks/`, `maintenance/`, `onetime/`). Exit code conventions:

- **0** = OK (success)
- **2** = SKIP (nothing to do — the `ExitSkip` constant in `runner/runner.go`)
- **anything else** = FAIL

Optional metadata lives in `dies/registry.yml` with `description` and `tags` per die.

## Key Patterns

- Repos must have a `.git/` directory to be valid execution targets
- `FilterRepos()` does exact name matching; empty filter = all repos
- Output uses `github.com/fatih/color` with nerd font icons (✔ ⚠ ✘)
- `ExpandTilde()` supports `~` and `~/path` only, not `~user/path`
- Stats are JSONL (one JSON object per line), malformed lines silently skipped for crash resilience
