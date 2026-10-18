# forge

forge holds every repo in the fleet to one standard. It generates each repo's pre-commit config,
the tool configs those hooks read, and its CI workflow. The blocks it composes them from are
compiled into the binary, and every version they name comes from one declaration file. It also
sets the GitHub settings a repo should carry, and runs locally the hooks and suites CI runs.

forge's unit of work is one repo. Every operation is "do this to a repo". Sweeping the portfolio is
how one operation reaches many repos, not a different kind of operation. `fleet`'s unit is the
fleet as a whole, and it reads each repo only to assemble a view of the whole. So the test for a
command is what it operates on, not whether it writes. A read that runs per repo belongs here. A
question about the fleet belongs in `fleet`, even if answering it one day means writing something.

## Install

```bash
dotfiles packages apply --package forge             # on a fleet box, recorded like any package
go install github.com/datapointchris/forge@latest   # anywhere with Go
forge update                                        # replace this binary with the latest release
forge version                                       # the release installed
```

[Releases](https://github.com/datapointchris/forge/releases) carry builds for linux and darwin on
amd64 and arm64. `forge update` checks the archive against the release's published checksums, and
needs no Go toolchain. On a terminal, forge checks once a day for a newer release and names it.
Setting `NO_AUTO_UPDATE` turns that check off.

The blocks, tool configs and dies are compiled in, and there is no filesystem mode. A development
build from a checkout, `go install .`, carries that checkout's standard.

## Starting from scratch

forge reads two declarations and ships neither: the repo registry, and the file pinning every
version it rolls out. On a fleet box both live in the fleet library clone, and forge's config names
them.

```bash
dotfiles apply                         # installs forge, and the config naming both files
gh auth login                          # the library is private; the GitHub dies read through gh
fleet machine library clone            # repos.json and pinned-versions.json; refuses if present
forge config show                      # each path forge resolved, and the layer that named it
forge toolchain show                   # the declaration parsed: its stamp version and every pin
forge repos list                       # what a bare verb visits; stderr counts what it left out
forge repos check                      # what only a person can settle; exits 3 on a finding
forge repos plan precommit -F <repo>   # what apply would change; exits 1 when something would
forge repos apply precommit -F <repo>  # make it so; the files land as an uncommitted diff
forge repos plan precommit -F <repo>   # exits 0: converged, which proves apply worked
```

`config show` is the command to run when anything reads the wrong place. The registry resolves
from `-c <path>`, then `$FORGE_REPOS_REGISTRY`, then `repos_registry` in forge's config at
`$XDG_CONFIG_HOME/forge/config.yml`, then `$XDG_DATA_HOME/forge/repos.json`. The declaration
resolves from `$FORGE_VERSIONS_FILE`, then `versions_file`. With neither, `toolchain show` exits 1
and names both, and every reconcile verb refuses, because forge ships no pins of its own.

`precommit` deploys into a repo for the first time only where the registry gives it a `toolchain`.
A repo with neither that declaration nor forge's stamp on an existing config is left alone, and its
row says why. A stamped repo the registry has gone quiet about is still maintained, as one
declaring no components, because forge wrote those files and owns keeping them right.

A die that reads GitHub calls `gh`. Without a login, that die reports `unknown`, which never moves
an exit code. An unauthenticated `gh` is not a portfolio of drifted repos, and apply is not the fix
for a login.

Naming a die is the confirmation, so `forge repos apply <die>` runs without asking. Bare
`forge repos apply` prints the plan and asks, showing how many changes across how many repos.
`--yes` skips the prompt. Off a terminal it is required, and the run exits 2 without it rather than
blocking on a closed stdin.

No die commits. A file change lands as an unstaged diff in the checkout, for you to read and
commit. A die that changes a GitHub setting changes it on GitHub.

## The grammar

The noun comes first and the verb last: `forge repos plan`, `forge dies show`, `forge cli diff`. A
die is an argument to a reconcile verb, never a command of its own. The pre-commit config is
`forge repos plan precommit`, and the workflow is `forge repos plan ci`. `forge dies list` names
every die. A parent command run bare prints its help, so the tree is walkable without this file.

Repos are named two ways. The verbs under `repos` and `directories` take `-F a,b`. `test` and `lint`
take repo names as arguments. A name that matches nothing exits 2, because a run that dropped it
would report the rest, and a caller would read the dropped name as passing. The `cli` verbs take
tool names rather than repo names.

The reconcile verbs, `test`, `lint`, the `cli` verbs, `config show`, `stamp spec` and `dies stats`
take `--json` for a caller.

Exit codes follow terraform's `-detailed-exitcode`: 0 converged, 1 changes pending, 2 usage, 3
something is wrong. `plan` answers 0 or 1, and 3 only on a refusal. `check` answers 0 or 3 and never
1, because a repo behind the standard is drift rather than a fault. `test`, `lint` and
`directories run` exit 1 when something failed.

## Which surface answers which question

| Ask | Surface | Reads |
| --- | --- | --- |
| What would bring a repo up to the standard | `repos plan` | each repo's files and GitHub settings, against the compiled-in blocks and the declaration |
| What can only a person settle | `repos check` | the same measurement, read for findings apply cannot fix |
| Which repos does a bare verb touch | `repos list` | the registry |
| Run something that is not a die, everywhere | `repos exec` | each repo's checkout |
| Hold a folder git does not version to the standard | `directories` | `maintained_directories` in forge's config |
| What does a die do, and how often has it run | `dies` | the compiled-in dies, and the run history |
| Do the suites pass | `test` | each repo's declared components |
| Do the hooks pass over every file | `lint` | each repo's committed `.pre-commit-config.yaml`, in a clone of HEAD |
| Did a CLI lose a command or a flag | `cli diff` | each installed CLI's `--help`, against a saved snapshot |
| Where does command grammar vary across the CLIs | `cli audit` | each installed CLI's `--help` and cobra completion |
| What is pinned, and which file said so | `toolchain show` | the declaration |
| Which files carry the rollout stamp | `stamp spec` | the compiled-in dies |
| Which files did forge read | `config show` | forge's config, the environment, and `-c` |

## `repos` — one measurement read three ways, and only apply writes

```bash
forge repos plan                        # every change apply would make, in every active repo
forge repos plan precommit -F alpha,beta
forge repos check                       # findings apply cannot fix, such as a hand-written pipeline
forge repos apply gitignore -F alpha    # one die, unprompted
forge repos apply -F alpha              # every die, after a confirmation
forge repos list                        # the active repos the registry owns
forge repos exec -- git status --short  # anything that is not a die
forge repos exec -f ./one-off.sh
```

`plan` is a prefix of apply's call graph, not apply with a flag turned off. A die's `Observe`
reads, its `Diff` is pure, and its `Perform` is the only method that writes. `plan` and `check`
stop before `Perform`. No die holds a branch asking whether it may write, so there is no such branch
to get wrong. That is why apply has no `--dry-run` to be the opposite of.

Each die returns `Change`s, and the verbs differ in which they keep. A `Change` carries a verdict
(matched, missing, stale, undeclared or unknown) and a repair (automatic, by hand, or none).

- `plan` keeps the automatic repairs. The repo differs from the standard, which is what apply is
  for.
- `check` keeps the by-hand findings: a hand-written pipeline, an unmarked custom hook, a missing
  CLAUDE.md. A repo can be clean on one verb and not the other.
- `unknown` is neither, and never moves the exit code. Unverified is not permission.
- `undeclared` is never actionable. forge does not delete what it did not put there.

With no `-F`, a verb acts on the `active` repos the registry owns. `dormant` repos and reference
clones are left out, and `-F` reaches either by name. Nothing reaches a `retired` repo. A reference
clone is an entry whose `owner` differs from the registry's top-level `owner`. No implicit sweep
writes to a repo the registry does not own. Dormant repos take no further release, so a maintenance
die swept across them would be churn.

`repos exec` tags each line the command prints with the repo's registry name and a tab, on the
stream it was written to. Each repo's status and the closing tally go to stderr, so stdout carries
only the commands' output. The command sees the repo's registry name as `FORGE_REPO_NAME`, because
a checkout's basename is not unique. A command exiting 2 is reported as skipped rather than failed.
`exec` skips any directory without a `.git`. Anything worth running twice belongs in a die, which
gets plan, check and apply.

## What a repo gets — generated from blocks, stamped, and never written over a hand-written file

The `precommit` die composes `.pre-commit-config.yaml` from the numbered blocks in
`pre-commit/blocks/`. Which blocks apply, and the directory each runs in, come from the repo's
declared `toolchain.components`, never a filesystem probe. The portfolio has several layouts for
where a Go module lives, and no layout reveals a SQL dialect. The same die deploys the tool configs those
hooks read, from `pre-commit/configs/`. Where the registry declares the repo, it installs the git
hooks for every stage the config names, because an uninstalled stage is hooks that silently never
run.

The `ci` die composes `.github/workflows/validate.yml` from `ci/blocks/`. It runs on pull requests
and is callable through `workflow_call`, so a release workflow can `needs:` it. Each declared
component with checks of its own gets one job, holding what no hook runs, such as its full test
suite, a vulnerability scan or a build. One more job, `hooks`, runs every standard hook from the
committed pre-commit config. CI runs the hooks themselves rather than copies of their checks,
because a copy pinning a different release passes what the hook fails.

What forge refuses to overwrite:

- **A hand-written pipeline.** The output is `validate.yml`, never `ci.yml`. `ci.yml` is the name a
  hand-written pipeline takes by default, and generating over one would destroy work nothing could
  recover. A `validate.yml` without forge's stamp is reported and left alone.
- **A hand-written tool config.** Every template carries `# forge-managed` on its first line. A
  file at a managed path without it is reported rather than overwritten.
- **A custom hook.** A repo's own hooks sit under a marker, and regeneration keeps them. A hook in
  the config with no marker blocks regeneration and is reported by `check`.

```yaml
# > custom:before:file-checks - Description
# > custom:after:vue - Description
# > custom:after:all - Description
```

Blocks name no version. Every rev, action version, language floor and binary pin comes from the
declaration, and a pin the declaration cannot fill is a refusal rather than a placeholder shipped
to a repo. The `ci` die also rewrites the declared pins in hand-written workflows, and changes
nothing else in them.

The pre-commit config and the files the `ci` die writes open with `# forge-toolchain: N`, where N
is the declaration's `stamp.version`. The stamp is how a rollout tells a repo on the current
standard from one behind it, so bump it on any pin change. `forge stamp spec` names each file that
carries it.

`runs-on` follows the repo's declared `visibility`. A private repo takes the self-hosted pool,
because GitHub bills hosted minutes on private repos only. Anything not declared private takes the
hosted image the declaration pins. A fork's pull request on a public repo runs the fork's code, and
a self-hosted runner sits inside a private network.

## `directories` — the same verbs for what git does not version, plus `run`

```bash
forge directories list
forge directories check
forge directories plan -F claude
forge directories apply precommit -F claude
forge directories run -F claude         # run the generated hooks, since nothing else will
```

A maintained directory is a folder held to the standard without a remote: a home directory, a
Syncthing folder. It gets the same generated config and tool configs a repo gets. Dies that read a
remote, a branch or a workflow report themselves not-applicable rather than vanishing, so a row says
why it found nothing.

`run` is the verb `repos` does not have. A repo's hooks run on every commit and in CI on every pull
request. A directory has neither, so its generated config would otherwise never run.

pre-commit needs a git index to know which files exist. forge builds a throwaway one: a bare
repository in its cache, with the directory as its work tree. Nothing is written inside the
directory, because a `.git` in a file-synced folder conflicts on every peer. The directory's own
`.gitignore` decides which files are examined. Hooks that fix rather than report rewrite files, as
they would at commit time.

The directories are declared in forge's config, never in the registry. Several tools read the
registry, and each takes an entry there to be a git repo with a GitHub remote. A key only forge acts
on would change what iterating the registry means for all of them.

```yaml
maintained_directories:
  - name: claude
    path: ~/.claude
    description: Editor and agent configuration. Synced, not versioned.
    toolchain:
      components:
        - {stack: python, dir: .}
        - {stack: shell, dir: .}

  # An empty components list still declares the directory, and it gets the
  # blocks no stack gates. Omitting toolchain leaves a directory forge has never
  # written to untouched.
  - name: notes
    path: ~/documents/notes
    toolchain:
      components: []
```

A missing config file is not an error. An unknown key is, because a misspelled key leaves a
directory undeclared, and an undeclared directory reads as a converged one.

## `test` — every repo's suites, run the way its CI runs them

```bash
forge test                              # every active repo's declared components
forge test alpha beta                   # just these
forge test --failed                     # print captured output for the failures
forge test -j 4                         # repos at once; the default is half the CPUs, at least two
```

A stack that carries a suite runs the command its CI block runs, so a local run and CI cannot
disagree about what "the tests" means. `tested` in `suites/suites.go` lists those stacks. A Vue or
Node component runs its `test:unit` script where it has one and `test` otherwise, as its CI job
does, because `test` is where a frontend puts its browser run.

A component turns out `passed`, `failed`, `no_suite` or `unknown`. A pass/fail runner would have to
lie about the last two. `no_suite` is a repo with no tests yet, and pytest's exit 5 lands there.
`unknown` is a component that could not be measured: an absent runner, missing `node_modules`, a
timeout. It does not move the exit code, or a machine missing one runner reports a screen of
failures. Nothing is installed to repair it, because that is a fact about the machine, and
converging a machine is a configuration manager's job.

Repos run concurrently, and components within a repo never do. A repo holding an api and a cli runs
both against the same database and ports. The default is half the CPUs, because `go test` and
pytest already spread across cores. Measured on 16 cores across the portfolio:

```text
jobs=1    195.2s elapsed, 195.2s of suite time
jobs=4     64.9s elapsed, 219.1s
jobs=8     59.4s elapsed, 223.0s
jobs=16    60.2s elapsed, 264.8s
```

Everything is won by four. Sixteen is slower in wall clock than eight, and spends 19% more suite
time. At eight the run is as long as the slowest single suite, so a shorter run means splitting
that suite rather than adding workers.

## `lint` — the hooks CI runs, over every file, in a clone that is thrown away

```bash
forge lint                              # every active repo's standard hooks, over every file
forge lint alpha beta                   # just these
forge lint --failed                     # print each failing hook's output
```

The hooks are the ones the repo's generated CI runs: every hook a standard block put in the
committed `.pre-commit-config.yaml`. A hook in a custom section is the repo's own and is not run,
since some need a workstation. CI runs the hooks over what a push changed, and `lint` runs them over
every file, so `lint` can fail where CI passes.

Each repo is linted in a throwaway clone of its HEAD under forge's cache, because several hooks
rewrite what they check. The checkout is never written, and an uncommitted change is not linted.
Each package's `node_modules` is mirrored into the clone, and its `postinstall` runs there, as
`npm ci` runs it in CI. A timeout or Ctrl-C stops every tool the run started and removes each clone
before forge exits.

A repo turns out `passed`, `failed`, `no_hooks` or `unknown`. `no_hooks` is a repo with no
committed config, or none of forge's hooks in it. `unknown` is a hook whose tool is not on this
machine, a hook environment pre-commit could not set up, or a repo that ran out of time. It does
not move the exit code.

## `dies` — the library, which executes nothing

```bash
forge dies list                         # every die, with its run count
forge dies show precommit
forge dies search gitignore             # by name, description or tag
forge dies stats precommit              # its run history
```

A die runs only as the argument to a reconcile verb. A die is a Go value implementing
`reconcile.Die`, listed in `Builtin()` in `dies/builtin.go`. It carries its own name, description
and tags, which `list` and `search` read, so no side file can disagree with it.

- `Observe` measures the repo and only reads.
- `Diff` is pure. It compares the observation with the standard and returns the `Change`s owed,
  each marked as a repair apply makes or one a person must.
- `Perform` makes one `Change`, re-checking live that it is still owed. It is the only method that
  writes, and `check` and `plan` never call it.

`dies/property_test.go` runs `Observe` and `Diff` for every die in `Builtin()` against a fixture,
and fails if anything on disk changed. Registering a die is what holds it to that.

## `cli` — command surfaces read from the outside, never by running a bare subcommand

```bash
forge cli spec                          # every installed CLI's command tree
forge cli audit                         # where their grammar varies from each other
forge cli snapshot                      # keep this reading as the next version
forge cli diff                          # what moved since the last snapshot
forge cli diff 3 4                      # between two saved versions
```

With no arguments, the tools are the active repos in the registry whose binary is on `PATH`.
Everything is read from `--help`, and from cobra's `__complete` callback where a tool has one. No
bare subcommand is ever run. The shape most worth finding is a noun that performs a read when
invoked with no verb, and running it to find out would fire that read.

`audit` compares tools to each other and reports variation without failing, because that register
is design guidance. `snapshot` and `diff` compare one tool to its own past, and that register binds.
A command or flag that was there and is not is a broken contract for whatever called it. No repo's
own suite covers it, because the break is at the seam between two. Both exit 0 whatever they find.

A tool present on only one side of a diff is reported whole, rather than as every command it
carries. A tool missing when the snapshot was taken would otherwise bury the one renamed flag the
diff is run to find.

## `toolchain` and `stamp` — pins are declared, never discovered

```bash
forge toolchain show                    # what is pinned, and which file said so
forge stamp spec --json                 # which files carry the rollout stamp, and its form
```

Raising a pin is an edit to the file `toolchain show` names, plus a `stamp.version` bump. Roll it
out to one repo before fanning out: `forge repos apply precommit -F <repo>`.

No forge command writes a pin. A pin is chosen. Taking whatever each upstream last tagged is the
opposite of pinning, and one command doing it would move every hook in every repo at once.

`fleet versions` reads `forge stamp spec --json` at run time rather than keeping its own copy, so
the installed forge is the one authority on where stamps live.

## The registry, and where forge writes

The registry is JSON, shared with the other tools that read the portfolio. forge reads the
top-level `owner`, and `sync_base`, where the `synced-dirs` die keeps each repo's directories.

- `status` is `active`, `dormant` or `retired`, and it is required. An entry without one is left
  out of every bare sweep, because forge supplies no meaning for an absent status.
- `owner` names the GitHub owner. An entry whose owner differs from the registry's is a reference
  clone.
- `visibility` picks the runner a generated workflow names.
- `toolchain.components` is what the generators and `forge test` read. `toolchain` also takes
  `sql_dialect`, `shellcheck_disable` and `exclude`.
- `synced_dirs` lists repo-relative directories the `synced-dirs` die links into the sync base.
- `binary` names the command a repo installs, where `forge cli` cannot read it out of the repo.
- `description` is what `fleet status` shows.

```json
{
  "owner": "datapointchris",
  "sync_base": "~/dev/repos",
  "repos": [
    {
      "name": "forge",
      "owner": "datapointchris",
      "visibility": "public",
      "path": "~/tools/forge",
      "status": "active",
      "description": "What this repo is.",
      "toolchain": {"components": [{"stack": "go", "dir": "."}, {"stack": "actions", "dir": "."}]}
    }
  ]
}
```

`-c <path>` overrides the registry for one run. It is declared on `repos`, `directories`, `cli`,
`config`, `test` and `lint`, the commands that open the registry, and on no others.

Beyond the repos and directories it acts on, forge keeps three things on the machine:

- `$XDG_CACHE_HOME/forge/` holds what a run rebuilds: the throwaway clones `lint` makes, and the
  bare index for each maintained directory.
- `$XDG_DATA_HOME/forge/cli-surface/` holds each `cli snapshot`, numbered. Nothing is pruned.
- `~/.local/share/forge/stats.jsonl` gets one line per die each time `check`, `plan` or `apply`
  runs, under either noun. `forge dies stats` reads it.
