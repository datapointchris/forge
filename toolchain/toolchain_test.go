package toolchain

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func loadManifest(t *testing.T) *Toolchain {
	t.Helper()
	manifest, err := Load(os.DirFS("testdata"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return manifest
}

// A version written into a block is a copy of the pin that generation throws
// away, and the one a reader of the block believes. Every line a substitution
// rewrites must carry Pin instead.
func TestBlocksNameNoVersion(t *testing.T) {
	versioned := []*regexp.Regexp{revLineRE, usesLineRE, goInstallRE, runtimeLineRE, binaryLineRE, uvxLineRE}
	for _, dir := range []string{"../pre-commit/blocks", "../ci/blocks"} {
		err := fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(dir + "/" + path)
			if err != nil {
				return err
			}
			for n, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.Contains(line, Pin) {
					continue
				}
				for _, re := range versioned {
					if re.MatchString(line) {
						t.Errorf("%s/%s:%d names a version instead of %s: %s", dir, path, n+1, Pin, strings.TrimSpace(line))
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The fixture has to fill every pin the real blocks carry, or a generator test
// is refused before it asserts anything.
func TestFixturePinsEveryBlock(t *testing.T) {
	manifest := loadManifest(t)
	for _, dir := range []string{"../pre-commit/blocks", "../ci/blocks"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(dir + "/" + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if missing := Unpinned(manifest.ApplyAll(string(data))); len(missing) > 0 {
				t.Errorf("%s/%s: the fixture pins nothing for %v", dir, entry.Name(), missing)
			}
		}
	}
}

func TestUnpinnedNamesTheRepoARevBelongsTo(t *testing.T) {
	block := "  - repo: https://example.com/hook\n    rev: \"" + Pin + "\"\n    hooks:\n      - id: hook\n"
	got := Unpinned((&Toolchain{Version: 1}).ApplyRevs(block))
	if len(got) != 1 || got[0] != "https://example.com/hook" {
		t.Errorf("Unpinned = %v, want the repo URL", got)
	}
}

// pre-commit-shfmt tags v3.13.1-1 for shfmt 3.13.1, and CI downloads shfmt by
// its own release number. Keeping the counter asks for a release that does not
// exist.
func TestShfmtTakesTheReleaseItsHookWraps(t *testing.T) {
	manifest := &Toolchain{Version: 1, Hooks: []Hook{{Repo: hookPinnedTools["shfmt"], Rev: "v3.13.1-1"}}}

	got := manifest.ApplyBinaryVersions("          shfmt_version=\"" + Pin + "\"\n")

	if !strings.Contains(got, `shfmt_version="3.13.1"`) {
		t.Errorf("shfmt not derived from its hook: %q", got)
	}
}

func TestLoadRefusesABinariesEntryForAHookPinnedTool(t *testing.T) {
	fixture := fstest.MapFS{File: {Data: []byte("version: 1\nbinaries:\n  - name: shellcheck\n    version: \"0.10.0\"\n")}}
	if _, err := Load(fixture); err == nil {
		t.Error("a second copy of shellcheck's version loaded without complaint")
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

func TestApplyBinaryVersionsOverridesBlockPin(t *testing.T) {
	manifest := &Toolchain{
		Version:  9,
		Binaries: []Binary{{Name: "bats", Version: "9.9.9"}},
	}

	got := manifest.ApplyBinaryVersions("          bats_version=\"0.0.1\"\n")

	if !strings.Contains(got, `bats_version="9.9.9"`) {
		t.Errorf("manifest binary version not applied: %q", got)
	}
	if strings.Contains(got, "0.0.1") {
		t.Errorf("block version survived the override: %q", got)
	}
}

// A tool whose name holds an underscore must still be pinned. When the name
// pattern excluded underscores this matched nothing and failed silently, so the
// block kept whatever version it happened to name.
func TestApplyBinaryVersionsHandlesUnderscoredName(t *testing.T) {
	manifest := &Toolchain{
		Version:  9,
		Binaries: []Binary{{Name: "bats_support", Version: "0.3.0"}},
	}

	got := manifest.ApplyBinaryVersions("          bats_support_version=\"0.0.1\"\n")

	if !strings.Contains(got, `bats_support_version="0.3.0"`) {
		t.Errorf("underscored binary name not pinned: %q", got)
	}
}

// A block may pin a version the manifest does not own; only managed names are
// rewritten, so an unrelated assignment of the same shape is left intact.
func TestApplyBinaryVersionsLeavesUnmanagedNameAlone(t *testing.T) {
	manifest := &Toolchain{Version: 9, Binaries: []Binary{{Name: "bats", Version: "9.9.9"}}}

	got := manifest.ApplyBinaryVersions("          hadolint_version=\"1.2.3\"\n")

	if !strings.Contains(got, `hadolint_version="1.2.3"`) {
		t.Errorf("unmanaged binary version was rewritten: %q", got)
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

// A repo that treats ruff as a fleet tool rather than a project dependency does
// not declare it, and `uv run ruff` then failed to spawn instead of linting.
// CI uses uvx, pinned to the same rev as the pre-commit hook so the two cannot
// report different findings.
func TestApplyUvxVersionsTracksTheHookRev(t *testing.T) {
	manifest := &Toolchain{Version: 9}
	manifest.Hooks = append(manifest.Hooks, Hook{Repo: "https://github.com/astral-sh/ruff-pre-commit", Rev: "v9.9.9"})

	got := manifest.ApplyUvxVersions("      - run: uvx ruff@0.0.1 check .\n")
	if want := "      - run: uvx ruff@9.9.9 check .\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	unmapped := "      - run: uvx somethingelse@1.2.3 --help\n"
	if got := manifest.ApplyUvxVersions(unmapped); got != unmapped {
		t.Errorf("unmapped tool rewritten: %q", got)
	}
}

func TestAnActionPinnedToACommitKeepsItsCommit(t *testing.T) {
	manifest := &Toolchain{Version: 1, Actions: []Action{{Uses: "actions/checkout", Version: "v9"}}}
	line := "      - uses: actions/checkout@0123456789abcdef0123456789abcdef01234567 # v4.1.1\n"

	if got := manifest.ApplyActionVersions(line); got != line {
		t.Errorf("a commit pin was loosened to a tag: %q", got)
	}
}

// A hand-written release job builds against the Go it names, often a matrix of
// several, and one declared version would collapse it.
func TestAWorkflowForgeDidNotWriteKeepsItsRuntimes(t *testing.T) {
	manifest := &Toolchain{
		Version:  1,
		Actions:  []Action{{Uses: "actions/setup-go", Version: "v9"}},
		Runtimes: []Runtime{{Name: "go", Version: "1.99"}},
	}

	got := manifest.ApplyWorkflowPins("      - uses: actions/setup-go@v1\n        with:\n          go-version: \"1.21\"\n")

	if !strings.Contains(got, "actions/setup-go@v9") {
		t.Errorf("the action pin was not applied: %q", got)
	}
	if !strings.Contains(got, `go-version: "1.21"`) {
		t.Errorf("the runtime was rewritten: %q", got)
	}
}
