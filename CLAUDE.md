# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Forge

Forge **acts on repos**: it runs an operation against a selection of them, one at a time. Planning
directory sync, command execution, reusable maintenance scripts ("dies"), and the composable
pre-commit and CI standardization systems.

**The split with fleet is granularity, not read versus write.** Every operation here is "do this to
a repo"; the sweep machinery — selection, `-F`, running across the portfolio — is how one operation
reaches many repos, not a different kind of operation. `fleet`'s unit is the fleet as a whole, and
it does no repo-specific work beyond reading each repo to assemble a view of it.

So the test for a new command is **what it operates on**. A read that runs per repo still belongs
here; a question about the fleet belongs in fleet even if answering it one day means writing
something.

**`status` and `brief` were on the wrong side of that line** and have gone to fleet, as
`fleet status` and `fleet info`. Both answered a question about the portfolio rather than operating
on a repo. Forge has no read side left; do not add a third.

## Commands

```bash
go build -o forge .        # Build binary
go test ./...              # Run all tests (includes Go integration tests for dies)
golangci-lint run          # Lint (enabled set is in .golangci.yml)
go run . <subcommand>      # Run without building
```

The hook inventory is generated from the pinned-version declaration — `forge toolchain show` prints it and names the file, which is what to read rather than a list here, along with a repo's own `.pre-commit-config.yaml`. `standards/ci.md` § "forge is the source of truth" is the rule, and this repo owns the generator that makes it enforceable. A custom hook runs the Python test suite when `pre-commit/` files change.

## Architecture

**CLI layer** (`cmd/`) uses Cobra. Top-level commands:

- `repos` — the reconcile verbs, and everything that acts on repos. `check` / `plan` / `apply` each take an optional die name, and `-F` narrows the repos. Naming a die is its own confirmation — the word is the scope — so that form of `apply` runs unprompted; the unaliased form prints the plan and asks, taking `--yes` to skip and refusing off a TTY rather than blocking on a closed stdin. Widening the arity does not widen what apply may touch: `reconcile.Apply` skips anything not `Actionable()`, so a `ByHand` finding is unreachable from either form. `list` answers which repos a verb would visit; `exec` runs an arbitrary command or script file, which is where a one-off sweep lives now that dies are Go
- `directories` — the same reconcile verbs over the targets git does not version: a Syncthing folder, a home directory, anything held to the standard without a remote. Built by the same `reconcileNoun` factory as `repos`, so the two cannot drift in flag names, exit codes or output shape — `cli-design.md` § "Two front doors on one dataset spell everything identically" is the rule, and `TestBothNounsSpellTheSharedVerbsIdentically` is what keeps it true. They are declared in **forge's own config**, never in the registry: `repos.json` is read by syncer, fleet, indy and `pull-requests`, and each takes an entry there to be a git repo with a GitHub remote. `run` is the verb only this noun has
- `config` — print the resolved config with the layer that set each value, per `configuration.md`. The registry path is among them: `repos_registry` names a registry maintained outside forge's own data directory, `-c` still beats it, and forge's data directory answers for a machine that declares neither. This reverses an earlier decision that made `-c` the only override on the grounds that a tool resolving its own data directory needs no config key — which conflates carrying a path with accepting one. A path compiled in is what makes a tool fleet-specific; a path it is told is what keeps it generic. The alternative in practice was a symlink from forge's data directory to the real registry, made by hand on every machine and reported by nothing
- `dies` — the library, which executes nothing: `list`, `show`, `search`, `stats`
  - `stats` aggregates one row per die, most-recently-run first, with `--since` (git's own spelling — `2 weeks`, `30.days.ago` — plus Go durations and ISO dates) and `--json`. Naming a die gives its run-by-run history instead. Per-run rows grow without bound while the number of dies does not, so the old per-run dump put the oldest screen in front of the reader first
- `toolchain` — `show` alone, and it reads the file `versions_file` names. A machine naming none is an error pointing at `forge config show`, not a fallback to the embedded manifest. It prints the resolved path beside the version, so the numbers are always attributable to a file. **Nothing here writes a pin.** A pin is chosen, not discovered, and asking upstream what it has tagged answers a different question from what the fleet rolls out. Raising one is an edit to the declaration plus a `stamp.version` bump, fanned out by `forge repos apply precommit -F <repo>` and then widened
- `test` — run the tests of every component the registry declares for a repo, using the command `ci/blocks/` generates into that repo's own workflow, so a local run and CI run the same thing
- `cli` — `spec`, `audit`, `diff` and `snapshot` over the installed CLIs' command surfaces, reporting where their grammar varies. Reads from the *outside* only: `--help`, plus cobra's `__complete` where a tool has one. It never runs a bare subcommand, because the shape most worth finding is a noun that performs a read with no verb, and running it to find out would fire that read against a live API. Four help formats are parsed (`cliaudit.Framework`) — the parsers exist because five tools hand-roll their help and cannot be read any other way. **Reports variation, never fails**: `$STANDARDS_DIR/cli-design.md` holds machine contracts that bind and design guidance that does not, and this covers the second, so it exits 0 whatever it finds. Discovery resolves active repos to binaries from what each repo already declares — a `pyproject.toml` `[project.scripts]` key, a goreleaser `binary:` — falling back to the repo name; a repo declaring an entry point that is not installed is listed, one declaring none is not (that is a library, not a missing tool). **It audits what is installed, not the working tree**, so a tool built but not installed reports its released surface
- `version` — print version, commit, and build date (set via ldflags)
- `update` — self-update from GitHub releases (downloads pre-built binary, atomic swap)

**Embedded assets** — pre-commit blocks, configs, scripts and CI blocks are embedded into the binary via `//go:embed` in `embed.go`. The binary is self-contained. There is no filesystem mode: dies are Go, so a development build *is* the current dies, and `FORGE_DIES_DIR` had nothing left to switch.

**Packages** (at repo root):

- `config` — loads the repo registry, and forge's own config:
  - **Repo registry** (wherever `repos_registry` points; `repos-registry` prints it): defines repos with `name`, `path`, `status` (`active`/`dormant`/`retired`), and optional `description` and `owner`. `owner` is the GitHub owner and is required on every entry, because a bare name does not identify a repository. A reference clone — cloned for reading, never for cross-repo work — is an entry whose owner differs from the registry's own, derived by `LoadSyncerConfig` into `Repo.Reference`. Reading a *present* owner as the marker is what made every implicit sweep select nothing the day the field went universal.
  - **Machine config** (`$XDG_CONFIG_HOME/forge/config.yml`, YAML): `maintained_directories`, each reusing the `Repo` shape. This exists because the registry is *shared* — a key only forge can act on does not merely add a concept the other readers ignore, it changes what iterating the collection means for all of them at once. YAML because every entry needs its reason beside it, which is also why `Repo` and `Toolchain` carry both `json` and `yaml` tags: yaml.v3 lowercases the Go field name by default, so `sql_dialect` would arrive as nil rather than failing. `TestRepoParsesIdenticallyFromJSONAndYAML` is what keeps the two spellings one struct. Unknown keys are an error, because a misspelled key leaves a directory undeclared and an undeclared directory reads as a converged one.
  - **`toolchain`** on a repo entry declares its build surface: `components` (a `stack` plus the `dir` it lives in) and `sql_dialect`. Declared, never detected — the portfolio has five conventions for where a Go service lives (`api/`, `cli/`, root, and two legacy shapes), and a fact like the SQL dialect is not derivable from a layout at any level of tidiness. A repo can hold several components of one stack: nomad's `api/` and `cli/` are both Go modules, deliberately isolated. `config.FindRepoByPath` resolves a working directory to its entry, so a generator run anywhere inside a repo finds its declaration.

  - **`sync_base`** (top level) and **`synced_dirs`** (per repo) are what the planning die reads. Declared rather than compiled in: `~/dev/repos` is one fleet's Syncthing layout, and `stats/data` belongs to ichrisbirch. Both were hardcoded in the bash die, which is what `DefaultReposPath`'s own comment rules out.
  - The `-c` persistent flag overrides the repos file path.
- `reconcile` — the die contract and the walk. `Change` (Verdict × Repair), the check/plan lenses, `Assess`/`Apply`, exit codes, rendering. See below.
- `dies` — one file per die, the `Builtin()` registry, and stats (JSONL append log at `~/.local/share/forge/stats.jsonl`).
- `runner` — repo selection, and command execution for `repos exec`
- `precommit` — Go implementation of config generation (block composition, custom section preservation, hook deduplication, safety checks)
- `ci` — generates the baseline validation workflow, reusing `precommit`'s block composition and custom-section markers
- `directory` — runs a maintained directory's hooks. pre-commit needs a git index to know which files exist, so this builds a throwaway one: a bare repo in `$XDG_CACHE_HOME/forge/directories/<name>.git` with the directory as its work tree. Nothing is written inside the directory — a `.git` in a Syncthing folder conflicts on every peer — and that is asserted rather than assumed. Three details are load-bearing. `GIT_DIR`/`GIT_WORK_TREE` go in the **environment**, because pre-commit re-invokes git and only the environment reaches those calls. `git init` refuses when `GIT_WORK_TREE` is set, so creation uses an env without it. And the environment is inherited by hook *installation*, where a build backend runs its own git — check-json5 builds with poetry, poetry asks git for ignored files, and git reports a project root whose `.git` does not exist — so the hook environments are installed first in a throwaway real repo with a clean env
- `toolchain` — the pinned-version manifest shared by both generators, read from the declaration or from the embedded default

**Repo selection** — every command resolves its repos through `runner.SelectRepos(repos, names)`. With no `-F` it returns `status: active` only, and **excludes reference clones**: no implicit operation should ever write to a repo we don't own. Naming repos explicitly with `-F` overrides both, so a clone or a dormant repo stays reachable on purpose. Retired repos are the one status `-F` cannot reach.

**Dormant is excluded from implicit sweeps, and that is most of the portfolio** (`jq -r '.repos[].status' "$(repos-registry)" | sort | uniq -c` — the registry lives outside forge's data directory, which is the whole point of the `repos_registry` config key below). None of it takes another release, so a maintenance die run across it is churn: a config every repo "should" have is worth nothing in one that will never run the tool. An entry with **no** status counts as active — repos.json is hand-edited, and the opposite default drops a repo out of every maintenance operation without a word.

**Data flow for a reconcile verb:** load the registry → `SelectRepos` (retired, dormant and reference clones dropped unless named) → build one `Assets` (embedded trees + manifest) shared by the walk → `AssessAll` (Observe then Diff per repo per die, refusals isolated) → fold through the verb's lens → render rows, or `apply` and render outcomes → record the run → exit 0/1/3.

## Dies

A die is a Go value implementing `reconcile.Die`: `Observe` reads, `Diff` is pure, `Perform` is the only writer. `plan` is a **prefix of apply's call graph** rather than apply with a flag off, so no die contains a branch asking whether it may write — there is no branch that can be wrong. `forge dies list` enumerates them; `dies/builtin.go` is the registry, and a die carries its own description and tags rather than having them in a side-file that can disagree.

That replaced a `FORGE_CHECK` environment variable read just before each script's write, opted into per die with `supports_check: true` and verified by nothing. Four of twelve write dies implemented it; the rest could only be previewed by running them.

**`Change` is the contract.** `Verdict` is matched/missing/stale/undeclared/**unknown**; `Repair` is automatic/by_hand/none.

- **`plan` keeps `Automatic` repairs** — the repo differs from the standard, which is what apply is for.
- **`check` keeps `ByHand` findings** — a hand-written pipeline, an unmarked custom hook, a `.planning` diverged from its synced copy, a missing CLAUDE.md. Real drift apply cannot fix.
- **`Unknown` is neither**, and never moves the exit code. `gh` unauthenticated is not fifty drifted repos, and apply is not the fix for a login. *Unverified is not permission.*
- **`Undeclared` is never actionable.** Forge does not delete what it did not put there.

The old `checks/` vs `maintenance/` directory split was this distinction at the wrong granularity — `has-clean-gitignore` ended by printing "run sync-gitignore", the same desired-state list written twice in two files. Each concern is one die now.

**Exit codes** (`cli-design.md` § Machine contract, plus terraform's `-detailed-exitcode`): 0 converged, 1 pending changes, 2 usage, 3 something is wrong. `plan` answers 0/1 and 3 only on a refusal; `check` answers 0/3 and never 1.

**The property test is the point.** `dies/property_test.go` runs `Observe` + `Diff` for every registered die against a fixture sandbox and fails if anything changed. A new die is safe by default rather than safe by declaration, which is exactly what `supports_check` could not be.

## Pre-commit Standardization System

Forge includes a composable system for generating standardized `.pre-commit-config.yaml` across all repos.

**`pre-commit/blocks/`** — numbered YAML fragments composed from the repo's declared components:

- Generic (every target): file-checks, markdown, shell, codespell. The shell block is shellcheck, shfmt (styled by the deployed `.editorconfig`, never by hook args) and bats. The bats hook is guarded so a repo with no `tests/*.bats` passes, but a repo that *has* them and lacks bats fails at 127 rather than reporting success — the failure mode that hid seven shellcheck findings in `font` behind a green `language: system` hook
- Python: python-format (uv-lock, ruff-format), python-lint (ruff-check, mypy via uv run)
- Go: gofumpt, go-vet, go-build, go-mod-tidy, go-test, golangci-lint
- Vue: eslint, prettier, typecheck (each entering the component's own directory)
- Docker: hadolint
- GitHub Actions: actionlint
- Terraform: validate, tflint, fmt, docs
- SQL: sqlfluff-lint, with the dialect taken from the registry's `sql_dialect`

**`pre-commit/toolchain.yml`** — a test fixture, read by no command. forge's own CI declares no `versions_file`, and `TestToolchainManagesEveryBlockRepo` needs an in-repo manifest to run against. That test fails if a block names a remote repo the fixture doesn't pin, so a new block cannot introduce an unmanaged version. Every command reads the file `versions_file` names instead, and `forge toolchain show` prints that path beside the version. Blocks keep their own `rev:` lines so each stays readable and valid standalone, but generation overwrites them from the declaration — the manifest wins.

The manifest's `version` is stamped into every generated config as a `# forge-toolchain: N` first line. That stamp is what makes staged rollout possible: bump a tool and the version, resync one repo, verify, then fan out — and `rg '^# forge-toolchain:' ` across the portfolio answers which repos are current. Bump `version` on any rev change.

**`pre-commit/configs/`** — standard tool config templates deployed alongside the pre-commit config:

- `markdownlint.json` — all repos
- `shellcheckrc.ini` — deployed as `.shellcheckrc` to all repos, for the same reason as `.editorconfig`: the shell block is generic. Carries one disable, SC1091/SC1090, because a source through a variable or an installed path cannot be resolved and following one that can be resolved was measured over 295 shell files to yield nothing that not following it misses. Its only product was `# shellcheck source=` directives, each a hardcoded path that rots when the file moves. **Never add another `disable=` line**: the config this replaced turned off SC2155 fleetwide and hid six real findings per repo, where `local x=$(cmd)` masks the command exit status. A repo needing an exception declares `toolchain.shellcheck_disable` in the registry, which the die appends below the shared block
- `editorconfig.ini` — deployed as `.editorconfig` to all repos, because the shell block is generic and shfmt takes its style from there rather than from hook args. Shell keys sit under `[*]`, not `[*.sh]`: a shell executable is commonly shipped with no extension at all, a suffix pattern cannot reach one, and shfmt does not skip a file it fails to match — it formats it at its tab default. shfmt reads these keys only for files it identifies as shell, so `[*]` costs nothing there; the per-language sections exist because editors read `[*]` for everything. **Never put a parser or printer flag on the shfmt hook**: a flag replaces the EditorConfig file rather than merging with it, so one `--indent` silently drops `switch_case_indent` and `binary_next_line` as well
- `golangci.yml` — Go repos
- `prettierrc.json` — Vue repos
- `sqlfluff.ini` — deployed as `.sqlfluff` wherever a `sql_dialect` is declared. The narrowed ruleset and why the defaults were unusable are `standards/ci.md` § "sqlfluff ships a narrowed ruleset, lint-only"
- `pyproject-tools.toml` — merged into Python repos' pyproject.toml (ruff, mypy, codespell, pytest, pyright). The ruff `select` is the six rules every repo already runs, not an aspirational set: a template nothing conforms to reads as the standard while being unable to measure drift. `[tool.pyright]` is here despite the hook enforcing mypy, because basedpyright runs in every editor on every machine and is therefore exactly what drifts — it was dropped once as "an editor concern" and the four repos configuring it diverged. Named `pyright`, not `basedpyright`, so one section serves nvim and Pylance. `typeCheckingMode = "standard"` replaced the twelve-key blocks repos had accumulated; that collapse was a one-time migration, already applied everywhere, and is not something the steady-state sync repeats

**Config generation** — `precommit/generate.go`. Block composition, custom section preservation, hook deduplication, and safety checks, all as pure functions over text. The `precommit` die composes them directly rather than through `Run`/`DryRun`, which is what makes `Observe` a read: `Generate` and `SafetyCheck` cannot write, so the read verbs have no path to a write.

Which blocks apply, and **where each one runs**, come from the repo's declared `toolchain.components` — the same source CI reads, never a filesystem probe. Stack blocks carry `{{dir}}` (a `cd` target) and `{{dirprefix}}` (a `files:` anchor, empty at the root since `^\./` matches nothing pre-commit passes). Renders that come out identical collapse, so a repo with `api/` and `cli/` gets one Go block; renders that differ have their hook ids and names suffixed with the directory. `dies.declaredCategories` derives the same answer the die deploys tool configs from, so the config and its configs cannot disagree about which stacks a repo has.

A block must appear in `categoryMap` (stack-gated) or `genericBlocks` (every target); one in neither is an error rather than a silent default. Two categories are gated by something other than a component, and both are seeded by `Generate` rather than by `dirsByCategory`. `sql` follows a declared `sql_dialect` and fills `{{dialect}}`, because `.sql` files have no build directory of their own and nothing in them says postgres rather than sqlite. `git` holds conventional-commits and commit-branding, and follows the `versioned` argument: both hook a commit stage, so on a target git does not version they would be hooks that can never fire — and nothing would report it, since `uninstalledHooks` finds no missing stages where there is no `.git` to install them into.

**`pre-commit/scripts/`** — Python helper scripts (embedded in binary):

- `generate_config.py` — legacy Python generator (replaced by Go implementation, kept as reference)
- `merge_pyproject_tools.py` — merges standard tool sections into pyproject.toml using tomlkit (no Go equivalent for lossless TOML editing). **The standard owns exactly the keys it writes**, recorded as `[tool.forge] managed` in each repo's pyproject. That record is what makes retraction possible: a key dropped from the template is removed everywhere on the next sync, because the record proves forge put it there, and the retraction is printed rather than silent. A key absent from the record is the project's and is unreachable from the delete path.

  This replaced a `REPLACE_SECTIONS` set naming whole sections to overwrite wholesale. Owning a section and setting a floor under one are different jobs, and one verb doing both deleted project config three times — a repo's ruff `exclude`, then a FastAPI repo's bugbear exemptions, its pydantic mypy plugin and an alembic per-file-ignore. Per-key ownership recorded at write time cannot express that mistake, which is why the fix is a mechanism rather than a fourth entry removed from a list. Paths are stored as arrays, not dotted strings, because a segment can contain a dot (`per-file-ignores."__init__.py"`) and the record that authorizes deletion does not get to depend on quoting being right. The record table is rebuilt from scratch on every write, so a resync is byte-identical — the die reporting converged depends on that idempotence

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
`go install` versions come from the same pinned-version declaration as the pre-commit hooks, and the same
`# > custom:` markers preserve repo-specific steps.

**The `push: main` trigger is emitted only when nothing else covers main** — the rule and the
duplicate-run measurement behind it are `standards/ci.md` § "`push: main` is emitted only when
nothing else validates main", which names this file as its canonical source.

`ReleaseGatesOnValidate` is the implementation, and it decides by **reading `release.yml` for a
`uses:` naming this workflow**.
Detection, not a registry field, and deliberately against the usual `declared, never detected`
rule: the failure being prevented is someone adding a release gate and forgetting to flip a flag,
which a flag cannot prevent and a file read cannot get wrong. It re-derives on every generate, so
adding or removing the gate fixes the triggers by itself. Every unknown — no `release.yml`, an
unreadable one, a release gating on a hand-written `ci.yml` — answers false and keeps `push`, so
the failure mode is the old duplicate run rather than an unvalidated main.

**One job per declared component**, named `<stack>` at the root or `<stack>-<dir>` below it, each
with a `working-directory`. nomad generates `go-api`, `go-cli`, and `vue-web` running in parallel —
a single serial job would hide which module failed and force them to share one setup step. A
declared stack with no CI block is skipped rather than emitting an empty job, so the registry stays
free to declare more than CI can build.

**`runs-on` follows the repo's declared visibility**, through `ci.RunnerFor`. A private repo takes
the self-hosted pool, because GitHub bills Actions minutes for hosted runners on private repos and
not on public ones. Anything not positively declared private takes `ubuntu-latest`, and the
direction of that default is the safety property: a fork's pull request on a public repo runs the
fork's own code on whatever runner it lands on, and a self-hosted runner sits inside a private
network. `TestNoProductionCallerOutsideThisPackageNamesTheSelfHostedRunner` is what keeps the choice
in `RunnerFor` rather than at a call site.

**The die writes a second file, `.github/actionlint.yaml`**, and only into the repos whose workflow
names the pool. actionlint discovers GitHub's own labels and nothing else, so without that
declaration a private repo's actionlint hook reports the pool as a typo. Its absence in a public
repo is a check rather than an omission — it is what makes that repo's hook reject a hand-written
workflow reaching the self-hosted runner. Both paths are observed whatever the runner is: a repo
that turns public has the declaration **removed**, because a path nothing reads is a path every verb
reports converged. The stamp is what authorises that delete, so a hand-written config at either
spelling — `.yaml` or `.yml` — is reported and left alone.

The output is **`validate.yml`, not `ci.yml`** — several repos hand-wrote a `ci.yml` long before this
existed (nomad's is a multi-job pipeline with working directories and image env), and generating over
one would destroy work nothing could recover. The `ci` die refuses any `validate.yml` lacking the
`# forge-toolchain:` header for the same reason. Bespoke pipelines stay as separate workflow files;
the generated one is additive.

**`forge repos check` is the pre-rollout gate.** A version bump fans out to every repo, so the rollout
is only as safe as its worst one. The `precommit` and `ci` dies validate the workflow with actionlint
and the config with `pre-commit validate-config`, and report what would block a real sync as `ByHand`
findings. This absorbed the `can-generate` die, which existed only because the generators could not be
run across the portfolio — the thing `-F` gave every other operation and the promotion to a top-level
command had taken away.

The findings worth knowing are the ones no schema validator can reach. `defaults.run.working-directory`
does not apply to action inputs, so any path in one needs `{{dir}}`. A valid workflow can still fail at
runtime: the python block guards mypy behind a config check and tolerates pytest's exit 5 (no tests
collected), because `docs`, `homelab` and `refcheck` would otherwise fail on a baseline they never
opted into. And a schema-valid pre-commit config can fail on first use, so the gate resolves every
`npm run X` it emits against that component's `package.json` — which is how nomad's missing `typecheck`
script and the workspace repos' missing `lint:fix` surfaced. `${{ ... }}` is workflow syntax, not an
unexpanded generator placeholder; the placeholder check has to exclude it.

`ls ci/blocks/` is which stacks have one. **docker deliberately has none** — the reason is
`standards/ci.md` § "Docker gets no generated CI block". A declared stack with no block is skipped
rather than emitting an empty job, which is what makes the absence safe.

**Every pinned version comes from the declaration, not from this repo.** `versions_file` in forge's
config names it, resolved exactly like `repos_registry` — flag, then `$FORGE_VERSIONS_FILE`, then
the config key. Unset means the manifest embedded in this binary, which is what forge did before
the file existed and is what a machine with no declaration still gets.

That inverts what `toolchain.yml` was. The embedded copy cannot drift and cannot move without
cutting a release, so a version bump used to mean releasing forge; naming a file makes it one edit
and a sweep. A declared file that cannot be read is an error rather than a fallback — quietly
rolling out whatever the binary shipped with is the failure this replaced, a bump that reports
success and changes nothing.

`toolchain.yml` stays as that embedded default and is still the shape the generators consume;
`toolchain.LoadFile` reads the declaration into the same type, so nothing downstream knows which
answered. `forge toolchain show` is where a person finds out — it prints the resolved path beside
the version, because the two sources print identical numbers and only the path separates a pin
someone chose from one this binary shipped with.

**The `gomod` die writes both Go directives, from that declaration.** The two look like one setting
and are not: `go` is a floor a consumer must clear, `toolchain` is what this build switches
up to. Taking a fixed standard library by raising the floor has a measured cost — `go install
<tool>@latest` prefers a release the *installing* machine can build, so a module floored above the
Go on a machine is skipped there silently, returning 0 and leaving the old binary while the
installer reports the machine converged.

Measured 2026-08-14 on archlinux with system go1.26.5. todoui was floored at 1.26.6 to clear five
standard-library advisories, and `go install @latest` returned 0 in 0.25s leaving v1.11.1 in place.
fleet took a toolchain directive against the same advisories: govulncheck clean, `go list` still
reporting the module at 1.26.5, `GOTOOLCHAIN=local go build` still working, and the release
installing on that machine. So raising a floor stays a compatibility decision belonging to whoever
owns the module, never to a fleet-wide sweep.

Both numbers come from the declaration's `languages.go`. Bump the toolchain on a standard-library
advisory; `govulncheck` in generated CI is what reports one.

**A declared floor the pinned linter cannot build is refused, never written.** Generated CI sets up
exactly the floor under `GOTOOLCHAIN=local` and then installs golangci-lint, so a floor below its
own minimum fails Lint in every Go repo at once with nothing wrong in any of them. The declaration
carries that bottom as `languages.go.binding_minimum` — declared rather than derived, because
reading it needs the module proxy and a die that reaches the network to decide one line is a die
that fails offline. The refusal is `ByHand`, so it surfaces in `check` and `apply` cannot reach it.

**A floor moves in either direction.** Lowering one is safe for every consumer; raising one excludes
them. todoui and ichrisbirch/cli were floored at 1.26.6 before this existed and were pulled back to
the declaration, which is what makes the fleet's floor uniform with no exceptions.

A module already floored at or above the toolchain pin gets no toolchain line, since it would be a
second copy of the same fact — and an existing one there is reported `Undeclared` rather than
deleted, because forge does not remove what it did not put there. Changes are itemized per module
directory, because a triad repo has three and a row saying `go.mod` could not say which.

`Perform` converges the whole module rather than the one directive its `Change` names. A `Change`
carries the file, not the line, and a module can drift on both at once — so the second change for
one `go.mod` arrives after the first settled it and reports `Skipped`.

**The `pyproject` die is separate from `precommit`, and stays that way.** Adopting one better setting
through the full sync means also fanning out whatever the declaration currently pins, to every Python
repo at once. Coupling a cheap change to an expensive one is why the cheap change stopped being made
and the settings drifted instead.

Its `Observe` is the merge script's own `--check`, whose unified diff becomes the `Change`'s `Patch` —
so `forge repos plan pyproject` shows a template edit as it will land across every Python repo, and is
the only way to preview a retraction. `uv run --no-project` is passed because without it uv builds the
repo being edited just to run a stdlib script.

**The `precommit` die** generates the config, deploys the tool configs its hooks read, and installs the
git hooks for **every stage the config declares** — an uninstalled stage means those hooks silently
never run. Stages are parsed from the `stages:` lists rather than substring-matched, because
`commit-msg` is a substring of `prepare-commit-msg`. Running it across the portfolio found 41 repos
declaring both those stages with neither hook installed.

## Testing

**Go tests** (`go test ./...`):

- `config/` — forge and syncer config loading
- `reconcile/` — the fold (a mixed `[]Change` through both lenses), the walk's refusal isolation, exit codes
- `dies/` — per-die behavior against a temp fixture repo, plus `property_test.go`, which asserts across **every** registered die that `Observe` + `Diff` leave the sandbox byte-identical and that no `Change` is left unclassified
- `precommit/` — config generator: unit tests (using `fstest.MapFS`) plus integration tests against real blocks
- `runner/` — repo filtering, execution

Nothing shells out to `forge`, so `go test ./...` needs no prior `go install`.

**Python tests** (`pre-commit/scripts/run_tests.sh`):

- `test_merge_pyproject_tools.py` — pyproject merge: what the standard forces, what it never deletes, and what it retracts
- `test_generate_config.py`, `test_integration.py` — the legacy Python generator, kept as reference

Python tests run as a pre-commit hook on files matching `^pre-commit/`.

## Build and Release

- `.goreleaser.yaml` — goreleaser config with ldflags injecting version/commit/date into the binary
- `.github/workflows/release.yml` — release workflow, triggered on push to `main`. go-semantic-release decides the version and creates the tag; triggering on the tag instead is the broken pattern `standards/release.md` rejects, because a `GITHUB_TOKEN` tag push does not retrigger Actions
- Installed via `go install github.com/datapointchris/forge@latest` or dotfiles `go-tools.sh`
- `forge update` — self-updates by downloading the latest release binary from GitHub (no Go toolchain needed)
- `forge version` — shows version, commit SHA, and build date (`dev` when built without ldflags)

## Key Patterns

- `repos exec` requires a `.git/` and skips anything without one. The reconcile verbs do not: `reconcile.Target.Versioned()` reads the filesystem, and the dies needing a remote, a branch or a workflow report themselves not-applicable
- `FilterRepos()` does exact name matching; empty filter = all repos
- Output uses `github.com/fatih/color` with nerd font icons (✔ ⚠ ✘)
- `ExpandTilde()` supports `~` and `~/path` only, not `~user/path`
- Stats are JSONL (one JSON object per line), malformed lines silently skipped for crash resilience

## Embedded Assets

Pre-commit blocks, configs, Python scripts and CI blocks are embedded via `//go:embed` in `embed.go`. There is no filesystem mode and no extraction, with one exception: the `pyproject` die materializes `merge_pyproject_tools.py` and its template to a temp directory to run them, because tomlkit is the only thing in either ecosystem that edits TOML losslessly and a round-trip that drops a repo's comments is a replacement rather than a merge.

## Never write the breaking-change trailer in a commit message

The words `BREAKING CHANGE` — either number, colon or not, subject or body — cut a major release
here, and a major on this repo is an outage rather than a version. `commit-analyzer-cz` matches
them unanchored against the raw message and ORs the result with the configured major rules, so
`.semrelrc` cannot stop it and it majors even a `fix:` commit.

The module path carries no `/vN` suffix, so once a major exists `go install …@latest` cannot see it
and silently resolves the highest v1 instead — `dotfiles check` reports the tool stale forever
while `apply` exits 0 having installed nothing. Every already-installed binary is stranded too:
`goselfupdate` refuses a lower version and reports "already up to date". Recovery is a reinstall on
each machine, and it is not a rewrite — branch protection refuses one on `main`, and the offending
commit re-cuts the major on every push until a tag above it takes it out of range.

**The ban covers a commit that merely discusses the trailer.** One explaining this exact caveat cut
a fresh major on push. Name it some other way — "that marker" — and never quote it.

Deliberate majors use `chore(release-major)`, the one subject `.semrelrc` leaves as a major. Full
reasoning and the reset procedure: `standards/release.md` § "Never write the breaking-change
trailer in a Go repo's commit message".
