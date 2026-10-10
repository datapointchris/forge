package ci

import (
	"os"
	"regexp"
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
		&config.Toolchain{Components: components}, nil,
		precommit.Observed{Versioning: precommit.Versioned, Scripts: scripts})
	if err != nil {
		t.Fatalf("precommit.Generate: %v", err)
	}
	return cfg
}

// generateFor is the workflow for these components, with the pre-commit config
// the precommit die owes them committed.
func generateFor(t *testing.T, components []config.Component) string {
	t.Helper()
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), components, owedConfig(t, components), nil, Ungated, hostedRunner(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return workflow
}

var nextJobRE = regexp.MustCompile(`(?m)^  [A-Za-z0-9_-]+:\s*$`)

// jobSteps is the named job's text, up to the next job.
func jobSteps(workflow, job string) string {
	_, steps, found := strings.Cut(workflow, "\n  "+job+":\n")
	if !found {
		return ""
	}
	if next := nextJobRE.FindStringIndex(steps); next != nil {
		return steps[:next[0]]
	}
	return steps
}

// Every hook a standard block carries at the pre-commit stage, whatever its
// stack, is one the hooks job runs. No stack job runs a copy of any of them.
func TestTheHooksJobRunsEveryHookAStandardBlockCarries(t *testing.T) {
	entries, err := os.ReadDir("../pre-commit/blocks")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile("../pre-commit/blocks/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		config := "# generated:" + precommit.BlockName(entry.Name()) + "\n" + string(data)
		got := HooksToRun(config)
		for _, hook := range precommit.GeneratedHooks(config) {
			atCommit := len(hook.Stages) == 0 || slices.Contains(hook.Stages, "pre-commit")
			if atCommit != slices.Contains(got, hook.ID) {
				t.Errorf("%s from %s: run = %v, at the pre-commit stage = %v", hook.ID, entry.Name(), !atCommit, atCommit)
			}
		}
	}
}

func TestACustomHookStaysOutOfTheHooksJob(t *testing.T) {
	config := owedConfig(t, comps("python", ".")) +
		"\n# > custom:after:all - Needs a workstation\n  - repo: local\n    hooks:\n      - id: e2e\n        entry: ./run-e2e\n        language: system\n"
	if got := HooksToRun(config); slices.Contains(got, "e2e") {
		t.Errorf("a custom hook runs in CI: %v", got)
	}
}

// ruff-format's id also selects the scripts hook, which carries it under an
// alias, so running both selectors would run that hook twice.
func TestAnAliasedHookRunsUnderItsID(t *testing.T) {
	got := HooksToRun(owedConfig(t, comps("python", "."), "bin/tool"))
	if !slices.Contains(got, "ruff-format") || slices.Contains(got, "ruff-format-scripts") {
		t.Errorf("hooks = %v, want ruff-format and not ruff-format-scripts", got)
	}
}

// The tekwizely Go hooks call the go on PATH, so a Go repo's hooks job sets up
// each module's toolchain, as its go job does.
func TestTheHooksJobSetsUpGoForEachModule(t *testing.T) {
	hooks := jobSteps(generateFor(t, comps("go", "api", "go", "cli")), HooksJob)
	for _, module := range []string{"api", "cli"} {
		if !strings.Contains(hooks, `go-version-file: "`+module+`/go.mod"`) {
			t.Errorf("no Go set up from %s/go.mod:\n%s", module, hooks)
		}
	}
}

// The vue hooks run npm scripts inside the component, so each component's
// packages are installed in the hooks job too.
func TestTheHooksJobInstallsEveryNodeComponent(t *testing.T) {
	hooks := jobSteps(generateFor(t, comps("vue", "web", "node", "server")), HooksJob)
	for _, dir := range []string{"web", "server"} {
		if !strings.Contains(hooks, `- working-directory: "`+dir+`"`) {
			t.Errorf("no install in %s:\n%s", dir, hooks)
		}
	}
	if !strings.Contains(hooks, "node-version-file: .nvmrc") {
		t.Errorf("no Node set up:\n%s", hooks)
	}
}

// setup-terraform names no directory, so two components render it identically.
func TestTwoComponentsRenderingOneSetupGetItOnce(t *testing.T) {
	hooks := jobSteps(generateFor(t, comps("terraform", "infra", "terraform", "modules/net")), HooksJob)
	if got := strings.Count(hooks, "hashicorp/setup-terraform@"); got != 1 {
		t.Errorf("setup-terraform appears %d times, want 1:\n%s", got, hooks)
	}
}

// Docker has no CI block, so the hooks job is the only job a docker-only repo
// gets.
func TestAStackWithNoCIBlockStillGetsItsHooksRun(t *testing.T) {
	workflow := generateFor(t, comps("docker", "."))
	if !strings.Contains(jobSteps(workflow, HooksJob), "\n            hadolint-docker\n") {
		t.Errorf("no hooks job running hadolint:\n%s", workflow)
	}
}

func TestARepoWithNoPreCommitConfigGetsNoHooksJob(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), "", nil, Ungated, hostedRunner(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(workflow, "\n  hooks:\n") {
		t.Errorf("a hooks job with nothing to run:\n%s", workflow)
	}
}

// after:all closes the file rather than a job, so no workflow can orphan it.
func TestOnlyASectionBesideAMissingJobIsOrphaned(t *testing.T) {
	workflow := generateFor(t, comps("shell", ".", "go", "."))
	sections := map[string]string{
		"before:shell": "      - run: echo yq\n",
		"before:hooks": "      - run: echo yq\n",
		"after:go":     "      - run: echo done\n",
		"after:all":    "      - run: echo done\n",
	}

	if got := OrphanedSections(sections, workflow); !slices.Equal(got, []string{"before:shell"}) {
		t.Errorf("OrphanedSections = %v, want [before:shell]", got)
	}
}

// A node component lints with the vue block's hooks, and builds in the vue job.
func TestANodeComponentGetsTheVueJob(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("node", "server"), "", nil, Ungated, hostedRunner(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	vue := jobSteps(workflow, "node-server")
	if !strings.Contains(vue, "working-directory: server") || !strings.Contains(vue, "npm run build") {
		t.Errorf("no vue job for the node component:\n%s", workflow)
	}
}
