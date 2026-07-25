package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func detected(stacks ...string) map[string]bool {
	d := make(map[string]bool)
	for _, s := range stacks {
		d[s] = true
	}
	return d
}

func TestGenerateIncludesOnlyDetectedStacks(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), detected("go"), nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.Contains(workflow, "go-version-file: go.mod") {
		t.Error("go block missing for a go repo")
	}
	if strings.Contains(workflow, "uv run pytest") {
		t.Error("python block included for a go-only repo")
	}
	if strings.Contains(workflow, "npm ci") {
		t.Error("vue block included for a go-only repo")
	}
	// Uncategorized blocks apply everywhere.
	if !strings.Contains(workflow, "actions/checkout@") {
		t.Error("checkout block missing")
	}
}

// The manifest, not the block file, decides the action version — otherwise the
// CI blocks become a second place versions drift.
func TestGenerateTakesActionVersionsFromManifest(t *testing.T) {
	manifest := &toolchain.Toolchain{
		Version: 42,
		Actions: []toolchain.Action{{Uses: "actions/checkout", Version: "v99"}},
	}

	workflow, err := Generate(os.DirFS("blocks"), manifest, detected(), nil)
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
// one would destroy work with no way back, so an unstamped file must abort.
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

	if _, err := Run(os.DirFS(blocksDir), manifest, detected("go")); err == nil {
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

	first, err := Run(os.DirFS(blocksDir), manifest, detected("go"))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first != "generated" {
		t.Errorf("first run = %q, want generated", first)
	}

	second, err := Run(os.DirFS(blocksDir), manifest, detected("go"))
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
