# forge

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

## Architecture

`forge --help` lists the Cobra commands in `cmd/`. What is not guessable from it:

- **`repos apply <die>` runs unprompted; bare `repos apply` asks.** Naming a die is its own
  confirmation. The unaliased form prints the plan, takes `--yes`, and refuses off a TTY rather than
  blocking on a closed stdin. Neither form reaches a `ByHand` finding: `reconcile.Apply` skips
  anything not `Actionable()`.
- **`directories` targets live in forge's own config, never the registry.** The registry is read by
  other tools, and each takes an entry there to be a git repo with a GitHub remote. Both nouns come
  from one `reconcileNoun` factory, and `TestBothNounsSpellTheSharedVerbsIdentically` keeps them level.
- **`-c` is declared on `repos`, `directories`, `cli`, `config` and `test`, never on the root.** A root
  persistent flag is advertised on every subcommand, including ones that never open the registry.
  `loadRepos` refuses a command that has not declared it, and `cmd/flagscope_test.go` pins the set.
- **`cli` reads installed CLIs from the outside only** — `--help`, plus cobra's `__complete` where a
  tool has one. It never runs a bare subcommand, because a noun that performs a read with no verb
  would fire that read against a live API. It reports variation and exits 0 whatever it finds.
- **Nothing in forge writes a pin.** `toolchain show` reads the file `versions_file` names and prints
  the path beside the version. A pin is chosen, not discovered.

**Embedded assets.** Blocks, configs, scripts and CI blocks are embedded via `//go:embed` in
`embed.go`, and there is no filesystem mode. The one extraction is the `pyproject` die, which
materializes `merge_pyproject_tools.py` and its template to a temp directory: tomlkit is the only
lossless TOML editor in either ecosystem, and a round-trip that drops a repo's comments is a
replacement rather than a merge. Its tests are `pre-commit/scripts/run_tests.sh`, run as a hook on
`^pre-commit/`.

**The registry** (wherever `repos_registry` points) gives each repo `name`, `path`, `status`
(`active`/`dormant`/`retired`) and a required `owner`. A reference clone is an entry whose owner
differs from the registry's own, derived by `LoadSyncerConfig` into `Repo.Reference`. Reading a
*present* owner as the marker once made every implicit sweep select nothing.

**Machine config** (`$XDG_CONFIG_HOME/forge/config.yml`) holds `maintained_directories` in YAML, so
each entry keeps its reason beside it. Unknown keys are an error, because a misspelled key leaves a
directory undeclared and an undeclared directory reads as converged. `Repo` and `Toolchain` carry
both `json` and `yaml` tags because yaml.v3 lowercases field names, so `sql_dialect` would arrive as
nil. `TestRepoParsesIdenticallyFromJSONAndYAML` pins it.

**`toolchain` on a registry entry is declared, never detected.** It names `components` (a `stack` and
its `dir`) and `sql_dialect`. The portfolio has five layouts for where a Go service lives, and no
layout reveals a SQL dialect. Every target resolves by name through `runner.SelectRepos`, never from
the working directory. `sync_base` and `synced_dirs` are declared for the same reason: a sync base is
one machine's layout.

**`directory` runs a maintained directory's hooks through a throwaway git index**: a bare repo in
`$XDG_CACHE_HOME/forge/directories/<name>.git` with the directory as its work tree, because a `.git`
in a Syncthing folder conflicts on every peer. `GIT_DIR`/`GIT_WORK_TREE` go in the environment,
because pre-commit re-invokes git. `git init` refuses while `GIT_WORK_TREE` is set, so creation
drops it. Hook environments install first in a clean throwaway repo, because a build backend runs its
own git — check-json5 builds with poetry — and trips on the inherited variables.

**Repo selection** — every command resolves its repos through `runner.SelectRepos(repos, names)`. With no `-F` it returns `status: active` only, and **excludes reference clones**: no implicit operation should ever write to a repo we don't own. Naming repos explicitly with `-F` overrides both, so a clone or a dormant repo stays reachable on purpose. Retired repos are the one status `-F` cannot reach.

**Selection is not the only gate, and the second one decides what a die may do.** Reaching a repo is `SelectRepos`; acting on it is each die's own applicability test, and for `precommit` that test is `maintained`. A declaration answers for a repo somebody works in. Forge's own `# forge-toolchain:` stamp answers for one nobody does: forge wrote those files, so it owns keeping them right, and a registry silent about the repo's stacks has not unwritten them. A stamped repo with no declaration generates as if it declared no components, which is the spelling a declaration of no components already has. That is not the same as "the generic blocks only": `Generate` seeds `git` from the target being versioned and `python-scripts` from the shebang scan, and neither is gated on a component. **The stamp authorizes correcting what forge wrote and nothing else**: first deployment still needs a declaration, and git hooks forge never installed are not installed by it. Those hooks are still *measured* there and reported `ByHand`, because a stage the config names with no hook installed is broken whether or not forge may fix it. Without both gates, every repo forge has written to but does not track holds whatever it last generated, and every verb reports converged because nothing looked.

**The stamp that authorizes those four files lives in only one of them**, so deleting `.pre-commit-config.yaml` returns the other three to exactly that unreachable state. `strandedToolConfigs` is what closes it: with no declaration and no config at all, the three generic tool configs are reported `ByHand` when **all three** are present. Forge deploys them together, so the full set is evidence forge wrote there and a lone `.editorconfig` is not. That evidence is weaker than a stamp and can afford to be, because what it authorizes is a report — nothing is written, overwritten or removed, and a stamp is still what would be needed to touch them.

**Dormant is excluded from implicit sweeps, and that is most of the portfolio** (`jq -r '.repos[].status' "$(repos-registry)" | sort | uniq -c`). None of it takes another release, so a maintenance die run across it is churn: a config every repo "should" have is worth nothing in one that will never run the tool. An entry with **no** status counts as active — repos.json is hand-edited, and the opposite default drops a repo out of every maintenance operation without a word.

**Data flow for a reconcile verb:** load the registry → `SelectRepos` (retired, dormant and reference clones dropped unless named) → build one `Assets` (embedded trees + manifest) shared by the walk → `AssessAll` (Observe then Diff per repo per die, refusals isolated) → fold through the verb's lens → render rows, or `apply` and render outcomes → record the run → exit 0/1/3.

`repos exec` requires a `.git/` and skips anything without one. The reconcile verbs do not:
`Target.Versioned()` reads the filesystem, and the dies needing a remote report themselves not-applicable.

## Dies

A die is a Go value implementing `reconcile.Die`: `Observe` reads, `Diff` is pure, `Perform` is the only writer. `plan` is a **prefix of apply's call graph** rather than apply with a flag off, so no die contains a branch asking whether it may write — there is no branch that can be wrong. `forge dies list` enumerates them; `dies/builtin.go` is the registry, and a die carries its own description and tags rather than having them in a side-file that can disagree.

**`Change` is the contract.** `Verdict` is matched/missing/stale/undeclared/**unknown**; `Repair` is automatic/by_hand/none.

- **`plan` keeps `Automatic` repairs** — the repo differs from the standard, which is what apply is for.
- **`check` keeps `ByHand` findings** — a hand-written pipeline, an unmarked custom hook, a `.planning` diverged from its synced copy, a missing CLAUDE.md. Real drift apply cannot fix.
- **`Unknown` is neither**, and never moves the exit code. `gh` unauthenticated is not fifty drifted repos, and apply is not the fix for a login. *Unverified is not permission.*
- **`Undeclared` is never actionable.** Forge does not delete what it did not put there.

**Exit codes**, following terraform's `-detailed-exitcode`: 0 converged, 1 pending changes, 2 usage, 3 something is wrong. `plan` answers 0/1 and 3 only on a refusal; `check` answers 0/3 and never 1.

**The property test is the point.** `dies/property_test.go` runs `Observe` + `Diff` for every registered die against a fixture sandbox and fails if anything changed. A new die is safe by default rather than safe by declaration.

## Pre-commit Standardization System

`precommit/generate.go` composes `.pre-commit-config.yaml` from the numbered fragments in
`pre-commit/blocks/`, as pure functions over text, so the `precommit` die's `Observe` cannot write.
Which blocks apply, and **where each runs**, come from the repo's declared `toolchain.components`,
never a filesystem probe. Stack blocks carry `{{dir}}` and `{{dirprefix}}`. Identical renders
collapse; differing ones get their hook ids suffixed with the directory. A block must appear in
`categoryMap` or `genericBlocks`, and one in neither is an error rather than a silent default.

Four categories are seeded by `Generate` rather than by a component:

- `sql` follows a declared `sql_dialect`, because nothing in a `.sql` file says which dialect it is.
- `git` (conventional-commits) follows whether the target is versioned. On an unversioned target it
  would be a hook that can never fire, and `uninstalledHooks` would not report it.
- `python-scripts` follows a scan of tracked files for extensionless apps with a
  `#!/usr/bin/env -S uv run --script` shebang. identify tags those `uv`, not `python`, so the ordinary
  ruff and mypy hooks skip them without a word — and mypy's `mypy .` collects by extension, so fixing
  the tag alone would not reach them. The block re-runs all three by path, with
  `--scripts-are-modules` so two apps in one commit do not collide as `__main__`. It is omitted when
  the scan finds nothing, since a hook matching no files reports passing. The scan is detection,
  deliberately: a new app landing undeclared is the gap it closes, and a registry key cannot.
- `go-release` (the major-marker hook) follows a declared Go component in a versioned repo whose
  workflow `uses:` go-semantic-release, read by `ci.InvokesGoSemanticRelease`. That analyzer majors
  on the marker unanchored, and a major on a Go module strands every install. A Go repo tagging
  with svu `--v0` or releasing through python-semantic-release gets no hook, since the marker cuts
  nothing there and the hook's remedy is wrong. An unreadable workflow answers yes: a spurious hook
  refuses a commit, a missing one ships the major.

**The shell block's bats hook is guarded both ways.** A repo with no `tests/*.bats` passes; a repo
that has them and lacks bats fails at 127. A green `language: system` hook once hid seven shellcheck
findings by reporting success there. In CI, the hooks job installs bats, its helper libraries and
the declared jq wherever `tests/*.bats` exists.

**Blocks name no version.** Each writes `{{pin}}` where one belongs, generation fills it from the
declaration, and a pin the declaration cannot fill is a refusal rather than a placeholder shipped to
a repo (`toolchain.Unpinned`, `TestBlocksNameNoVersion`). Generation stamps the declaration's
`version` as `# forge-toolchain: N`; bump it on any pin change, because the stamp is what staged
rollout reads. `toolchain.StampPrefix` is its one definition. `forge stamp spec --json` prints it
with `dies.StampedFiles`, which fleet reads at run time instead of keeping a copy, and
`TestStampedFilesAreExactlyTheFilesTheDiesStamp` holds that list to what the dies write. A tool
forge runs outside its hook takes the release that hook pins, as `hookPinnedTools` maps them: a uvx
line in a workflow, a Python repo's dev pin, the uv that writes a lock. A `binaries` entry for one is
refused as a second copy.
`toolchain/testdata/toolchain.yml` is the test fixture, read by no command, and its values name
tools rather than releases.

**A hook whose tool is a Go module never runs it from `PATH`.** A hook doing so passes or fails by what that
machine last installed, and gofumpt writes, so two releases rewrite each other. A hook whose tool is
a Go module runs as a `repo: local`, `language: golang` hook. pre-commit installs it from an
`additional_dependencies` item `- <module>@{{pin}}`, which `ApplyDependencyVersions` fills from the
`tools` pin. pre-commit builds it with the Go on `PATH`, or downloads a Go where there is none, so
CI runs the same hook with no install step of its own. Each such hook runs over a whole module,
never only the staged files. A formatter fed the staged files leaves the rest of the module as it
was, and the next commit touching one of those files fails on a change it did not make. So the Go
pair `cd`s into each declared directory, and golangci-lint must start beside the go.mod it loads
anyway. The tekwizely hooks walk every go.mod themselves and
stay one copy.

**Every template in `pre-commit/configs/` carries `# forge-managed` on its first line.** `handWritten`
reads it, and a file at a managed path without it is reported rather than overwritten
(`TestEveryDeployedToolConfigCarriesTheManagedMarker`). What each deployed config has learned:

- **markdownlint and prettier are YAML, not JSON**, because JSON cannot carry the marker and prettier
  warns on an unknown key every run. Both tools prefer the JSON file when both exist, so
  `toolConfig.supersedes` removes the old spelling when its content matches and reports it otherwise.
- **`.shellcheckrc` disables only SC1091/SC1090.** Never add another `disable=` line: the config this
  replaced turned off SC2155 fleetwide and hid real findings. A repo needing an exception declares
  `toolchain.shellcheck_disable` in the registry.
- **`.editorconfig` puts shell keys under `[*]`, not `[*.sh]`**, because shell executables often have
  no extension and shfmt formats an unmatched file at its tab default. **Never put a parser or printer
  flag on the shfmt hook**: one flag replaces the EditorConfig file wholesale.
- **`pyproject-tools.toml` keeps `[tool.pyright]`** although the hook enforces mypy, because
  basedpyright runs in every editor and is what drifts. Its ruff `select` is the rules every repo
  already runs, since a template nothing conforms to cannot measure drift. A rule joins it after
  the sweep that makes the fleet pass it, as `ICN` did with its import aliases (`dt`, `sa`, `sf`,
  `st`, `dc`). Those sit under `extend-aliases`, because `aliases` replaces ruff's defaults.
  flake8-bandit joins one rule at a time, never as `S`. Across the Python repos the family reported
  hundreds of findings, nearly all of them S603 and S607 on subprocess calls a CLI makes on purpose,
  and S101 on every pytest assert. Selecting the family would take an ignore list that grows with
  each repo. The rules selected are the hazards with no deliberate use in the fleet: `exec`, `eval`,
  pickle, an HTTP request with no timeout, md5 or sha1 for security, and TLS verification off. S506
  stays out because it flags `yaml.BaseLoader`, which constructs no objects.
- `golangci.yml` goes to Go repos, and `.sqlfluff` — narrowed and lint-only, so it never reformats —
  wherever a `sql_dialect` is declared.
- **`rust-toolchain.toml` is the one template the declaration fills.** Its channel is
  `languages.rust.toolchain`, named by the entry's `toolchainOf`. A declaration without one refuses
  every Rust repo, because a channel rustup cannot resolve fails every cargo call there.

**`merge_pyproject_tools.py`** merges standard tool sections into pyproject.toml using tomlkit (no Go equivalent for lossless TOML editing). **The standard owns exactly the keys it writes**, recorded as `[tool.forge] managed` in each repo's pyproject. That record is what makes retraction possible: a key dropped from the template is removed everywhere on the next sync, because the record proves forge put it there, and the retraction is printed rather than silent. A key absent from the record is the project's and is unreachable from the delete path.

It gates adoption on the same terms, so forcing a key is conditional rather than unconditional. A key the project already sets, to a value the standard disagrees with and the record does not claim, is reported as a conflict and left alone — `Stale` + `ByHand`, so `check` surfaces it and `apply` cannot reach it. A path whose intermediate segment is not a table is the same answer, because reading it as absent would have adoption replace the segment. Agreement is recorded without writing, which is what lets a repo converge; refusing to record it would leave the key outside the standard permanently. The comment above a key is why this matters: forge cannot see prose, so inverting a value it did not write leaves the explanation arguing for a value that is gone.

Per-key ownership recorded at write time replaced whole-section overwrites, which deleted project config three times — a ruff `exclude`, bugbear exemptions, a pydantic mypy plugin, an alembic per-file-ignore. Paths are stored as arrays, not dotted strings, because a segment can contain a dot (`per-file-ignores."__init__.py"`) and the record that authorizes deletion does not get to depend on quoting being right. The record table is rebuilt from scratch on every write, so a resync is byte-identical — the die reporting converged depends on that idempotence.

**The one thing the merge owns outside `[tool]` is a dev pin.** `devPinnedTools` in `dies/pyproject.go`
lists the tools held at the release their pre-commit hook pins — `HookPinnedVersion`, the same
derivation CI's `uvx` lines take — and the script gets each as `--pin NAME==VERSION`. The pin is
rewritten in every `[dependency-groups]` group and `[project.optional-dependencies]` extra naming
the package, keeping its extras and marker, and added to `[dependency-groups] dev` where none does.
Every other element stays the project's, in order, and `[project] dependencies` is never touched.
Ownership is recorded by name as `[tool.forge] pinned`. It is not gated like a key: the hook
already runs that version at every commit, so a differing dev spec is a second answer to a
question CI settles. A write is followed by a lock wherever the repo keeps a `uv.lock`, so the
lock never lags the spec. Raising the ruff hook rev in the declaration is the whole bump.

**The uv-pre-commit hook's rev is the uv release the hook, CI, a release build and forge all run
against `uv.lock`.** A lock records the format `revision` of whichever uv last rewrote it. Two
writers at different releases flip it on every re-lock. `uv lock --check` passes either way. So the
lock after a write runs as `uvx uv@<pin> lock`, never the uv on `PATH`. A lock whose content
changes takes that uv's revision. A lock that changes nothing keeps the revision it found. Where
the declaration pins no uv hook, `Observe` refuses any repo keeping a `uv.lock`, before the merge
writes. A Python release's `build_command` reads the rev out of the committed
`.pre-commit-config.yaml` to install its uv. A `rev:` line moved off the one after the hook's
`repo:` line matches nothing there, and every release fails at that install.
`TestIntegration_AReleaseBuildReadsTheUvHookRev` holds the line in place.

**Custom hook markers** — repos with project-specific hooks use these markers in their `.pre-commit-config.yaml`:

```yaml
# > custom:before:file-checks - Description
# > custom:after:vue - Description
# > custom:after:all - Description
```

The generator preserves these across re-runs. A safety check aborts if unrecognized hooks exist without markers.

A custom section naming a repo the versions file declares takes the declared rev on every run, and a
`- <module>@<ref>` item naming a declared module takes the declared version.

A custom hook sharing a standard hook's id replaces that hook whole, so the declared rev and args never
reach it. `check` reports each one as `ByHand` under its own item, and the config still regenerates.

## CI Standardization System

`ci/blocks/` fragments compose into `.github/workflows/validate.yml`, triggered on `pull_request` and
exposed via `workflow_call` so a release workflow can `needs:` it. Versions come from the same
declaration as the hooks, and the same `# > custom:` markers preserve repo-specific steps.

**`push: main` is emitted only when nothing else covers main.** `ReleaseGatesOnValidate` decides by
reading `release.yml` for a `uses:` naming this workflow. That is detection against the usual
declared-never-detected rule, deliberately: the failure is someone adding a release gate and
forgetting a flag, which a flag cannot prevent. Every unknown answers false and keeps `push`, so the
failure mode is a duplicate run rather than an unvalidated main.

**CI runs the hooks themselves, never copies of their checks.** A copy is a second spelling of the
check, and the two drift: a CI step pinning a different release, or checking a different scope,
passes what the hook fails. So a stack job holds only what no hook runs — the full test suite where
the hook runs `-short`, govulncheck, cargo-audit, a build — and a stack whose every check is a hook,
such as shell or lua, gets no job.

**One job per declared component with checks of its own**, named `<stack>` at the root or
`<stack>-<dir>` below it, run in parallel so a failure names its module. A declared stack with no CI
block is skipped rather than emitting an empty job. That is why docker has no block: a Dockerfile is
built by the deploy, not by validation.

**A stack's setup block opens both its job and the hooks job.** `NN-<category>-setup.yml` puts on
`PATH` what that stack's hooks call: setup-go for the tekwizely hooks, setup-terraform for
`terraform_validate`, setup-node and `npm ci` for the vue scripts. The hooks job has no directory of
its own, so it carries each component's setup rendered for its directory, once per distinct render.
setup-go exports `GOTOOLCHAIN=local`, under which a second setup-go ignores its go.mod's toolchain
line. So the hooks job exports `auto` before running anything.

**One more job, `hooks`, runs every hook a standard block put in the repo's config.**
`ci.HooksToRun` reads them from the committed `.pre-commit-config.yaml`, never from the config the
precommit die would write. CI runs the committed file, and the two differ wherever that die is
blocked or not yet applied. A list taken from the owed config would name hooks the file lacks. A
repo declaring no components gets a workflow holding this job alone, wherever forge maintains its
pre-commit config. The job runs hooks by id, so a custom alias sharing an id runs with it. It drops a
hook off the `pre-commit` stage, and every hook in a custom section: several need a workstation, such
as a running dev stack or a local editor install.

**A custom section whose job is gone holds the workflow back.** Custom sections are keyed
`before:<job>` or `after:<job>`, and `Generate` renders one only beside a job it emits. A section
keyed to a removed job would vanish on the next write, and it is the one part of the file nothing
else can recreate. `ci.OrphanedSections` names each, and the die reports it `ByHand` against
`validate.yml` until the marker moves to a job that exists, such as `before:hooks`.

**The hooks job checks what the push or pull request changed.** That is what the hooks saw at commit
time, so a finding in a file nobody touched cannot fail a push. A change touching a hook or tool
config, a manifest, a lockfile or a toolchain file is the exception, and the job checks every file.
A new ruff rule or linter release matches no source file's type filter. Scoped to the change, it
would grade nothing, and its findings would land on the next author to touch the code. The run
script names each file that widens the scope. The checkout stays one commit deep
and the job fetches only the base commit. pre-commit falls back to a two-dot diff where two commits
share no history on disk, and refcheck's `--moves` reads the range as one. Where no earlier commit
can be fetched, as on a repo's first push, the job checks every file and says so in a notice.

**`runs-on` follows the repo's declared visibility, through `ci.RunnerFor`.** A private repo takes the
self-hosted pool, because GitHub bills hosted minutes on private repos only. Anything not positively
declared private takes the hosted image, and that direction is the safety property: a fork's pull
request on a public repo runs the fork's code, and a self-hosted runner sits inside a private network.
`TestNoProductionCallerOutsideThisPackageNamesTheSelfHostedRunner` keeps the choice in `RunnerFor`.

**The hosted image is a pinned release, `runners.hosted` in the declaration.** `ubuntu-latest` moves
to a new release on GitHub's schedule, and a job that passed the day before then fails with nothing
in the repo changed. Both loaders refuse a declaration naming no release, or naming `ubuntu-latest`.
`ApplyWorkflowPins` rewrites `ubuntu-latest` and every `ubuntu-NN.NN` in a hand-written workflow to
the pin, so a matrix and the `if:` reading it move together. A variant such as `ubuntu-24.04-arm`
names a different machine and is left alone, and so are macOS and Windows labels. So is a label
right after `:` or `/`, which is an image tag, and a list naming two distinct Ubuntu labels, which
tests several releases on purpose.

**The die also writes `.github/actionlint.yaml` into every repo it generates CI for**, declaring the
labels actionlint cannot discover. actionlint compiles in the hosted images that existed at its
release, so a pin newer than the pinned actionlint is an unknown label without it. The self-hosted
pool is declared only where the workflow names it. Its absence in a public repo makes actionlint
reject a hand-written workflow reaching the pool. A repo that turns public has the pool taken out; a
hand-written config at `.yaml` or `.yml` is reported and left alone. The die lints against that
config too, and holds back `validate.yml` or a repin it refuses.

**The output is `validate.yml`, not `ci.yml`.** `ci.yml` is the name a hand-written pipeline takes
by default, and generating over one would destroy work nothing could recover. The die refuses any `validate.yml`
lacking the `# forge-toolchain:` header for the same reason.

**`forge repos check` is the pre-rollout gate.** The `precommit` and `ci` dies run actionlint and
`pre-commit validate-config`, and report what would block a real sync as `ByHand`. One trap no schema
catches: `defaults.run.working-directory` does not apply to action inputs, so a path in one needs
`{{dir}}`.

**Every pinned version comes from the declaration.** It resolves from `$FORGE_VERSIONS_FILE`, then
the `versions_file` config key, and unset is an error, because forge ships no pins of its own.
`forge toolchain show` prints the path it read.

**The declared pins reach workflows forge did not write.** The `ci` die rewrites the declared action,
the release a downloading action installs, and `go install`, uvx and binary versions in every hand-written workflow and in every custom section of
the generated one, through `ApplyWorkflowPins`, and changes nothing else in them. A runtime version is
left alone, because a hand-written matrix may test several on purpose. So is a commit pin on an
action the declaration names by tag alone, because the tag would loosen it.

**A third-party action is used by commit, a first-party one by tag.** An action outside `actions/*`
declares `sha` beside an exact `version`, and every line naming it becomes `@<sha> # <version>`,
whatever ref it held, so an older commit moves with the declaration. A tag's owner can move it to
other code after review, and a commit cannot move. `actions/*` stays on a major tag: a commit there
is a hand-updated hash on a first-party tool for no gain. Load refuses a third-party entry without a
full commit, and a commit beside a tag naming no single release, such as `v7`.

**A commit pins an action's code and nothing it downloads.** setup-uv installs the newest uv, and
setup-terraform the newest terraform, unless an input names one. `downloadingActions` maps each to
that input and to the binary its release resolves as, and `ApplyActionReleaseInputs` fills it on
every step naming the action, in a generated workflow or a hand-written one. uv takes the uv-lock
hook's rev. terraform is a `binaries` entry, so a declaration without one refuses every Terraform
repo. A hand-written step without the input is left without it.

**The `gomod` die writes both Go directives, from that declaration.** The two look like one setting
and are not: `go` is a floor a consumer must clear, `toolchain` is what this build switches
up to. Taking a fixed standard library by raising the floor has a measured cost — `go install
<tool>@latest` prefers a release the *installing* machine can build, so a module floored above the
Go on a machine is skipped there silently, returning 0 and leaving the old binary while the
installer reports the machine converged.

Measured against a system Go one patch below the pin. A module floored above it to clear five
standard-library advisories was skipped: `go install @latest` returned 0 in a quarter of a second
and left the old binary in place. A module taking a toolchain directive against the same advisories
worked — govulncheck clean, `go list` still reporting the lower version, `GOTOOLCHAIN=local go
build` still working, and the release installing on that machine. So raising a floor stays a compatibility decision belonging to whoever
owns the module, never to a fleet-wide sweep.

Both numbers come from the declaration's `languages.go`. Bump the toolchain on a standard-library
advisory; `govulncheck` in generated CI is what reports one.

**A floor moves in either direction.** Lowering one is safe for every consumer; raising one excludes
them. No module is floored above the declaration, so nothing in the portfolio is stricter than the
fleet. The declaration carries its own exceptions below the floor, and a retired repo is not drift.

A module already floored at or above the toolchain pin gets no toolchain line, since it would be a
second copy of the same fact — and an existing one there is reported `Undeclared` rather than
deleted, because forge does not remove what it did not put there. Changes are itemized per module
directory, because a triad repo has three and a row saying `go.mod` could not say which.

**The same die pins the golang image in each module's Dockerfiles.** Every `FROM golang:` line in a
declared Go component directory takes the release CI's setup-go reads from the converged go.mod:
the toolchain directive where there is one, and the floor where there is not. The official image
sets `GOTOOLCHAIN=local`, which ignores go.mod's toolchain line. So the tag alone decides what
compiles the shipped binary. A floating `golang:alpine` follows Go's newest release, which no CI
run tested. The rewrite keeps `--platform` and the stage name.

Only a bare release and the `alpine` family are rewritten. The official image publishes a new Go
release only on its newest distro releases, so a variant naming a distro release, such as
`alpine3.22` or `bookworm`, may have no tag for the pin. That variant is reported `ByHand`. So are
a digest, which outranks the tag beside it, and a build-argument tag, which whoever runs the build
fills. `buildGo` reads only the go.mod values `Perform` never rewrites: the `go` line where no
floor is declared, and a toolchain line no pin owes. So the image and go.mod agree whichever
change applies first. A Dockerfile outside a component directory is not read.

The die cannot see the prose above a `FROM` line, so a comment explaining the old tag survives the
rewrite and argues for it. The commit landing a rewrite rewrites that comment too.

A raise made on the day of a Go release can fail the image job until Docker Hub publishes
`golang:<pin>-alpine`. Production keeps the previous image meanwhile.

`Perform` converges the whole module rather than the one directive its `Change` names. A `Change`
carries the file, not the line, and a module can drift on both at once — so the second change for
one `go.mod` arrives after the first settled it and reports `Skipped`.

**The `pyproject` die is separate from `precommit`, and stays that way.** Adopting one better setting
through the full sync means also fanning out whatever the declaration currently pins, to every Python
repo at once. Coupling a cheap change to an expensive one is what stops the cheap change being made.
Its `Observe` is the merge script's own `--check`, whose JSON report carries the unified diff that
becomes the `Change`'s `Patch`, so `forge repos plan pyproject` is the only way to preview a
retraction. `uv run --no-project` stops uv building the repo being edited just to run the script.

**The `precommit` die installs the git hooks for every stage the config declares**, where the registry
declares the repo; an uninstalled stage means those hooks silently never run. On a repo maintained by
the stamp alone, the same stages are measured and reported `ByHand` rather than installed. Stages are
parsed from the `stages:` lists, never substring-matched, because `commit-msg` is a substring of
`prepare-commit-msg`.

## Release

`release.yml` triggers on push to `main` and go-semantic-release creates the tag, because a tag pushed
with `GITHUB_TOKEN` does not retrigger Actions.

## Never write the breaking-change trailer in a commit message

Those two words anywhere in a message cut a major here, and a major on a Go module
with no `/vN` path is an outage: `go install …@latest` stops seeing the tag, every
installed binary is stranded, and recovery is a reinstall on each machine. The
analyzer matches unanchored and ORs past `.semrelrc`, so nothing switches it off.
A commit that merely *discusses* the trailer cuts one too — say "that marker".
Deliberate majors are `chore(release-major)`. Reset procedure and the measurement:
`standards/release.md` § "Never write the breaking-change trailer in a Go repo's
commit message".
