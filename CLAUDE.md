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

**CLI layer** (`cmd/`) uses Cobra. Top-level commands:

- `exec` — run an inline command or script file across repos
- `dies` — manage and run dies (reusable scripts with metadata and stats tracking)
  - Subcommands: `list`, `run`, `show`, `search`, `stats`
- `precommit generate` — generate `.pre-commit-config.yaml` from standard blocks (Go implementation)
- `version` — print version, commit, and build date (set via ldflags)
- `update` — self-update from GitHub releases (downloads pre-built binary, atomic swap)

**Embedded assets** — dies, pre-commit blocks, configs, and scripts are embedded into the binary via `//go:embed` in `embed.go` at the repo root. The binary is self-contained — no repo clone needed.

**Dual-mode operation:**

- **Embedded mode** (default): binary uses embedded assets. Die scripts are extracted to temp files for execution. `FORGE_DATA_DIR` env var points to extracted pre-commit assets.
- **Filesystem mode** (development): when `FORGE_DIES_DIR` env var is set, dies and assets are read from disk. Use direnv (`.envrc` in repo root) for automatic setup. Scripts reference assets via `dirname $0` resolution.

**Internal packages** (`internal/`):

- `config` — loads two config files:
  - **Forge config** (`~/.config/forge/config.toml`, TOML): `repos_file` pointing to the repo registry
  - **Repo registry** (`~/dev/repos.json`, JSON): defines repos with `name`, `path`, and `status` (`active`/`dormant`/`retired`)
  - The `-c` persistent flag overrides the repos file path. `FORGE_DIES_DIR` env var enables filesystem mode for development.
- `dies` — registry (`LoadRegistry` accepts `fs.FS` — works with `os.DirFS`, `embed.FS`, or test fakes) and stats (JSONL append log at `~/.local/share/forge/stats.jsonl`)
- `runner` — executes commands in each repo directory, handles output capture, colored results, filtering, and env var injection
- `assets` — extracts embedded assets to temp directories for shell execution, manages cleanup
- `precommit` — Go implementation of config generation (block composition, custom section preservation, hook deduplication, safety checks)

**Data flow for `dies run`:** determine asset source (embedded or `FORGE_DIES_DIR`) → load registry from `fs.FS` → validate die exists → extract script to temp file if embedded → load repo registry → get repos → filter retired → filter by `-F` flag → execute script in each repo via bash (with `FORGE_DATA_DIR` if embedded) → print colored results → append stats record → cleanup temp files.

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

**Config generation** — now a Go function in `internal/precommit/generate.go`, invoked via `forge precommit generate --detected <stack>`. The `sync-pre-commit.sh` die calls this instead of the Python script. Handles block composition, custom section preservation, hook deduplication, and safety checks.

**`pre-commit/scripts/`** — Python helper scripts (embedded in binary):

- `generate_config.py` — legacy Python generator (replaced by Go implementation, kept as reference)
- `merge_pyproject_tools.py` — merges standard tool sections into pyproject.toml using tomlkit (no Go equivalent for lossless TOML editing)

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
- `internal/precommit/` — config generator: 9 unit tests (using `fstest.MapFS`) + 7 integration tests against real blocks
- `internal/runner/` — repo filtering, execution

**Note:** The sync-pre-commit integration tests (`internal/dies/sync_precommit_test.go`) require the `forge` binary on PATH since the die script calls `forge precommit generate`. Run `go install .` before `go test ./...`.

**Python tests** (`pre-commit/scripts/run_tests.sh`):

- `test_generate_config.py` — 8 unit tests for the legacy Python generator
- `test_merge_pyproject_tools.py` — 5 unit tests for pyproject merge
- `test_integration.py` — 10 integration tests for the legacy Python generator

Python tests run as a pre-commit hook on files matching `^pre-commit/`.

## Build and Release

- `.goreleaser.yaml` — goreleaser config with ldflags injecting version/commit/date into the binary
- `.github/workflows/release.yml` — GitHub Actions release workflow triggered by version tags
- Installed via `go install github.com/datapointchris/forge@latest` or dotfiles `go-tools.sh`
- `forge update` — self-updates by downloading the latest release binary from GitHub (no Go toolchain needed)
- `forge version` — shows version, commit SHA, and build date (`dev` when built without ldflags)

## Key Patterns

- Repos must have a `.git/` directory to be valid execution targets
- `FilterRepos()` does exact name matching; empty filter = all repos
- Output uses `github.com/fatih/color` with nerd font icons (✔ ⚠ ✘)
- `ExpandTilde()` supports `~` and `~/path` only, not `~user/path`
- Stats are JSONL (one JSON object per line), malformed lines silently skipped for crash resilience

## Embedded Assets

All die scripts, pre-commit blocks, configs, and Python scripts are embedded into the binary via `//go:embed` directives in `embed.go`. By default, the binary uses embedded assets. Set `FORGE_DIES_DIR` env var to use filesystem assets during development (the `.envrc` in the repo root does this automatically via direnv).
