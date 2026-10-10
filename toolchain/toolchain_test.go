package toolchain

import (
	"io/fs"
	"os"
	"path/filepath"
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
// rewrites must carry Pin instead. A release number in any other shape, such
// as an action's version input, is a copy no substitution updates at all.
func TestBlocksNameNoVersion(t *testing.T) {
	literal := regexp.MustCompile(`\bv?\d+\.\d+\.\d+\b`)
	versioned := []*regexp.Regexp{revLineRE, usesLineRE, goInstallRE, runtimeLineRE, binaryLineRE, uvxLineRE, dependencyLineRE, literal}
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

func TestLoadRefusesAThirdPartyActionItCannotPinToACommit(t *testing.T) {
	commit := strings.Repeat("c", 40)
	for name, entry := range map[string]string{
		"a tag alone":        "  - uses: astral-sh/setup-uv\n    version: v7.1.2\n",
		"a moving major":     "  - uses: astral-sh/setup-uv\n    version: v7\n    sha: " + commit + "\n",
		"an abbreviated sha": "  - uses: astral-sh/setup-uv\n    version: v7.1.2\n    sha: c0ffee1\n",
		"an uppercase sha":   "  - uses: astral-sh/setup-uv\n    version: v7.1.2\n    sha: " + strings.ToUpper(commit) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := fstest.MapFS{File: {Data: []byte("version: 1\nhosted_runner: ubuntu-99.04\nactions:\n" + entry)}}
			if _, err := Load(fixture); err == nil {
				t.Error("loaded without complaint")
			}
		})
	}
}

func TestLoadAcceptsAFirstPartyTagAndAThirdPartyCommit(t *testing.T) {
	fixture := fstest.MapFS{File: {Data: []byte("version: 1\nhosted_runner: ubuntu-99.04\nactions:\n" +
		"  - uses: actions/checkout\n    version: v7\n" +
		"  - uses: astral-sh/setup-uv\n    version: v7.1.2\n    sha: " + strings.Repeat("c", 40) + "\n")}}
	if _, err := Load(fixture); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestApplyDependencyVersionsPinsADeclaredModuleOnly(t *testing.T) {
	manifest := &Toolchain{Version: 1, Tools: []Tool{{Module: "mvdan.cc/gofumpt", Version: "v0.12.0"}}}
	block := "        additional_dependencies:\n" +
		"          - mvdan.cc/gofumpt@" + Pin + "\n" +
		"          - example.com/undeclared@v1.0.0\n"

	got := manifest.ApplyDependencyVersions(block)

	if !strings.Contains(got, "- mvdan.cc/gofumpt@v0.12.0\n") {
		t.Errorf("the declared module did not take its pin: %q", got)
	}
	if !strings.Contains(got, "- example.com/undeclared@v1.0.0\n") {
		t.Errorf("an undeclared module was rewritten: %q", got)
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

// An older commit has to move to the declared one, or a hand-written workflow
// pinned once by hand never takes a bump.
func TestADeclaredCommitReplacesWhateverRefTheLineHeld(t *testing.T) {
	commit := strings.Repeat("c", 40)
	manifest := &Toolchain{Version: 1, Actions: []Action{{Uses: "astral-sh/setup-uv", Version: "v7.1.2", Sha: commit}}}
	want := "      - uses: astral-sh/setup-uv@" + commit + " # v7.1.2"

	for name, line := range map[string]string{
		"a tag":                  "      - uses: astral-sh/setup-uv@v6",
		"the block's pin":        "      - uses: astral-sh/setup-uv@" + Pin,
		"an older commit":        "      - uses: astral-sh/setup-uv@" + strings.Repeat("0", 40) + " # v6.0.0",
		"a commit with no label": "      - uses: astral-sh/setup-uv@" + strings.Repeat("0", 40),
	} {
		if got := manifest.ApplyActionVersions(line); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}

	note := "      - uses: astral-sh/setup-uv@v6.0.0 # v6.0.0 until the cache bug is fixed"
	if got := manifest.ApplyActionVersions(note); got != want+" until the cache bug is fixed" {
		t.Errorf("the note after the release was lost: %q", got)
	}
}

func TestOnlyTheDownloadingActionsOwnInputIsRewritten(t *testing.T) {
	manifest := &Toolchain{Version: 1, Hooks: []Hook{{Repo: hookPinnedTools["uv"], Rev: "0.9.1"}}}
	workflow := strings.Join([]string{
		"      - uses: astral-sh/setup-uv@v7",
		"        with:",
		"          version: 0.4.0",
		"      - uses: goreleaser/goreleaser-action@v6",
		"        with:",
		"          version: v2.1.0",
		"      - name: no input",
		"        uses: astral-sh/setup-uv@v7",
		"      - run: echo version: 1",
		"      - uses: astral-sh/setup-uv@v7",
		"        with:",
		"          version: 0.9.1",
	}, "\n")

	got := strings.Split(manifest.ApplyActionReleaseInputs(workflow), "\n")

	if got[2] != `          version: "0.9.1"` {
		t.Errorf("setup-uv's input = %q, want the uv its hook pins", got[2])
	}
	if got[5] != "          version: v2.1.0" {
		t.Errorf("another action's input was rewritten: %q", got[5])
	}
	if len(got) != 12 {
		t.Errorf("an input was added to a step that had none:\n%s", strings.Join(got, "\n"))
	}
	if got[11] != "          version: 0.9.1" {
		t.Errorf("an input already naming the release was rewritten: %q", got[11])
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

// GitHub moves ubuntu-latest to a new release on its own schedule, so a
// workflow forge did not write is pinned the same as one it did. A matrix and
// the condition reading it move together, or the condition stops matching.
func TestAWorkflowForgeDidNotWriteRunsOnTheDeclaredImage(t *testing.T) {
	manifest := &Toolchain{Version: 1, HostedRunner: "ubuntu-99.04"}
	workflow := "jobs:\n" +
		"  build:\n" +
		"    runs-on: ubuntu-latest\n" +
		"  old:\n" +
		"    runs-on: ubuntu-22.04\n" +
		"  cross:\n" +
		"    strategy:\n" +
		"      matrix:\n" +
		"        os: [ubuntu-latest, macos-latest, windows-latest]\n" +
		"    if: matrix.os == 'ubuntu-latest'\n"

	got := manifest.ApplyWorkflowPins(workflow)

	want := strings.NewReplacer("ubuntu-latest", "ubuntu-99.04", "ubuntu-22.04", "ubuntu-99.04").Replace(workflow)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Each of these names a machine the pin does not describe, or is prose about a
// runner rather than a choice of one.
func TestARunnerLabelThePinDoesNotDescribeIsLeftAlone(t *testing.T) {
	manifest := &Toolchain{Version: 1, HostedRunner: "ubuntu-99.04"}
	for _, line := range []string{
		"    runs-on: ubuntu-24.04-arm\n",
		"    runs-on: ubuntu-latest-4-cores\n",
		"    runs-on: ubuntu-slim\n",
		"    runs-on: [self-hosted, private-ci]\n",
		"    runs-on: macos-latest\n",
		"    # ubuntu-latest moves to a new release on GitHub's schedule\n",
		"    container: my-ubuntu-latest\n",
	} {
		if got := manifest.ApplyRunnerLabels(line); got != line {
			t.Errorf("rewritten: %q became %q", line, got)
		}
	}
}

// writeDeclaration writes a declaration holding a stamp and the sections the
// fragment adds, each written with a leading comma.
func writeDeclaration(t *testing.T, fragment string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pinned-versions.json")
	if err := os.WriteFile(path, []byte(`{"stamp": {"version": 1}`+fragment+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const hostedImage = `, "runners": {"hosted": "ubuntu-26.04"}`

// Every command loads the declaration through LoadFile, so a second copy of a
// version CI already takes from a hook pin is refused there.
func TestLoadFileRefusesABinariesEntryForAHookPinnedTool(t *testing.T) {
	_, err := LoadFile(writeDeclaration(t, hostedImage+`, "binaries": {"pins": [{"name": "ruff", "version": "0.12.5"}]}`))
	if err == nil || !strings.Contains(err.Error(), "ruff") {
		t.Errorf("LoadFile = %v, want a refusal naming ruff", err)
	}
}

func TestLoadFileReadsTheHostedImage(t *testing.T) {
	manifest, err := LoadFile(writeDeclaration(t, `, "runners": {"reason": "why", "hosted": "ubuntu-26.04"}`))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if manifest.HostedRunner != "ubuntu-26.04" {
		t.Errorf("HostedRunner = %q, want ubuntu-26.04", manifest.HostedRunner)
	}
}

// No image would write a bare runs-on into every workflow, and a floating
// label is the thing the pin replaces.
func TestLoadFileRefusesAHostedImageThatIsNotOneRelease(t *testing.T) {
	for name, runners := range map[string]string{
		"absent":    "",
		"empty":     `, "runners": {"hosted": ""}`,
		"floating":  `, "runners": {"hosted": "ubuntu-latest"}`,
		"a variant": `, "runners": {"hosted": "ubuntu-24.04-arm"}`,
	} {
		if _, err := LoadFile(writeDeclaration(t, runners)); err == nil || !strings.Contains(err.Error(), "runners.hosted") {
			t.Errorf("%s: LoadFile = %v, want a refusal naming runners.hosted", name, err)
		}
	}
}
