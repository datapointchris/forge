package toolchain

import (
	"os"
	"strings"
	"testing"
)

func loadManifest(t *testing.T) *Toolchain {
	t.Helper()
	manifest, err := Load(os.DirFS("../pre-commit"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return manifest
}

// A block that names a remote repo the manifest does not pin would ship a
// version nothing tracks — the exact drift the manifest exists to prevent.
func TestToolchainManagesEveryBlockRepo(t *testing.T) {
	manifest := loadManifest(t)
	blocks := os.DirFS("../pre-commit/blocks")

	unmanaged, err := manifest.UnmanagedRepos(blocks)
	if err != nil {
		t.Fatalf("UnmanagedRepos: %v", err)
	}
	if len(unmanaged) > 0 {
		t.Errorf("blocks use repos absent from toolchain.yml: %v", unmanaged)
	}
}

// The manifest overrides whatever rev a block declares — that override is the
// whole point, so verify it actually rewrites rather than passing through.
func TestApplyRevsOverridesBlockRev(t *testing.T) {
	manifest := &Toolchain{Version: 9}
	manifest.Hooks = append(manifest.Hooks, Hook{Repo: "https://github.com/rhysd/actionlint", Rev: "v9.9.9"})

	block := "  - repo: https://github.com/rhysd/actionlint\n    rev: v1.0.0\n    hooks:\n      - id: actionlint\n"
	got := manifest.ApplyRevs(block)

	if !strings.Contains(got, "rev: v9.9.9") {
		t.Errorf("manifest rev not applied: %q", got)
	}
	if strings.Contains(got, "rev: v1.0.0") {
		t.Errorf("block rev survived the override: %q", got)
	}
}

// A CI block naming an action the manifest does not pin would ship a version
// nothing tracks — same drift the hook manifest exists to prevent.
func TestToolchainManagesEveryCIAction(t *testing.T) {
	manifest := loadManifest(t)

	unmanaged, err := manifest.UnmanagedActions(os.DirFS("../ci/blocks"))
	if err != nil {
		t.Fatalf("UnmanagedActions: %v", err)
	}
	if len(unmanaged) > 0 {
		t.Errorf("CI blocks use actions absent from toolchain.yml: %v", unmanaged)
	}
}

func TestApplyToolVersionsOverridesGoInstall(t *testing.T) {
	manifest := &Toolchain{
		Version: 9,
		Tools:   []Tool{{Module: "example.com/lint/cmd/lint", Version: "v9.9.9"}},
	}

	got := manifest.ApplyToolVersions("          go install example.com/lint/cmd/lint@v1.0.0\n")

	if !strings.Contains(got, "@v9.9.9") {
		t.Errorf("manifest tool version not applied: %q", got)
	}
	if strings.Contains(got, "@v1.0.0") {
		t.Errorf("block version survived the override: %q", got)
	}
}

// A repo's own version file is the repo's business; only the literal
// `<name>-version:` input is the manifest's to pin.
func TestApplyRuntimeVersionsLeavesVersionFileAlone(t *testing.T) {
	manifest := &Toolchain{Version: 9, Runtimes: []Runtime{{Name: "node", Version: "24"}}}

	got := manifest.ApplyRuntimeVersions("          node-version: \"18\"\n          go-version-file: api/go.mod\n")

	if !strings.Contains(got, `node-version: "24"`) {
		t.Errorf("runtime version not applied: %q", got)
	}
	if !strings.Contains(got, "go-version-file: api/go.mod") {
		t.Errorf("version-file input was rewritten: %q", got)
	}
}
