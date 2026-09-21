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

func TestTheHooksJobRunsWhatNoStackJobCovers(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("go", ".")), map[string]bool{"go": true})

	for _, want := range []string{"check-yaml", "check-json5", "markdownlint", "shellcheck", "shfmt", "codespell", "refcheck"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not run: %v", want, got)
		}
	}
	for _, skipped := range []string{
		"go-vet-repo-mod",         // the go job runs it
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
	got := HooksToRun(owedConfig(t, comps("shell", ".")), map[string]bool{"shell": true})
	if slices.Contains(got, "shellcheck") || slices.Contains(got, "shfmt") {
		t.Errorf("the shell job's hooks run twice: %v", got)
	}
}

// ruff-format alone would select the python block's hook as well as the
// scripts one, and the python job runs that already.
func TestAScriptsHookIsRunByItsAlias(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("python", "."), "bin/tool"), map[string]bool{"python": true})
	if !slices.Contains(got, "ruff-format-scripts") || slices.Contains(got, "ruff-format") {
		t.Errorf("hooks = %v, want ruff-format-scripts and not ruff-format", got)
	}
}

// Docker has no CI block, so its repo was owed no workflow at all until the
// hooks job gave it one.
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
