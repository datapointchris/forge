package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/forge/v7/config"
	"github.com/datapointchris/forge/v7/toolchain"
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
		comps("go", "api", "go", "cli", "vue", "web"), nil, false)
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
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("python", ""), nil, false)
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
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("python", ""), nil, true)
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
	cases := []struct {
		name    string
		release string
		want    bool
	}{
		{"no release workflow at all", "", false},
		{
			"release gates on the generated workflow",
			"jobs:\n  validate:\n    uses: ./.github/workflows/validate.yml\n",
			true,
		},
		{
			// The shape every repo had before this: the release gated on a
			// hand-written ci.yml, so validate.yml still needs its own push.
			"release gates on a hand-written ci.yml",
			"jobs:\n  validate:\n    uses: ./.github/workflows/ci.yml\n",
			false,
		},
		{
			"release exists but gates on nothing",
			"jobs:\n  release:\n    runs-on: ubuntu-latest\n",
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if tc.release != "" {
				if err := os.MkdirAll(filepath.Dir(releaseWorkflowPath), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(releaseWorkflowPath, []byte(tc.release), 0o644); err != nil {
					t.Fatalf("write: %v", err)
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
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, false)
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
		comps("go", ".", "docker", "."), nil, false)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if strings.Contains(workflow, "  docker:") {
		t.Errorf("stack without a block produced a job:\n%s", workflow)
	}
}

func TestGenerateFailsWhenNoComponentHasABlock(t *testing.T) {
	if _, err := Generate(os.DirFS("blocks"), testManifest(t), comps("docker", "."), nil, false); err == nil {
		t.Fatal("expected an error rather than a workflow with zero jobs")
	}
}

// The manifest, not the block file, decides the action version.
func TestGenerateTakesActionVersionsFromManifest(t *testing.T) {
	manifest := &toolchain.Toolchain{
		Version: 42,
		Actions: []toolchain.Action{{Uses: "actions/checkout", Version: "v99"}},
	}

	workflow, err := Generate(os.DirFS("blocks"), manifest, comps("go", "."), nil, false)
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

// Several repos hand-wrote a workflow before generation existed. Overwriting
// one would destroy work with no way back.
func TestRunAbortsOnHandWrittenWorkflow(t *testing.T) {
	blocksDir := filepath.Join(originalDir(t), "blocks")
	manifest := testManifest(t)

	t.Chdir(t.TempDir())

	if err := os.MkdirAll(filepath.Dir(WorkflowPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	handWritten := "name: CI\njobs: {}\n"
	if err := os.WriteFile(WorkflowPath, []byte(handWritten), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Run(os.DirFS(blocksDir), manifest, comps("go", ".")); err == nil {
		t.Fatal("expected abort on a workflow with no forge-toolchain header")
	}

	data, err := os.ReadFile(WorkflowPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != handWritten {
		t.Error("aborted run must leave the existing workflow untouched")
	}
}

func TestRunIsIdempotent(t *testing.T) {
	blocksDir := filepath.Join(originalDir(t), "blocks")
	manifest := testManifest(t)

	t.Chdir(t.TempDir())

	first, err := Run(os.DirFS(blocksDir), manifest, comps("go", "."))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first != "generated" {
		t.Errorf("first run = %q, want generated", first)
	}

	second, err := Run(os.DirFS(blocksDir), manifest, comps("go", "."))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second != "no changes" {
		t.Errorf("second run = %q, want no changes — generation is not stable", second)
	}
}

func originalDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return dir
}

// A before:<stack> section runs before the stack's steps, not before checkout.
// ichrisbirch decrypts test secrets out of secrets/ in one, which needs the
// workspace to exist.
func TestCustomBeforeSectionFollowsCheckout(t *testing.T) {
	custom := map[string]string{
		"before:python": "      # > custom:before:python - Secrets\n" +
			"      - run: sops decrypt secrets/test.enc.env > .env",
	}

	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("python", "."), custom, false)
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
