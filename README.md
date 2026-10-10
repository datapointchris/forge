# Forge

Reconcile each git repository in a registry against a set of standards.

Forge reads a repo list from config and operates on each repo: reconciling it with
reusable operations called **dies**, running an ad-hoc command in it, running its
declared test suites, or running its standard pre-commit hooks over every file.

**Forge's unit of work is one repo.** Running an operation across the whole portfolio is machinery
for reaching many repos, not a different kind of operation. A question about the estate as a whole
is a different tool's job, whatever answers it.

The test is what a command operates on, not whether it writes: a per-repo read still belongs here,
and a question about the estate as a whole belongs elsewhere even if answering it means writing
something.

## Installation

### From GitHub Releases

Download the latest binary from [Releases](https://github.com/datapointchris/forge/releases). Builds are available for linux and darwin on amd64 and arm64.

### From Source

```bash
go install github.com/datapointchris/forge@latest
```

### Self-Update

```bash
forge update
```

Downloads and installs the latest release binary from GitHub. No Go toolchain required.

## Configuration

### Repo Registry

`$XDG_DATA_HOME/forge/repos.json` — defines the repos forge operates on. A
registry maintained elsewhere is named by `repos_registry` in forge's own
config, and `forge config show` prints which layer answered.

Override a single run with `-c <path>`. It is declared on the commands that read
the registry and on no others, so `forge repos`, `forge directories`, `forge cli`
and `forge config` take it anywhere under them, and `forge test` and `forge lint`
take it directly. `forge version` and `forge dies` do not, because neither opens one.

Dies are Go, compiled into the binary, so a development build is the current dies — there is no filesystem mode to switch into.

```json
{
  "owner": "datapointchris",
  "host": "https://github.com",
  "search_paths": ["~/code"],
  "repos": [
    {"name": "widget", "path": "~/src/widget", "status": "active", "description": "What this repo is."},
    {"name": "old-project", "path": "~/src/old", "status": "retired"}
  ]
}
```

Valid statuses: `active` (the default when the field is absent), `dormant`, `retired`. Only `active` repos are swept implicitly — `dormant` is reachable by naming it with `-F`, `retired` is not reachable at all. The optional `description` field is shown by `fleet status`.

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
    path: ~/documents/notes
    toolchain:
      components: []
```

`forge config show` prints what resolved and which layer set it. A missing file is not
an error, and an unknown key is — a misspelled key would leave a directory
undeclared, and an undeclared directory reads as a converged one.

## Usage

### Reconcile the repos

Three verbs over one measurement, Terraform-shaped. `plan` is `apply` minus its last
step — the same walk, stopping before the write — so there is no `--dry-run` for
`apply` to be the opposite of.

```bash
# What is wrong: findings apply cannot fix
forge repos check

# What apply would change, writing nothing
forge repos plan
forge repos plan precommit -F alpha,beta

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

### Run the suites

```bash
forge test                     # every active repo's declared components
forge test alpha beta          # just these
forge test --failed            # print captured output for the failures
forge test --json              # for a caller
forge test -j 4                # repos at once; default is half the CPUs
```

The command per stack is the one `ci/blocks/` generates into that repo's own
workflow, so a local run and CI cannot disagree about what "the tests" means. Vue is
the exception — its block builds without testing — so the component's own
`package.json` says what to run, and the unit script wins over the one wanting a
browser.

Four outcomes, not two, because a pass/fail runner has to lie about half of them.
`no_suite` is a repo with no tests yet, which is not failing, and pytest's exit 5
lands there. `unknown` is a component that could not be measured at all — an absent
runner, missing `node_modules`, a vanished directory, a timeout — and it does not
move the exit code, or a machine missing one runner reports a screen of failures.
Nothing is installed to repair an `unknown`: that is a fact about the machine, and
converging a machine is a configuration manager's job.

Repos run concurrently, never components within a repo — a repo holding an api and
a cli runs both against the same database and ports. Half the CPUs rather than one
per CPU, measured on 16 cores:

```text
jobs=1    195.2s elapsed, 195.2s of suite time
jobs=4     64.9s elapsed, 219.1s
jobs=8     59.4s elapsed, 223.0s
jobs=16    60.2s elapsed, 264.8s
```

Everything is won by four, and sixteen is slower in wall clock while spending 19%
more suite time. At eight the run is as long as the single slowest suite, so going
below a minute means splitting that rather than adding workers.

### Run the hooks

```bash
forge lint                     # every active repo's standard hooks, over every file
forge lint alpha beta          # just these
forge lint --failed            # print each failing hook's output
forge lint --json              # for a caller
forge lint -j 4                # repos at once; default is half the CPUs
```

The hooks are the ones the repo's generated CI runs: every hook a standard block
put in the committed `.pre-commit-config.yaml`. A hook in a custom section is the
repo's own and is not run. Each repo is linted in a throwaway clone of its HEAD,
because several hooks rewrite what they check, so the checkout is never written and
an uncommitted change is not linted. A package's installed dependencies are linked
into the clone, and its `postinstall` script runs there, as `npm ci` runs it in CI.

The outcomes follow `forge test`. `no_hooks` is a repo with no committed config or
none of forge's hooks in it. `unknown` is a hook whose tool is not on this machine,
or a repo that ran out of time, and it does not move the exit code. A name that
matches no repo exits 2.

### Command surfaces

```bash
forge cli spec                 # every installed CLI's command tree
forge cli audit                # where their grammar varies from each other
forge cli snapshot             # keep this reading as the next version
forge cli diff                 # what moved since the last one
forge cli diff 3 4             # between two saved versions
```

Everything is read from the outside — `--help`, and cobra's `__complete` callback
where a tool has one. No bare subcommand is ever run, because the shape most worth
finding is a noun that performs a read when invoked with no verb, and running it to
find out would fire that read.

`audit` compares tools to each other and reports variation without failing; that
register is design guidance. `snapshot` and `diff` compare one tool to its own past,
and that register binds: a command or flag that was there and is not is a broken
contract for whatever called it, which no repo's own suite covers because the break
is at the seam between two.

A tool present on only one side is reported whole rather than as every command it
carries — one that simply was not installed when the snapshot was taken would
otherwise bury the single renamed flag the diff is run to find.

### The version declaration

```bash
forge toolchain show           # what is pinned now, and which file said so
forge stamp spec --json        # which files carry the version stamp, and its form
```

Versions are declared, never discovered. `show` names the file it read:
`$FORGE_VERSIONS_FILE` when set, otherwise whatever `versions_file` points at in
forge's config. With neither, it exits 1 and names both, because forge ships no
pins of its own. Raising a pin is an edit to that file plus a
`stamp.version` bump, then a rollout to one repo before fanning out:
`forge repos apply precommit -F <repo>`.

There is no verb here that writes. Taking whatever tag each upstream has
published is the opposite of pinning, and the arity available for it moves
every hook at once.

### Version and update

```bash
# Show version info
forge version

# Self-update to latest release
forge update
```

## Writing Dies

A die is a Go value implementing `reconcile.Die`, listed in `Builtin()` in
`dies/builtin.go`. It carries its own name, description and tags, which
`forge dies list` and `forge dies search` read.

- `Observe` measures the repo and only reads.
- `Diff` is pure. It compares the observation with the standard and returns the
  `Change`s owed, each marked as a repair apply makes or one a person must.
- `Perform` makes one `Change`, re-checking live that it is still owed. It is the
  only method that writes, and `check` and `plan` never call it.

`dies/property_test.go` runs `Observe` and `Diff` for every die in `Builtin()`
against a fixture and fails if anything on disk changed. A new die is held to that
by being registered.
