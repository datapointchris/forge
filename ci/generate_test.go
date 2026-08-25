package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/toolchain"
)

func testManifest(t *testing.T) *toolchain.Toolchain {
	t.Helper()
	manifest, err := toolchain.Load(os.DirFS("../pre-commit"))
	if err != nil {
		t.Fatalf("toolchain.Load: %v", err)
	}
	return manifest
}

func comps(pairs ...string) []config.Component {
	var components []config.Component
	for i := 0; i < len(pairs); i += 2 {
		components = append(components, config.Component{Stack: pairs[i], Dir: pairs[i+1]})
	}
	return components
}

// nomad holds api/ and cli/ as separate Go modules. One serial job would hide
// which failed and make them share a setup step.
func TestGenerateEmitsAJobPerComponent(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t),
		comps("go", "api", "go", "cli", "vue", "web"), nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, job := range []string{"  go-api:", "  go-cli:", "  vue-web:"} {
		if !strings.Contains(workflow, job) {
			t.Errorf("missing job %q:\n%s", job, workflow)
		}
	}
	for _, dir := range []string{"        working-directory: api", "        working-directory: cli", "        working-directory: web"} {
		if !strings.Contains(workflow, dir) {
			t.Errorf("missing %q", dir)
		}
	}

	// Action inputs resolve from the workspace root, not working-directory, so
	// {{dir}} must have been expanded to the component path.
	if !strings.Contains(workflow, `go-version-file: "api/go.mod"`) {
		t.Errorf("{{dir}} not expanded in an action input:\n%s", workflow)
	}
	if strings.Contains(workflow, "{{dir}}") {
		t.Error("unexpanded {{dir}} placeholder left in output")
	}
	// Every job needs its own checkout — jobs do not share a workspace.
	if got := strings.Count(workflow, "actions/checkout@"); got != 3 {
		t.Errorf("checkout appears %d times, want 3 (one per job)", got)
	}
}

// Development is trunk-based, so pull_request alone would mean the workflow
// almost never runs, and workflow_call only fires for a repo whose release
// pipeline gates on it. Seven repos had no repo-wide lint at all; generating
// them a workflow with no reachable trigger would have looked like a fix.
func TestGenerateRunsOnPushToMain(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("python", ""), nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, trigger := range []string{"  push:\n    branches: [main]", "  pull_request:", "  workflow_call:"} {
		if !strings.Contains(workflow, trigger) {
			t.Errorf("missing trigger %q:\n%s", trigger, workflow)
		}
	}
}

// Where a release gates on this workflow it already runs on a push to main, so
// emitting push here runs every job twice for one commit.
func TestGenerateOmitsPushWhenTheReleaseGatesOnIt(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("python", ""), nil, Gated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if strings.Contains(workflow, "  push:") {
		t.Errorf("push trigger duplicates the release run:\n%s", workflow)
	}
	// workflow_call is what covers main once push is gone. Losing it would leave
	// a release gating on a workflow it cannot call.
	for _, trigger := range []string{"  pull_request:", "  workflow_call:"} {
		if !strings.Contains(workflow, trigger) {
			t.Errorf("missing trigger %q:\n%s", trigger, workflow)
		}
	}
}

func TestReleaseGatesOnValidate(t *testing.T) {
	const gate = "jobs:\n  validate:\n    uses: ./.github/workflows/validate.yml\n"

	cases := []struct {
		name      string
		workflows map[string]string
		want      ReleaseGating
	}{
		{"no workflows at all", nil, Ungated},
		{
			"release.yml gates on the generated workflow",
			map[string]string{"release.yml": gate},
			Gated,
		},
		{
			// learning and nomad ship a nested CLI from release-cli.yml, and
			// matching release.yml alone missed both.
			"a differently-named release workflow gates on it",
			map[string]string{"release-cli.yml": gate},
			Gated,
		},
		{
			"one of several workflows gates on it",
			map[string]string{
				"deploy-docs.yml": "jobs:\n  docs:\n    runs-on: ubuntu-latest\n",
				"release-cli.yml": gate,
			},
			Gated,
		},
		{
			// The shape every repo had before this: the release gated on a
			// hand-written ci.yml, so validate.yml still needs its own push.
			"release gates on a hand-written ci.yml",
			map[string]string{"release.yml": "jobs:\n  validate:\n    uses: ./.github/workflows/ci.yml\n"},
			Ungated,
		},
		{
			"release exists but gates on nothing",
			map[string]string{"release.yml": "jobs:\n  release:\n    runs-on: ubuntu-latest\n"},
			Ungated,
		},
		{
			// The generated workflow documents the gating shape in its own
			// comments, and it never gates itself.
			"only the generated workflow names itself",
			map[string]string{"validate.yml": gate},
			Ungated,
		},
		{
			"a non-workflow file names it",
			map[string]string{"notes.md": gate},
			Ungated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if tc.workflows != nil {
				if err := os.MkdirAll(workflowsDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				for name, body := range tc.workflows {
					if err := os.WriteFile(filepath.Join(workflowsDir, name), []byte(body), 0o644); err != nil {
						t.Fatalf("write %s: %v", name, err)
					}
				}
			}
			if got := ReleaseGatesOnValidate("."); got != tc.want {
				t.Errorf("ReleaseGatesOnValidate = %v, want %v", got, tc.want)
			}
		})
	}
}

// A root component is the common case and should not carry a redundant
// working-directory or a directory suffix in its job name.
func TestGenerateOmitsWorkingDirectoryAtRoot(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.Contains(workflow, "  go:") {
		t.Errorf("root component should produce a bare job name:\n%s", workflow)
	}
	// Match the defaults key, not the word — block comments mention it too.
	if strings.Contains(workflow, "        working-directory:") {
		t.Error("root component should not set a defaults working-directory")
	}
}

// The registry may declare a stack CI has no block for yet — docker is one:
// the image build is bespoke per repo and hadolint is a pre-commit concern, so
// there is no baseline job to run. Those must not produce empty jobs.
func TestGenerateSkipsStacksWithNoBlock(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t),
		comps("go", ".", "docker", "."), nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if strings.Contains(workflow, "  docker:") {
		t.Errorf("stack without a block produced a job:\n%s", workflow)
	}
}

// Which runner the job names decides whether setup-go's cache is worth anything.
//
// A hosted runner starts empty, so the cache is the whole point. A self-hosted one
// keeps GOMODCACHE between jobs, and restoring an archive over a module cache that
// already holds every file fails per file — 153k "Cannot open: File exists" lines in
// one observed step, which is then a log the GHA ingest webhook has to carry.
func TestGoCacheIsOnlyRestoredOnARunnerThatStartsEmpty(t *testing.T) {
	for _, testCase := range []struct {
		runner Runner
		want   string
	}{
		{Hosted, "cache: true"},
		{SelfHosted, "cache: false"},
	} {
		workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, Ungated, testCase.runner)
		if err != nil {
			t.Fatalf("Generate(%s): %v", testCase.runner, err)
		}
		if !strings.Contains(workflow, testCase.want) {
			t.Errorf("runner %s: missing %q:\n%s", testCase.runner, testCase.want, workflow)
		}
		if strings.Contains(workflow, "{{gocache}}") {
			t.Errorf("runner %s: unexpanded {{gocache}} placeholder left in output", testCase.runner)
		}
	}
}

func TestGenerateFailsWhenNoComponentHasABlock(t *testing.T) {
	if _, err := Generate(os.DirFS("blocks"), testManifest(t), comps("docker", "."), nil, Ungated, Hosted); err == nil {
		t.Fatal("expected an error rather than a workflow with zero jobs")
	}
}

// The manifest, not the block file, decides the action version.
func TestGenerateTakesActionVersionsFromManifest(t *testing.T) {
	manifest := &toolchain.Toolchain{
		Version: 42,
		Actions: []toolchain.Action{{Uses: "actions/checkout", Version: "v99"}},
	}

	workflow, err := Generate(os.DirFS("blocks"), manifest, comps("go", "."), nil, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.Contains(workflow, "actions/checkout@v99") {
		t.Errorf("manifest action version not applied:\n%s", workflow)
	}
	if !strings.HasPrefix(workflow, "# forge-toolchain: 42") {
		t.Errorf("stamp missing or wrong: %q", strings.SplitN(workflow, "\n", 2)[0])
	}
}

// A before:<stack> section runs before the stack's steps, not before checkout.
// ichrisbirch decrypts test secrets out of secrets/ in one, which needs the
// workspace to exist.
func TestCustomBeforeSectionFollowsCheckout(t *testing.T) {
	custom := map[string]string{
		"before:python": "      # > custom:before:python - Secrets\n" +
			"      - run: sops decrypt secrets/test.enc.env > .env",
	}

	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("python", "."), custom, Ungated, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	checkout := strings.Index(workflow, "actions/checkout")
	section := strings.Index(workflow, "custom:before:python")
	generated := strings.Index(workflow, "# generated:python")
	if checkout < 0 || section < 0 || generated < 0 {
		t.Fatalf("missing a landmark in:\n%s", workflow)
	}
	if checkout >= section || section >= generated {
		t.Error("before: section must sit between checkout and the generated block")
	}
}
