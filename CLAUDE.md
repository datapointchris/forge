# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Forge

Forge is a Go CLI tool that serves as an internal developer platform (IDP) for managing a portfolio of git repositories. It provides cross-project status views, planning directory sync, command execution across repos, reusable maintenance scripts ("dies"), and a composable pre-commit standardization system.

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

- `status` — cross-project status view: descriptions from repos.json, status.md content (printed verbatim — the docs are lean current-state snapshots, not changelogs), design doc listings from `.planning/` directories. Planning is resolved through each repo's own `.planning` symlink, never by joining `~/dev/repos/<name>/planning` — repo names are not unique, and name-keying attributed one repo's docs to another. A `.planning` that is a real directory instead of a symlink still renders but is reported as unsynced, since Syncthing never sees it. Flags: `--all` (include description-only repos), `--json` (machine-readable), `-F` (filter repos)
- `brief` — one-page cross-project work brief for an AI coding session. Composes three layers: planning (each repo's status.md + design docs, reusing `status`'s collector), roadmap (ordered `icb` projects + open items), and todos (the Computer-category `icb` task list surfaced as a capture inbox to triage into a project, plus open GitHub issues per repo via `gh`). The `icb`/`gh` layers degrade to warnings when a tool is missing or unauthenticated. Flags: `--json`, `-F`, `--no-issues`, `--no-tasks`.
  A fourth section, **Possibly done**, cross-checks the two: an open roadmap item carrying a `repo` is flagged when that repo's `status.md` has a line reporting completion *and* naming at least two of the item's distinctive words. It quotes the line as evidence rather than asserting a verdict, because the check is a heuristic. Two tuning decisions came from live false positives: words drawn from the completion vocabulary itself (`done`, `shipped`) are excluded from an item's distinctive terms, and one shared word is never enough. **This is the AI's dev brief across repos — deliberately NOT the human single pane of glass (`menu dashboard` in dotfiles, a glance across all life apps: tasks/habits/books/learning). Two audiences, two scopes; do not extend one to cover the other.**
- `exec` — run an inline command or script file across repos
- `dies` — manage and run dies (reusable scripts with metadata and stats tracking)
  - Subcommands: `list`, `run`, `show`, `search`, `stats`
- `precommit generate` — generate `.pre-commit-config.yaml` from standard blocks (Go implementation)
- `ci generate` — generate `.github/workflows/validate.yml` from standard CI blocks
- `version` — print version, commit, and build date (set via ldflags)
- `update` — self-update from GitHub releases (downloads pre-built binary, atomic swap)

**Embedded assets** — dies, pre-commit blocks, configs, and scripts are embedded into the binary via `//go:embed` in `embed.go` at the repo root. The binary is self-contained — no repo clone needed.

**Dual-mode operation:**

- **Embedded mode** (default): binary uses embedded assets. Die scripts are extracted to temp files for execution. `FORGE_DATA_DIR` env var points to extracted pre-commit assets.
- **Filesystem mode** (development): when `FORGE_DIES_DIR` env var is set, dies and assets are read from disk. Use direnv (`.envrc` in repo root) for automatic setup. Scripts reference assets via `dirname $0` resolution.

**Packages** (at repo root):

- `config` — loads two config files:
  - **Forge config** (`~/.config/forge/config.toml`, TOML): `repos_file` pointing to the repo registry
  - **Repo registry** (`~/dev/repos.json`, JSON): defines repos with `name`, `path`, `status` (`active`/`dormant`/`retired`), and optional `description` and `owner`. An `owner` marks a third-party reference clone — a repo cloned for reading, never for cross-repo work.
  - **`toolchain`** on a repo entry declares its build surface: `components` (a `stack` plus the `dir` it lives in) and `sql_dialect`. Declared, never detected — the portfolio has five conventions for where a Go service lives (`api/`, `cli/`, root, and two legacy shapes), and a fact like the SQL dialect is not derivable from a layout at any level of tidiness. A repo can hold several components of one stack: nomad's `api/` and `cli/` are both Go modules, deliberately isolated. `config.FindRepoByPath` resolves a working directory to its entry, so a generator run anywhere inside a repo finds its declaration.

  - The `-c` persistent flag overrides the repos file path. `FORGE_DIES_DIR` env var enables filesystem mode for development.
- `dies` — registry (`LoadRegistry` accepts `fs.FS` — works with `os.DirFS`, `embed.FS`, or test fakes) and stats (JSONL append log at `~/.local/share/forge/stats.jsonl`). Also contains the bash die scripts in category subdirectories.
- `runner` — executes commands in each repo directory, handles output capture, colored results, filtering, and env var injection
- `assets` — extracts embedded assets to temp directories for shell execution, manages cleanup
- `precommit` — Go implementation of config generation (block composition, custom section preservation, hook deduplication, safety checks)
- `ci` — generates the baseline validation workflow, reusing `precommit`'s block composition and custom-section markers
- `toolchain` — the version manifest shared by both generators

**Repo selection** — every command resolves its repos through `runner.SelectRepos(repos, names)`. With no `-F` it returns the active portfolio and **excludes reference clones**: no implicit operation should ever write to a repo we don't own. Naming repos explicitly with `-F` overrides that, so a clone stays reachable on purpose. Retired repos are excluded either way.

**Data flow for `dies run`:** determine asset source (embedded or `FORGE_DIES_DIR`) → load registry from `fs.FS` → validate die exists → extract script to temp file if embedded → load repo registry → `SelectRepos` (retired and reference clones dropped unless named) → execute script in each repo via bash (with `FORGE_DATA_DIR` if embedded) → print colored results → append stats record → cleanup temp files.

## Die Scripts

Dies are bash scripts in `dies_dir`, organized by category subdirectory. Exit code conventions:

- **0** = OK (success)
- **2** = SKIP (nothing to do — the `ExitSkip` constant in `runner/runner.go`)
- **anything else** = FAIL

Optional metadata lives in `dies/registry.yml` with `description` and `tags` per die.

**Categories:**

- `checks/` — scorecard dies (has-pre-commit, has-claude-md, has-clean-gitignore, has-planning-dir, planning-docs)
- `maintenance/` — golden path enforcement (sync-pre-commit, sync-ci, sync-planning, pre-commit-update, add-planning-to-gitignore, rename-master-to-main)
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

**`pre-commit/toolchain.yml`** — the single source of truth for every pinned tool version. Blocks keep their own `rev:` lines so each stays readable and valid standalone, but generation overwrites them from the manifest — the manifest wins. `TestToolchainManagesEveryBlockRepo` fails if a block names a remote repo the manifest doesn't pin, so a new block cannot introduce an unmanaged version.

The manifest's `version` is stamped into every generated config as a `# forge-toolchain: N` first line. That stamp is what makes staged rollout possible: bump a tool and the version, resync one repo, verify, then fan out — and `rg '^# forge-toolchain:' ` across the portfolio answers which repos are current. Bump `version` on any rev change.

**`pre-commit/configs/`** — standard tool config templates deployed alongside the pre-commit config:

- `markdownlint.json` — all repos
- `golangci.yml` — Go repos
- `prettierrc.json` — Vue repos
- `stylelintrc.json` — Vue repos
- `pyproject-tools.toml` — merged into Python repos' pyproject.toml (ruff, mypy, pyright, codespell, pytest)

**Config generation** — now a Go function in `precommit/generate.go`, invoked via `forge precommit generate --detected <stack>`. The `sync-pre-commit.sh` die calls this instead of the Python script. Handles block composition, custom section preservation, hook deduplication, and safety checks.

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

## CI Standardization System

`ci/blocks/` holds per-stack step fragments composed into `.github/workflows/validate.yml`, triggered
on `pull_request` and exposed via `workflow_call` so a release workflow can `needs:` it. Action and
`go install` versions come from the same `toolchain.yml` as the pre-commit hooks, and the same
`# > custom:` markers preserve repo-specific steps.

**One job per declared component**, named `<stack>` at the root or `<stack>-<dir>` below it, each
with a `working-directory`. nomad generates `go-api`, `go-cli`, and `vue-web` running in parallel —
a single serial job would hide which module failed and force them to share one setup step. A
declared stack with no CI block yet (docker, terraform are pre-commit concerns today) is skipped
rather than emitting an empty job, so the registry stays free to declare more than CI can build.

The output is **`validate.yml`, not `ci.yml`** — several repos hand-wrote a `ci.yml` long before this
existed (nomad's is a multi-job pipeline with working directories and image env), and generating over
one would destroy work nothing could recover. `ci.Run` aborts on any `validate.yml` lacking the
`# forge-toolchain:` header for the same reason. Bespoke pipelines stay as separate workflow files;
the generated one is additive.

**`dies/maintenance/sync-ci.sh`** — generates the workflow, exits SKIP when current or when no
supported stack is detected.

**`dies/maintenance/sync-pre-commit.sh`** — the main die that orchestrates everything. Detects tech stack, generates config, deploys tool configs, merges pyproject.toml. Idempotent — exits with SKIP when nothing changed.

## Testing

**Go tests** (`go test ./...`):

- `config/` — forge and syncer config loading
- `dies/` — registry and stats, plus integration tests for sync-pre-commit die (9 tests covering tech detection, dedup, custom preservation, safety, config deployment)
- `precommit/` — config generator: 9 unit tests (using `fstest.MapFS`) + 7 integration tests against real blocks
- `runner/` — repo filtering, execution

**Note:** The sync-pre-commit integration tests (`dies/sync_precommit_test.go`) require the `forge` binary on PATH since the die script calls `forge precommit generate`. Run `go install .` before `go test ./...`.

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
