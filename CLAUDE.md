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

Three categories are seeded by `Generate` rather than by a component:

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

**The shell block's bats hook is guarded both ways.** A repo with no `tests/*.bats` passes; a repo
that has them and lacks bats fails at 127. A green `language: system` hook once hid seven shellcheck
findings by reporting success there.

**Blocks name no version.** Each writes `{{pin}}` where one belongs, generation fills it from the
declaration, and a pin the declaration cannot fill is a refusal rather than a placeholder shipped to
a repo (`toolchain.Unpinned`, `TestBlocksNameNoVersion`). Generation stamps the declaration's
`version` as `# forge-toolchain: N`; bump it on any pin change, because the stamp is what staged
rollout reads. A tool CI runs whose pre-commit hook pins its release takes that release, as
`hookPinnedTools` maps them, and a `binaries` entry for one is refused as a second copy.
`toolchain/testdata/toolchain.yml` is the test fixture, read by no command, and its values name
tools rather than releases.

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
  already runs, since a template nothing conforms to cannot measure drift.
- `golangci.yml` goes to Go repos, and `.sqlfluff` — narrowed and lint-only, so it never reformats —
  wherever a `sql_dialect` is declared.

**`merge_pyproject_tools.py`** merges standard tool sections into pyproject.toml using tomlkit (no Go equivalent for lossless TOML editing). **The standard owns exactly the keys it writes**, recorded as `[tool.forge] managed` in each repo's pyproject. That record is what makes retraction possible: a key dropped from the template is removed everywhere on the next sync, because the record proves forge put it there, and the retraction is printed rather than silent. A key absent from the record is the project's and is unreachable from the delete path.

It gates adoption on the same terms, so forcing a key is conditional rather than unconditional. A key the project already sets, to a value the standard disagrees with and the record does not claim, is reported as a conflict and left alone — `Stale` + `ByHand`, so `check` surfaces it and `apply` cannot reach it. A path whose intermediate segment is not a table is the same answer, because reading it as absent would have adoption replace the segment. Agreement is recorded without writing, which is what lets a repo converge; refusing to record it would leave the key outside the standard permanently. The comment above a key is why this matters: forge cannot see prose, so inverting a value it did not write leaves the explanation arguing for a value that is gone.

Per-key ownership recorded at write time replaced whole-section overwrites, which deleted project config three times — a ruff `exclude`, bugbear exemptions, a pydantic mypy plugin, an alembic per-file-ignore. Paths are stored as arrays, not dotted strings, because a segment can contain a dot (`per-file-ignores."__init__.py"`) and the record that authorizes deletion does not get to depend on quoting being right. The record table is rebuilt from scratch on every write, so a resync is byte-identical — the die reporting converged depends on that idempotence.

**Custom hook markers** — repos with project-specific hooks use these markers in their `.pre-commit-config.yaml`:

```yaml
# > custom:before:file-checks - Description
# > custom:after:vue - Description
# > custom:after:all - Description
```

The generator preserves these across re-runs. A safety check aborts if unrecognized hooks exist without markers.

A custom section naming a repo the versions file declares takes the declared rev on every run.

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

**One job per declared component**, named `<stack>` at the root or `<stack>-<dir>` below it, run in
parallel so a failure names its module. A declared stack with no CI block is skipped rather than
emitting an empty job. That is why docker has no block: a Dockerfile is built by the deploy, not by
validation.

**One more job, `hooks`, runs the pre-commit hooks no stack job covers.** `ci.HooksToRun` reads them
from the committed `.pre-commit-config.yaml`, never from the config the precommit die would write.
CI runs the committed file, and the two differ wherever that die is blocked or not yet applied. A
list taken from the owed config would name hooks the file lacks. A repo declaring no components gets
a workflow holding this job alone, wherever forge maintains its pre-commit config.

**The hooks job drops a hook a stack job here runs, and a stack block names each one it runs.** That
is its `# covers:` line, which names hooks rather than a stack because a stack job runs only the
checks written into it. `TestAStackJobRunsEveryHookItsStackCarries` holds each stack block to every
hook its stack's pre-commit blocks carry, so a hook added to one needs a step in the other. The job
also drops a hook off the `pre-commit` stage, and a local hook, whose tool only a stack job installs.
A local hook calling uv is the exception, because this job sets uv up. The shell block is why
coverage is decided per repo. Every config carries it, and only a repo declaring a shell component
has a job running it.

**The hooks job checks what the push or pull request changed.** That is what the hooks saw at commit
time, so a finding in a file nobody touched cannot fail a push. The checkout stays one commit deep
and the job fetches only the base commit. pre-commit falls back to a two-dot diff where two commits
share no history on disk, and refcheck's `--moves` reads the range as one. Where no earlier commit
can be fetched, as on a repo's first push, the job checks every file and says so in a notice.

**`runs-on` follows the repo's declared visibility, through `ci.RunnerFor`.** A private repo takes the
self-hosted pool, because GitHub bills hosted minutes on private repos only. Anything not positively
declared private takes `ubuntu-latest`, and that direction is the safety property: a fork's pull
request on a public repo runs the fork's code, and a self-hosted runner sits inside a private network.
`TestNoProductionCallerOutsideThisPackageNamesTheSelfHostedRunner` keeps the choice in `RunnerFor`.

**The die also writes `.github/actionlint.yaml`, only where the workflow names the pool**, because
actionlint knows GitHub's labels and nothing else. Its absence in a public repo makes actionlint
reject a hand-written workflow reaching the pool. A repo that turns public has it removed; a
hand-written one at `.yaml` or `.yml` is reported and left alone.

**The output is `validate.yml`, not `ci.yml`.** Several repos carry a hand-written `ci.yml`, and
generating over one would destroy work nothing could recover. The die refuses any `validate.yml`
lacking the `# forge-toolchain:` header for the same reason.

**`forge repos check` is the pre-rollout gate.** The `precommit` and `ci` dies run actionlint and
`pre-commit validate-config`, and report what would block a real sync as `ByHand`. One trap no schema
catches: `defaults.run.working-directory` does not apply to action inputs, so a path in one needs
`{{dir}}`.

**Every pinned version comes from the declaration.** `versions_file` resolves like `repos_registry` —
flag, then `$FORGE_VERSIONS_FILE`, then the config key — and unset is an error, because forge ships
no pins of its own. `forge toolchain show` prints the path it read.

**The declared pins reach workflows forge did not write.** The `ci` die rewrites the declared action,
`go install`, uvx and binary versions in every hand-written workflow and in every custom section of
the generated one, through `ApplyWorkflowPins`, and changes nothing else in them. A runtime version is
left alone, because a hand-written matrix may test several on purpose. So is an action pinned to a
commit, which is a stronger pin than the tag that would replace it.

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

**A declared floor the pinned linter cannot build is refused, never written.** Generated CI sets up
exactly the floor under `GOTOOLCHAIN=local` and then installs golangci-lint, so a floor below its
own minimum fails Lint in every Go repo at once with nothing wrong in any of them. The declaration
carries that bottom as `languages.go.binding_minimum` — declared rather than derived, because
reading it needs the module proxy and a die that reaches the network to decide one line is a die
that fails offline. The refusal is `ByHand`, so it surfaces in `check` and `apply` cannot reach it.

**A floor moves in either direction.** Lowering one is safe for every consumer; raising one excludes
them. No module is floored above the declaration, so nothing in the portfolio is stricter than the
fleet. The declaration carries its own exceptions below the floor, and a retired repo is not drift.

A module already floored at or above the toolchain pin gets no toolchain line, since it would be a
second copy of the same fact — and an existing one there is reported `Undeclared` rather than
deleted, because forge does not remove what it did not put there. Changes are itemized per module
directory, because a triad repo has three and a row saying `go.mod` could not say which.

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
