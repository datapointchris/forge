# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Forge

Forge is a Go CLI tool that runs commands and scripts ("dies") across multiple git repositories. It reads a repo list from a syncer config and executes operations in each repo's working directory. It also manages a composable pre-commit standardization system.

## Commands

```bash
go build -o forge .        # Build binary
go test ./...              # Run all tests (includes Go integration tests for dies)
golangci-lint run          # Lint (13 linters, see .golangci.yml)
go run . <subcommand>      # Run without building
```

Pre-commit hooks run gofumpt, go vet, go build, go test, golangci-lint, shellcheck, codespell, and enforce conventional commits on every commit. A custom hook runs the Python test suite when `pre-commit/` files change.

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

Dies are bash scripts in `dies_dir`, organized by category subdirectory. Exit code conventions:

- **0** = OK (success)
- **2** = SKIP (nothing to do — the `ExitSkip` constant in `runner/runner.go`)
- **anything else** = FAIL

Optional metadata lives in `dies/registry.yml` with `description` and `tags` per die.

**Categories:**

- `checks/` — scorecard dies (has-pre-commit, has-claude-md, has-clean-gitignore, has-planning-dir, planning-docs)
- `maintenance/` — golden path enforcement (sync-pre-commit, pre-commit-update, add-planning-to-gitignore)
- `onetime/` — one-shot migrations

## Pre-commit Standardization System

Forge includes a composable system for generating standardized `.pre-commit-config.yaml` across all repos.

**`pre-commit/blocks/`** — numbered YAML fragments composed based on detected tech stack:

- Generic (all repos): conventional-commits, file-checks, markdown, shell, codespell
- Python: python-format (uv-lock, ruff-format), python-lint (ruff-check with 20 rule sets replacing bandit/pyupgrade/refurb, mypy via uv run)
- Go: gofumpt, go-vet, go-build, go-mod-tidy, go-test, golangci-lint
- Vue: eslint, prettier, stylelint, typecheck
- Docker: hadolint
- GitHub Actions: actionlint
- Terraform: validate, tflint, fmt, docs

**`pre-commit/configs/`** — standard tool config templates deployed alongside the pre-commit config:

- `markdownlint.json` — all repos
- `golangci.yml` — Go repos
- `prettierrc.json` — Vue repos
- `stylelintrc.json` — Vue repos
- `pyproject-tools.toml` — merged into Python repos' pyproject.toml (ruff, mypy, pyright, codespell, pytest)

**`pre-commit/scripts/`** — Python helper scripts:

- `generate_config.py` — generates .pre-commit-config.yaml from blocks, preserves custom markers, deduplicates overlapping hooks
- `merge_pyproject_tools.py` — merges standard tool sections into pyproject.toml using tomlkit (replace sections like pyright/ruff, merge sections like codespell/pytest)

**Custom hook markers** — repos with project-specific hooks use these markers in their `.pre-commit-config.yaml`:

```yaml
# > custom:before:file-checks - Description
# > custom:after:vue - Description
# > custom:after:all - Description
```

The generator preserves these across re-runs. A safety check aborts if unrecognized hooks exist without markers.

**`dies/maintenance/sync-pre-commit.sh`** — the main die that orchestrates everything. Detects tech stack, generates config, deploys tool configs, merges pyproject.toml. Idempotent — exits with SKIP when nothing changed.

## Testing

**Go tests** (`go test ./...`):

- `internal/config/` — forge and syncer config loading
- `internal/dies/` — registry and stats, plus integration tests for sync-pre-commit die (9 tests covering tech detection, dedup, custom preservation, safety, config deployment)
- `internal/runner/` — repo filtering, execution

**Python tests** (`pre-commit/scripts/run_tests.sh`):

- `test_generate_config.py` — 8 unit tests for the generator
- `test_merge_pyproject_tools.py` — 5 unit tests for pyproject merge
- `test_integration.py` — 10 integration tests running the generator end-to-end

Python tests run as a pre-commit hook on files matching `^pre-commit/`.

## Build and Release

- `.goreleaser.yaml` — goreleaser config for cross-platform binary releases
- `.github/workflows/release.yml` — GitHub Actions release workflow triggered by version tags
- Installed via `go install github.com/datapointchris/forge@latest` or dotfiles `go-tools.sh`

## Key Patterns

- Repos must have a `.git/` directory to be valid execution targets
- `FilterRepos()` does exact name matching; empty filter = all repos
- Output uses `github.com/fatih/color` with nerd font icons (✔ ⚠ ✘)
- `ExpandTilde()` supports `~` and `~/path` only, not `~user/path`
- Stats are JSONL (one JSON object per line), malformed lines silently skipped for crash resilience

## Known Issue

The binary installed via `go install` depends on the repo clone at `~/tools/forge/` for die scripts, blocks, and Python scripts. Plan in `.planning/embed-and-self-update.md` to fix with `go:embed` and a `forge update` command.
