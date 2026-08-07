# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Forge

Forge is a Go CLI tool that serves as an internal developer platform (IDP) for managing a portfolio of git repositories. It provides cross-project status views, planning directory sync, command execution across repos, reusable maintenance scripts ("dies"), and a composable pre-commit standardization system.

## Commands

```bash
go build -o forge .        # Build binary
go test ./...              # Run all tests (includes Go integration tests for dies)
golangci-lint run          # Lint (enabled set is in .golangci.yml)
go run . <subcommand>      # Run without building
```

The hook inventory is generated from `pre-commit/toolchain.yml` — read that, or `.pre-commit-config.yaml`, rather than a list here. `~/dev/standards/ci.md` § "Never restate the hook inventory in a repo's `CLAUDE.md`" is the rule, and this repo owns the generator that makes it enforceable. A custom hook runs the Python test suite when `pre-commit/` files change.

## Architecture

**CLI layer** (`cmd/`) uses Cobra. Top-level commands:

- `status` — cross-project status view: descriptions from repos.json, status.md content (printed verbatim — the docs are lean current-state snapshots, not changelogs), design doc listings from `.planning/` directories. Planning is resolved through each repo's own `.planning` symlink, never by joining `~/dev/repos/<name>/planning` — repo names are not unique, and name-keying attributed one repo's docs to another. A `.planning` that is a real directory instead of a symlink still renders but is reported as unsynced, since Syncthing never sees it. Flags: `--all` (include description-only repos), `--json` (machine-readable), `-F` (filter repos)
- `brief` — one-page cross-project work brief for an AI coding session. Composes three layers: planning (each repo's status.md + design docs, reusing `status`'s collector), roadmap (ordered `icb` projects + open items), and todos (the Computer-category `icb` task list surfaced as a capture inbox to triage into a project, plus open GitHub issues per repo via `gh`). The `icb`/`gh` layers degrade to warnings when a tool is missing or unauthenticated. Flags: `--json`, `-F`, `--no-issues`, `--no-tasks`.
  A fourth section, **Possibly done**, cross-checks the two: an open roadmap item carrying a `repo` is flagged when that repo's `status.md` has a line reporting completion *and* naming at least two of the item's distinctive words. It quotes the line as evidence rather than asserting a verdict, because the check is a heuristic. Two tuning decisions came from live false positives: words drawn from the completion vocabulary itself (`done`, `shipped`) are excluded from an item's distinctive terms, and one shared word is never enough. **This is the AI's dev brief across repos — deliberately NOT the human single pane of glass (`menu dashboard` in dotfiles, a glance across all life apps: tasks/habits/books/learning). Two audiences, two scopes; do not extend one to cover the other.**
- `exec` — run an inline command or script file across repos
- `dies` — manage and run dies (reusable scripts with metadata and stats tracking)
  - Subcommands: `list`, `run`, `show`, `search`, `stats`
  - `run` takes two different previews. `--dry-run`/`-n` names the repos the die would visit and executes nothing. `--check` runs it for real with `FORGE_CHECK=1` set and has the script report what it would change — a content-level preview `--dry-run` structurally cannot give. It is refused for any die not declaring `supports_check: true` in the registry, because a die that ignored the variable would write to every repo while the operator believed they were previewing
- `precommit generate` — generate `.pre-commit-config.yaml` from standard blocks (Go implementation)
- `ci generate` — generate `.github/workflows/validate.yml` from standard CI blocks (`--dry-run` prints instead of writing)
- `precommit check` — report hooks that would abort a sync because they are non-standard and unmarked
- `version` — print version, commit, and build date (set via ldflags)
- `update` — self-update from GitHub releases (downloads pre-built binary, atomic swap)

**Embedded assets** — dies, pre-commit blocks, configs, and scripts are embedded into the binary via `//go:embed` in `embed.go` at the repo root. The binary is self-contained — no repo clone needed.

**Dual-mode operation:**

- **Embedded mode** (default): binary uses embedded assets. Die scripts are extracted to temp files for execution. `FORGE_DATA_DIR` env var points to extracted pre-commit assets.
- **Filesystem mode** (development): when `FORGE_DIES_DIR` env var is set, dies and assets are read from disk. Use direnv (`.envrc` in repo root) for automatic setup. Scripts reference assets via `dirname $0` resolution.

**Packages** (at repo root):

- `config` — loads the repo registry:
  - **Repo registry** (`$XDG_DATA_HOME/forge/repos.json`, JSON): defines repos with `name`, `path`, `status` (`active`/`dormant`/`retired`), and optional `description` and `owner`. An `owner` marks a third-party reference clone — a repo cloned for reading, never for cross-repo work.
  - **`toolchain`** on a repo entry declares its build surface: `components` (a `stack` plus the `dir` it lives in) and `sql_dialect`. Declared, never detected — the portfolio has five conventions for where a Go service lives (`api/`, `cli/`, root, and two legacy shapes), and a fact like the SQL dialect is not derivable from a layout at any level of tidiness. A repo can hold several components of one stack: nomad's `api/` and `cli/` are both Go modules, deliberately isolated. `config.FindRepoByPath` resolves a working directory to its entry, so a generator run anywhere inside a repo finds its declaration.

  - The `-c` persistent flag overrides the repos file path. `FORGE_DIES_DIR` env var enables filesystem mode for development.
- `dies` — registry (`LoadRegistry` accepts `fs.FS` — works with `os.DirFS`, `embed.FS`, or test fakes) and stats (JSONL append log at `~/.local/share/forge/stats.jsonl`). Also contains the bash die scripts in category subdirectories.
- `runner` — executes commands in each repo directory, handles output capture, colored results, filtering, and env var injection
- `assets` — extracts embedded assets to temp directories for shell execution, manages cleanup
- `precommit` — Go implementation of config generation (block composition, custom section preservation, hook deduplication, safety checks)
- `ci` — generates the baseline validation workflow, reusing `precommit`'s block composition and custom-section markers
- `toolchain` — the version manifest shared by both generators

**Repo selection** — every command resolves its repos through `runner.SelectRepos(repos, names)`. With no `-F` it returns `status: active` only, and **excludes reference clones**: no implicit operation should ever write to a repo we don't own. Naming repos explicitly with `-F` overrides both, so a clone or a dormant repo stays reachable on purpose. Retired repos are the one status `-F` cannot reach.

**Dormant is excluded from implicit sweeps, and that is most of the portfolio** (`jq -r '.repos[].status' $XDG_DATA_HOME/forge/repos.json | sort | uniq -c`). None of it takes another release, so a maintenance die run across it is churn: a config every repo "should" have is worth nothing in one that will never run the tool. An entry with **no** status counts as active — repos.json is hand-edited, and the opposite default drops a repo out of every maintenance operation without a word.

**Data flow for `dies run`:** determine asset source (embedded or `FORGE_DIES_DIR`) → load registry from `fs.FS` → validate die exists → extract script to temp file if embedded → load repo registry → `SelectRepos` (retired, dormant and reference clones dropped unless named) → execute script in each repo via bash (with `FORGE_DATA_DIR` if embedded) → print colored results → append stats record → cleanup temp files.

## Die Scripts

Dies are bash scripts in `dies_dir`, organized by category subdirectory. Exit code conventions:

- **0** = OK (success)
- **2** = SKIP (nothing to do — the `ExitSkip` constant in `runner/runner.go`)
- **anything else** = FAIL

Optional metadata lives in `dies/registry.yml` with `description` and `tags` per die.

**Categories:**

- `checks/` — scorecard dies: report a repo's state, change nothing
- `maintenance/` — golden path enforcement: bring a repo back to the standard
- `onetime/` — one-shot migrations

`forge dies list` enumerates them with descriptions; the directories are the source of truth.

## Pre-commit Standardization System

Forge includes a composable system for generating standardized `.pre-commit-config.yaml` across all repos.

**`pre-commit/blocks/`** — numbered YAML fragments composed from the repo's declared components:

- Generic (all repos): conventional-commits, commit-branding, file-checks, markdown, shell, codespell. The shell block is shellcheck, shfmt (styled by the deployed `.editorconfig`, never by hook args) and bats. The bats hook is guarded so a repo with no `tests/*.bats` passes, but a repo that *has* them and lacks bats fails at 127 rather than reporting success — the failure mode that hid seven shellcheck findings in `font` behind a green `language: system` hook
- Python: python-format (uv-lock, ruff-format), python-lint (ruff-check, mypy via uv run)
- Go: gofumpt, go-vet, go-build, go-mod-tidy, go-test, golangci-lint
- Vue: eslint, prettier, typecheck (each entering the component's own directory)
- Docker: hadolint
- GitHub Actions: actionlint
- Terraform: validate, tflint, fmt, docs
- SQL: sqlfluff-lint, with the dialect taken from the registry's `sql_dialect`

**`pre-commit/toolchain.yml`** — the single source of truth for every pinned tool version. Blocks keep their own `rev:` lines so each stays readable and valid standalone, but generation overwrites them from the manifest — the manifest wins. `TestToolchainManagesEveryBlockRepo` fails if a block names a remote repo the manifest doesn't pin, so a new block cannot introduce an unmanaged version.

The manifest's `version` is stamped into every generated config as a `# forge-toolchain: N` first line. That stamp is what makes staged rollout possible: bump a tool and the version, resync one repo, verify, then fan out — and `rg '^# forge-toolchain:' ` across the portfolio answers which repos are current. Bump `version` on any rev change.

**`pre-commit/configs/`** — standard tool config templates deployed alongside the pre-commit config:

- `markdownlint.json` — all repos
- `shellcheckrc.ini` — deployed as `.shellcheckrc` to all repos, for the same reason as `.editorconfig`: the shell block is generic. Carries one disable, SC1091/SC1090, because a source through a variable or an installed path cannot be resolved and following one that can be resolved was measured over 295 shell files to yield nothing that not following it misses. Its only product was `# shellcheck source=` directives, each a hardcoded path that rots when the file moves. **Never add another `disable=` line**: the config this replaced turned off SC2155 fleetwide and hid six real findings per repo, where `local x=$(cmd)` masks the command exit status. A repo needing an exception declares `toolchain.shellcheck_disable` in the registry, which the die appends below the shared block
- `editorconfig.ini` — deployed as `.editorconfig` to all repos, because the shell block is generic and shfmt takes its style from there rather than from hook args. Shell keys sit under `[*]`, not `[*.sh]`: the portfolio keeps executables with no extension (`bin/font`, `dotfiles/apps/common/menu`, `theme/themes/*/bordersrc`), a suffix pattern cannot reach them, and shfmt does not skip a file it fails to match — it formats it at its tab default. shfmt reads these keys only for files it identifies as shell, so `[*]` costs nothing there; the per-language sections exist because editors read `[*]` for everything. **Never put a parser or printer flag on the shfmt hook**: a flag replaces the EditorConfig file rather than merging with it, so one `--indent` silently drops `switch_case_indent` and `binary_next_line` as well
- `golangci.yml` — Go repos
- `prettierrc.json` — Vue repos
- `sqlfluff.ini` — deployed as `.sqlfluff` wherever a `sql_dialect` is declared. Rules are narrowed to `ambiguous, references, structure, convention.terminator`: sqlfluff's defaults are mostly layout and capitalisation opinions, which failed every `.sql` file in the portfolio and would have taught everyone to skip the hook. The narrowed set passes clean across all seven repos while still catching unparsable SQL and unused CTEs
- `pyproject-tools.toml` — merged into Python repos' pyproject.toml (ruff, mypy, codespell, pytest, pyright). The ruff `select` is the six rules every repo already runs, not an aspirational set: a template nothing conforms to reads as the standard while being unable to measure drift. `[tool.pyright]` is here despite the hook enforcing mypy, because basedpyright runs in every editor on every machine and is therefore exactly what drifts — it was dropped once as "an editor concern" and the four repos configuring it diverged. Named `pyright`, not `basedpyright`, so one section serves nvim and Pylance. `typeCheckingMode = "standard"` replaced the twelve-key blocks repos had accumulated; that collapse was a one-time migration, already applied everywhere, and is not something the steady-state sync repeats

**Config generation** — a Go function in `precommit/generate.go`, invoked as `forge precommit generate`. Handles block composition, custom section preservation, hook deduplication, and safety checks.

Which blocks apply, and **where each one runs**, come from the repo's declared `toolchain.components` — the same source CI reads, never a filesystem probe. Stack blocks carry `{{dir}}` (a `cd` target) and `{{dirprefix}}` (a `files:` anchor, empty at the root since `^\./` matches nothing pre-commit passes). Renders that come out identical collapse, so a repo with `api/` and `cli/` gets one Go block; renders that differ have their hook ids and names suffixed with the directory. `--detected <stack>` remains as an override for a repo not yet in the registry, and places everything at the root. `forge precommit stacks` prints the declared categories so the die deploys tool configs from that same answer.

A block must appear in `categoryMap` (stack-gated) or `genericBlocks` (every repo); one in neither is an error rather than a silent default. The `sql` category is the one gated by something other than a component — a declared `sql_dialect` pulls it in and fills `{{dialect}}`, because `.sql` files have no build directory of their own and nothing in them says postgres rather than sqlite.

**`pre-commit/scripts/`** — Python helper scripts (embedded in binary):

- `generate_config.py` — legacy Python generator (replaced by Go implementation, kept as reference)
- `merge_pyproject_tools.py` — merges standard tool sections into pyproject.toml using tomlkit (no Go equivalent for lossless TOML editing). **The standard owns exactly the keys it writes**, recorded as `[tool.forge] managed` in each repo's pyproject. That record is what makes retraction possible: a key dropped from the template is removed everywhere on the next sync, because the record proves forge put it there, and the retraction is printed rather than silent. A key absent from the record is the project's and is unreachable from the delete path.

  This replaced a `REPLACE_SECTIONS` set naming whole sections to overwrite wholesale. Owning a section and setting a floor under one are different jobs, and one verb doing both deleted project config three times — a repo's ruff `exclude`, then a FastAPI repo's bugbear exemptions, its pydantic mypy plugin and an alembic per-file-ignore. Per-key ownership recorded at write time cannot express that mistake, which is why the fix is a mechanism rather than a fourth entry removed from a list. Paths are stored as arrays, not dotted strings, because a segment can contain a dot (`per-file-ignores."__init__.py"`) and the record that authorizes deletion does not get to depend on quoting being right. The record table is rebuilt from scratch on every write, so a resync is byte-identical — the die's SKIP status depends on that idempotence

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
declared stack with no CI block is skipped rather than emitting an empty job, so the registry stays
free to declare more than CI can build.

The output is **`validate.yml`, not `ci.yml`** — several repos hand-wrote a `ci.yml` long before this
existed (nomad's is a multi-job pipeline with working directories and image env), and generating over
one would destroy work nothing could recover. `ci.Run` aborts on any `validate.yml` lacking the
`# forge-toolchain:` header for the same reason. Bespoke pipelines stay as separate workflow files;
the generated one is additive.

**`dies/checks/can-generate.sh`** — the pre-rollout gate. Dry-runs both generators against a repo,
validates the workflow with actionlint and the config with `pre-commit validate-config`, and reports
what would block a real sync: unmarked custom hooks, or a hand-written `ci.yml`. Writes nothing.
**Run it across the portfolio before bumping the manifest** — a version bump fans out to every repo,
so the rollout is only as safe as its worst one. The gate is clean when it reports zero failures;
run it rather than trusting a count written here.

The findings worth knowing are the ones no schema validator can reach. `defaults.run.working-directory`
does not apply to action inputs, so any path in one needs `{{dir}}`. A valid workflow can still fail at
runtime: the python block guards mypy behind a config check and tolerates pytest's exit 5 (no tests
collected), because `docs`, `homelab` and `refcheck` would otherwise fail on a baseline they never
opted into. And a schema-valid pre-commit config can fail on first use, so the gate resolves every
`npm run X` it emits against that component's `package.json` — which is how nomad's missing `typecheck`
script and the workspace repos' missing `lint:fix` surfaced. `${{ ... }}` is workflow syntax, not an
unexpanded generator placeholder; the placeholder check has to exclude it.

CI blocks exist for go, python, vue, rust, lua and terraform. **docker deliberately has none**: the
image build is bespoke per repo (each app builds and pushes its own in its own pipeline) and hadolint
is a pre-commit concern, so there is no baseline job left to run. A declared stack with no block is
skipped rather than emitting an empty job.

**`dies/maintenance/sync-pyproject.sh`** — the pyproject merge alone, without the config
regeneration. Adopting one better setting through the full sync die means also fanning out whatever
`toolchain.yml` currently pins, to every Python repo at once; most carry no `# forge-toolchain:`
stamp yet, so that is a first-time rewrite rather than a bump. Coupling a cheap change to an
expensive one is why the cheap change stopped being made and the settings drifted instead. Running
it across the portfolio is also the drift report — OK means drifted, SKIP means current.

`--check` (via `FORGE_CHECK`) prints the unified diff each repo would take instead of writing it. A
template edit fans out to every Python repo at once, so this is the plan step: see the change across
the portfolio, then apply. It is also the only way to preview a retraction before it happens.

Both this and the sync die pass `uv run --no-project`: without it uv builds the repo being edited
just to run a stdlib script, and the build chatter on stderr is long enough to swallow the one word
the die reads back to decide OK versus SKIP.

**`dies/maintenance/sync-ci.sh`** — generates the workflow, exits SKIP when current or when no
declared component has a CI block.

**`dies/maintenance/sync-pre-commit.sh`** — the main die that orchestrates everything. Generates the config, deploys tool configs, merges pyproject.toml, and installs the git hooks for **every stage a block uses** (`pre-commit`, `commit-msg`, `prepare-commit-msg`, `post-commit`) — an uninstalled stage means those hooks silently never run. Idempotent — exits with SKIP when nothing changed.

## Testing

**Go tests** (`go test ./...`):

- `config/` — forge and syncer config loading
- `dies/` — registry and stats, plus integration tests for the sync-pre-commit die (declared stacks, dedup, custom preservation, safety, config deployment). Each test writes a temp registry naming its temp repo and points the die at it with `XDG_DATA_HOME`, so a test declares its stacks the same way a real repo does.
- `precommit/` — config generator: unit tests (using `fstest.MapFS`) plus integration tests against real blocks
- `runner/` — repo filtering, execution

**Note:** The sync-pre-commit integration tests (`dies/sync_precommit_test.go`) require the `forge` binary on PATH since the die script calls `forge precommit generate`. Run `go install .` before `go test ./...`.

**Python tests** (`pre-commit/scripts/run_tests.sh`):

- `test_generate_config.py` — unit tests for the legacy Python generator
- `test_merge_pyproject_tools.py` — pyproject merge: what the standard forces, what it never deletes, and what it retracts
- `test_integration.py` — integration tests for the legacy Python generator

Python tests run as a pre-commit hook on files matching `^pre-commit/`.

## Build and Release

- `.goreleaser.yaml` — goreleaser config with ldflags injecting version/commit/date into the binary
- `.github/workflows/release.yml` — release workflow, triggered on push to `main`. go-semantic-release decides the version and creates the tag; triggering on the tag instead is the broken pattern `~/dev/standards/release.md` rejects, because a `GITHUB_TOKEN` tag push does not retrigger Actions
- Installed via `go install github.com/datapointchris/forge/v4@latest` or dotfiles `go-tools.sh`
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
