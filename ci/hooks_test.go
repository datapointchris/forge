package ci

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/precommit"
)

// owedConfig renders the real pre-commit blocks for these components, as the
// precommit die would for a versioned repo.
func owedConfig(t *testing.T, components []config.Component, scripts ...string) string {
	t.Helper()
	cfg, err := precommit.Generate(os.DirFS("../pre-commit/blocks"), testManifest(t),
		&config.Toolchain{Components: components}, nil, true, scripts)
	if err != nil {
		t.Fatalf("precommit.Generate: %v", err)
	}
	return cfg
}

// coveredBy is what the named stacks' jobs declare they run, read from their
// real blocks.
func coveredBy(t *testing.T, stacks ...string) map[string]bool {
	t.Helper()
	covered := make(map[string]bool)
	for _, stack := range stacks {
		block, err := loadBlock(os.DirFS("blocks"), stack)
		if err != nil || block == "" {
			t.Fatalf("no %s block: %v", stack, err)
		}
		hooks, _ := splitCovers(block)
		for _, hook := range hooks {
			covered[hook] = true
		}
	}
	return covered
}

// preCommitHooks is every hook the real pre-commit blocks run at the pre-commit
// stage, by the category that pulls each block in.
func preCommitHooks(t *testing.T) map[string][]precommit.GeneratedHook {
	t.Helper()
	entries, err := os.ReadDir("../pre-commit/blocks")
	if err != nil {
		t.Fatal(err)
	}
	byCategory := make(map[string][]precommit.GeneratedHook)
	for _, entry := range entries {
		data, err := os.ReadFile("../pre-commit/blocks/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		name := precommit.BlockName(entry.Name())
		for _, hook := range precommit.GeneratedHooks("# generated:" + name + "\n" + string(data)) {
			if len(hook.Stages) == 0 || slices.Contains(hook.Stages, "pre-commit") {
				byCategory[precommit.BlockCategory(name)] = append(byCategory[precommit.BlockCategory(name)], hook)
			}
		}
	}
	return byCategory
}

// stackBlocks names every CI block a declared stack selects.
func stackBlocks(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("blocks")
	if err != nil {
		t.Fatal(err)
	}
	var stacks []string
	for _, entry := range entries {
		if name := precommit.BlockName(entry.Name()); name != "checkout" && name != HooksJob {
			stacks = append(stacks, name)
		}
	}
	return stacks
}

// A hook its stack's job leaves out falls to the hooks job, which has none of
// Go, terraform or node to run it with, or to nothing where it is local.
func TestAStackJobRunsEveryHookItsStackCarries(t *testing.T) {
	hooks := preCommitHooks(t)
	for _, stack := range stackBlocks(t) {
		covered := coveredBy(t, stack)
		carried := make(map[string]bool)
		for _, hook := range hooks[precommit.StackToCategory(stack)] {
			carried[hook.Selector()] = true
			if !covered[hook.Selector()] {
				t.Errorf("the %s job does not run %s from the %s block", stack, hook.Selector(), hook.Block)
			}
		}
		for hook := range covered {
			if !carried[hook] {
				t.Errorf("the %s block covers %s, which no %s pre-commit block carries", stack, hook, stack)
			}
		}
	}
}

func TestTheHooksJobRunsEveryHookNoStackJobCarries(t *testing.T) {
	stacks := make(map[string]bool)
	for _, stack := range stackBlocks(t) {
		stacks[precommit.StackToCategory(stack)] = true
	}
	for category, hooks := range preCommitHooks(t) {
		if stacks[category] {
			continue
		}
		for _, hook := range hooks {
			config := "# generated:" + hook.Block + "\n  - repo: " + hook.Repo + "\n    hooks:\n      - id: " + hook.ID + "\n"
			if hook.Alias != "" {
				config += "        alias: " + hook.Alias + "\n"
			}
			if hook.Entry != "" {
				config += "        entry: " + hook.Entry + "\n"
			}
			if got := HooksToRun(config, nil); !slices.Equal(got, []string{hook.Selector()}) {
				t.Errorf("%s from the %s block runs in no job", hook.Selector(), hook.Block)
			}
		}
	}
}

func TestACoversLineStaysOutOfTheWorkflow(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), "", nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(workflow, "covers:") {
		t.Errorf("a covers line reached the workflow:\n%s", workflow)
	}
}

func TestTheHooksJobRunsWhatNoStackJobCovers(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("go", ".")), coveredBy(t, "go"))

	for _, want := range []string{"check-yaml", "check-json5", "markdownlint", "shellcheck", "shfmt", "codespell", "refcheck"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not run: %v", want, got)
		}
	}
	for _, skipped := range []string{
		"go-vet-repo-mod",         // the go job runs it
		"go-fumpt-repo",           // so too, though no other job has the Go it needs
		"bats",                    // local, and its tool is installed only by the shell job
		"conventional-pre-commit", // grades a commit message, which a pushed tree lacks
	} {
		if slices.Contains(got, skipped) {
			t.Errorf("%s is run: %v", skipped, got)
		}
	}
}

// The shell block is generic, so every repo's config has it, but only a repo
// declaring a shell component has a shell job to run it.
func TestShellHooksRunHereOnlyWithoutAShellJob(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("shell", ".")), coveredBy(t, "shell"))
	if slices.Contains(got, "shellcheck") || slices.Contains(got, "shfmt") {
		t.Errorf("the shell job's hooks run twice: %v", got)
	}
}

// ruff-format alone would select the python block's hook as well as the
// scripts one, and the python job runs that already.
func TestAScriptsHookIsRunByItsAlias(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("python", "."), "bin/tool"), coveredBy(t, "python"))
	if !slices.Contains(got, "ruff-format-scripts") || slices.Contains(got, "ruff-format") {
		t.Errorf("hooks = %v, want ruff-format-scripts and not ruff-format", got)
	}
}

// The python job's mypy never sees a uv script, and uv is the one tool the
// hooks job sets up.
func TestALocalHookCallingUVRunsInTheHooksJob(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("python", "."), "bin/tool"), coveredBy(t, "python"))
	if !slices.Contains(got, "mypy-scripts") {
		t.Errorf("mypy-scripts is not run: %v", got)
	}
}

// Docker has no CI block, so the hooks job is the only job a docker-only repo
// gets.
func TestAStackWithNoCIBlockStillGetsItsHooksRun(t *testing.T) {
	components := comps("docker", ".")
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), components, owedConfig(t, components), nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(workflow, "\n  hooks:\n") || !strings.Contains(workflow, "\n            hadolint-docker\n") {
		t.Errorf("no hooks job running hadolint:\n%s", workflow)
	}
}

func TestARepoWithNoPreCommitConfigGetsNoHooksJob(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), "", nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(workflow, "\n  hooks:\n") {
		t.Errorf("a hooks job with nothing to run:\n%s", workflow)
	}
}

// Its pre-commit config carries the vue block's hooks, which are local, so no
// job but a stack job can run them.
func TestANodeComponentGetsTheVueJob(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("node", "server"), "", nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(workflow, "working-directory: server") || !strings.Contains(workflow, "npm run lint:fix") {
		t.Errorf("no vue job for the node component:\n%s", workflow)
	}
}
