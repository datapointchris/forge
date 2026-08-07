package precommit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/datapointchris/forge/v2/config"
	"github.com/datapointchris/forge/v2/toolchain"
)

func makeTestBlocks() fstest.MapFS {
	return fstest.MapFS{
		"00-conventional-commits.yml": &fstest.MapFile{
			Data: []byte("  # Conventional commits\n" +
				"  - repo: https://example.com/commits\n" +
				"    hooks:\n" +
				"      - id: conventional-pre-commit\n"),
		},
		"05-file-checks.yml": &fstest.MapFile{
			Data: []byte("  # File format checks\n" +
				"  - repo: https://example.com/hooks\n" +
				"    hooks:\n" +
				"      - id: check-yaml\n" +
				"      - id: check-toml\n"),
		},
		"30-python-format.yml": &fstest.MapFile{
			Data: []byte("  # Python: formatting\n" +
				"  - repo: https://example.com/ruff\n" +
				"    hooks:\n" +
				"      - id: ruff-format\n"),
		},
		"40-go.yml": &fstest.MapFile{
			Data: []byte("  # Go\n" +
				"  - repo: https://example.com/go\n" +
				"    hooks:\n" +
				"      - id: go-vet\n"),
		},
	}
}

func detected(techs ...string) *config.Toolchain {
	declared := &config.Toolchain{}
	for _, tech := range techs {
		declared.Components = append(declared.Components, config.Component{Stack: tech, Dir: "."})
	}
	return declared
}

// at builds a toolchain from stack/dir pairs.
func at(pairs ...string) *config.Toolchain {
	declared := &config.Toolchain{}
	for i := 0; i < len(pairs); i += 2 {
		declared.Components = append(declared.Components, config.Component{Stack: pairs[i], Dir: pairs[i+1]})
	}
	return declared
}

func getHookIDs(config string) []string {
	re := regexp.MustCompile(`(?m)^\s+-\s*id:\s*(\S+)`)
	matches := re.FindAllStringSubmatch(config, -1)
	var ids []string
	for _, m := range matches {
		ids = append(ids, m[1])
	}
	return ids
}

func getGeneratedBlocks(config string) []string {
	re := regexp.MustCompile(`(?m)^# generated:(\S+)`)
	matches := re.FindAllStringSubmatch(config, -1)
	var names []string
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func indexOf(slice []string, item string) int {
	for i, s := range slice {
		if s == item {
			return i
		}
	}
	return -1
}

func TestBlockName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"05-file-checks.yml", "file-checks"},
		{"30-python-format.yml", "python-format"},
		{"00-conventional-commits.yml", "conventional-commits"},
	}
	for _, tc := range tests {
		got := BlockName(tc.input)
		if got != tc.want {
			t.Errorf("BlockName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestShouldIncludeBlock(t *testing.T) {
	dirs := dirsByCategory(detected("python", "actions").Components)

	mustInclude := func(name string, want bool) {
		t.Helper()
		got, err := ShouldIncludeBlock(name, dirs)
		if err != nil {
			t.Fatalf("ShouldIncludeBlock(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("ShouldIncludeBlock(%q) = %v, want %v", name, got, want)
		}
	}

	mustInclude("conventional-commits", true)
	mustInclude("file-checks", true)
	mustInclude("python-format", true)
	mustInclude("go", false)
	mustInclude("vue", false)
}

// A block in neither table used to read as generic, which silently shipped
// cargo and stylua hooks to every Go and Python repo.
func TestShouldIncludeBlockRejectsUnclassified(t *testing.T) {
	if _, err := ShouldIncludeBlock("brand-new-stack", nil); err == nil {
		t.Error("an unclassified block should be an error, not a default")
	}
}

// Every real block must be classified, so adding one cannot repeat the leak.
func TestEveryRealBlockIsClassified(t *testing.T) {
	entries, err := os.ReadDir("../pre-commit/blocks")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := BlockName(entry.Name())
		if _, err := ShouldIncludeBlock(name, nil); err != nil {
			t.Errorf("block %s: %v", name, err)
		}
	}
}

func TestGenerateSimpleConfig(t *testing.T) {
	blocks := makeTestBlocks()
	config, err := Generate(blocks, testToolchain(t), detected("python"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(config, "# generated:conventional-commits") {
		t.Error("missing conventional-commits block")
	}
	if !strings.Contains(config, "# generated:file-checks") {
		t.Error("missing file-checks block")
	}
	if !strings.Contains(config, "# generated:python-format") {
		t.Error("missing python-format block")
	}
	if strings.Contains(config, "go-vet") {
		t.Error("go-vet should not be present")
	}
	if strings.Contains(config, "custom") {
		t.Error("no custom sections expected")
	}
}

func TestCustomSectionsPreserved(t *testing.T) {
	blocks := makeTestBlocks()
	custom := map[string]string{
		"before:file-checks": "# > custom:before:file-checks - Stats capture\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: devstats-capture",
		"after:python-format": "# > custom:after:python-format - Custom linter\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: my-linter",
		"after:all": "# > custom:after:all - Tests\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: pytest-results",
	}

	config, err := Generate(blocks, testToolchain(t), detected("python"), custom)
	if err != nil {
		t.Fatal(err)
	}

	hooks := getHookIDs(config)
	captureIdx := indexOf(hooks, "devstats-capture")
	fileChecksIdx := indexOf(hooks, "check-yaml")
	linterIdx := indexOf(hooks, "my-linter")
	pytestIdx := indexOf(hooks, "pytest-results")

	if captureIdx < 0 {
		t.Fatal("devstats-capture not found")
	}
	if captureIdx >= fileChecksIdx {
		t.Error("custom:before:file-checks should come before file-checks")
	}
	if linterIdx < indexOf(hooks, "ruff-format") {
		t.Error("custom:after:python-format should come after python-format")
	}
	if pytestIdx != len(hooks)-1 {
		t.Error("custom:after:all should be last")
	}
}

func TestExtractCustomSections(t *testing.T) {
	configText := "repos:\n" +
		"# > custom:before:file-checks - Stats capture\n" +
		"  - repo: local\n" +
		"    hooks:\n" +
		"      - id: devstats-capture\n" +
		"\n" +
		"# generated:file-checks - File checks\n" +
		"  - repo: https://example.com\n" +
		"\n" +
		"# > custom:after:all - Tests\n" +
		"  - repo: local\n" +
		"    hooks:\n" +
		"      - id: pytest-results\n"

	sections := ExtractCustomSections(configText)

	if _, ok := sections["before:file-checks"]; !ok {
		t.Error("missing before:file-checks section")
	}
	if _, ok := sections["after:all"]; !ok {
		t.Error("missing after:all section")
	}
	if !strings.Contains(sections["before:file-checks"], "devstats-capture") {
		t.Error("before:file-checks should contain devstats-capture")
	}
	if !strings.Contains(sections["after:all"], "pytest-results") {
		t.Error("after:all should contain pytest-results")
	}
}

// A workflow indents every marker inside a job, so extraction that anchored at
// ^ found nothing there: before:/after: sections were silently dropped on the
// next sync, and an unterminated after:<block> absorbed the jobs after it.
func TestExtractCustomSectionsIndented(t *testing.T) {
	workflow := "jobs:\n" +
		"  rust:\n" +
		"    steps:\n" +
		"      # > custom:before:rust - System headers\n" +
		"      - run: apt-get install -y libwebkit2gtk-4.1-dev\n" +
		"\n" +
		"      # generated:rust\n" +
		"      - run: cargo test\n"

	sections := ExtractCustomSections(workflow)

	if _, ok := sections["before:rust"]; !ok {
		t.Fatal("missing before:rust section")
	}
	if !strings.Contains(sections["before:rust"], "libwebkit2gtk") {
		t.Error("before:rust should contain the apt-get step")
	}
	if strings.Contains(sections["before:rust"], "cargo test") {
		t.Error("indented # generated: must terminate the section")
	}
}

func TestRoundtripPreservesCustom(t *testing.T) {
	blocks := makeTestBlocks()
	custom := map[string]string{
		"before:file-checks": "# > custom:before:file-checks - Capture\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: devstats-capture",
	}

	config1, err := Generate(blocks, testToolchain(t), detected("python"), custom)
	if err != nil {
		t.Fatal(err)
	}

	extracted := ExtractCustomSections(config1)
	config2, err := Generate(blocks, testToolchain(t), detected("python"), extracted)
	if err != nil {
		t.Fatal(err)
	}

	if config1 != config2 {
		t.Error("roundtrip should produce identical output")
	}
}

// A comment inside a hook is not the end of it — stripping used to stop there
// and leave the hook's remaining keys behind as invalid YAML.
func TestStripHooksKeepsCommentedHookWhole(t *testing.T) {
	content := strings.Join([]string{
		"  - repo: local",
		"    hooks:",
		"      - id: doomed",
		"        name: doomed",
		"        # why this entry is written the odd way",
		"        entry: echo hi",
		"        language: system",
		"      - id: survivor",
		"        entry: echo bye",
	}, "\n")

	got := StripHooksFromBlock(content, map[string]bool{"doomed": true})

	for _, gone := range []string{"doomed", "echo hi", "why this entry"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q should have been stripped:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "id: survivor") || !strings.Contains(got, "echo bye") {
		t.Errorf("survivor hook damaged:\n%s", got)
	}
}

// A marker that is not the last thing in the file absorbs everything below it,
// hooks included, leaving the standard block with a bare `hooks:` key that
// pre-commit rejects.
func TestGenerateDropsRepoEntriesLeftWithoutHooks(t *testing.T) {
	blocks := makeTestBlocks()
	custom := map[string]string{
		"after:python-format": strings.Join([]string{
			"# > custom:after:python-format - Swallowed the block below it",
			"  - repo: local",
			"    hooks:",
			"      - id: ruff-format",
			"      - id: check-yaml",
			"      - id: check-toml",
		}, "\n"),
	}

	config, err := Generate(blocks, testToolchain(t), detected("python"), custom)
	if err != nil {
		t.Fatal(err)
	}

	if regexp.MustCompile(`hooks:\n(\s*\n)*(#|\s*- repo:|$)`).MatchString(config) {
		t.Errorf("a repo entry was left with no hooks:\n%s", config)
	}
}

func TestSafetyCheckBlocksUnknownHooks(t *testing.T) {
	blocks := makeTestBlocks()
	configText := "repos:\n" +
		"  - repo: local\n" +
		"    hooks:\n" +
		"      - id: check-yaml\n" +
		"      - id: devstats-capture\n" +
		"      - id: my-secret-hook\n"

	unknown, err := SafetyCheck(configText, blocks, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	unknownSet := make(map[string]bool)
	for _, id := range unknown {
		unknownSet[id] = true
	}

	if !unknownSet["devstats-capture"] {
		t.Error("devstats-capture should be unknown")
	}
	if !unknownSet["my-secret-hook"] {
		t.Error("my-secret-hook should be unknown")
	}
	if unknownSet["check-yaml"] {
		t.Error("check-yaml should be recognized")
	}
}

func TestSafetyCheckAllowsMarkedHooks(t *testing.T) {
	blocks := makeTestBlocks()
	configText := "repos:\n" +
		"  - repo: local\n" +
		"    hooks:\n" +
		"      - id: unknown-hook\n"

	customSections := map[string]string{
		"after:all": "# > custom:after:all - Stuff\n  - repo: local\n    hooks:\n      - id: unknown-hook",
	}

	unknown, err := SafetyCheck(configText, blocks, nil, customSections)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) > 0 {
		t.Errorf("a marked hook should be recognized, got %v", unknown)
	}
}

func TestSafetyCheckStillCatchesUnmarkedSiblings(t *testing.T) {
	blocks := makeTestBlocks()
	configText := "repos:\n" +
		"  - repo: local\n" +
		"    hooks:\n" +
		"      - id: marked-hook\n" +
		"      - id: forgotten-hook\n"

	customSections := map[string]string{
		"after:all": "# > custom:after:all - Stuff\n  - repo: local\n    hooks:\n      - id: marked-hook",
	}

	unknown, err := SafetyCheck(configText, blocks, nil, customSections)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 || unknown[0] != "forgotten-hook" {
		t.Errorf("want [forgotten-hook], got %v", unknown)
	}
}

func TestCustomHooksNotDuplicatedInStandard(t *testing.T) {
	blocks := makeTestBlocks()
	custom := map[string]string{
		"after:python-format": "# > custom:after:python-format - Custom formatter\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: ruff-format\n" +
			"        name: custom ruff-format\n" +
			"        entry: custom-ruff",
	}

	config, err := Generate(blocks, testToolchain(t), detected("python"), custom)
	if err != nil {
		t.Fatal(err)
	}

	count := strings.Count(config, "id: ruff-format")
	if count != 1 {
		t.Errorf("expected 1 ruff-format, got %d", count)
	}
	if !strings.Contains(config, "custom-ruff") {
		t.Error("custom ruff entry should be present")
	}
}

// Integration tests using the real blocks from the forge repo.

func forgeBlocksDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "pre-commit", "blocks")
}

func realBlocks(t *testing.T) fs.FS {
	t.Helper()
	dir := forgeBlocksDir(t)
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("blocks directory not found: %s", dir)
	}
	return os.DirFS(dir)
}

func TestIntegration_PythonRepo(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), detected("python"), nil)
	if err != nil {
		t.Fatal(err)
	}

	genBlocks := getGeneratedBlocks(config)
	if !contains(genBlocks, "python-format") {
		t.Error("missing python-format block")
	}
	if !contains(genBlocks, "python-lint") {
		t.Error("missing python-lint block")
	}
	if contains(genBlocks, "go") {
		t.Error("go block should not be present")
	}

	hooks := getHookIDs(config)
	for _, id := range []string{"ruff-format", "ruff-check", "mypy", "uv-lock"} {
		if !contains(hooks, id) {
			t.Errorf("missing hook: %s", id)
		}
	}
}

func TestIntegration_GoRepo(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), detected("go"), nil)
	if err != nil {
		t.Fatal(err)
	}

	genBlocks := getGeneratedBlocks(config)
	if !contains(genBlocks, "go") {
		t.Error("missing go block")
	}
	if contains(genBlocks, "python-format") {
		t.Error("python block should not be present")
	}

	hooks := getHookIDs(config)
	if !contains(hooks, "go-fumpt-repo") {
		t.Error("missing go-fumpt-repo hook")
	}
}

// The block names no directory; the declaration does. A repo with its frontend
// in web/ must get hooks that enter web/, not the block author's frontend/.
func TestIntegration_VueHooksEnterTheDeclaredDirectory(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), at("vue", "web"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(config, "cd web &&") {
		t.Error("vue hooks should cd into the declared directory")
	}
	if strings.Contains(config, "cd frontend &&") {
		t.Error("vue hooks should not reference the block's example directory")
	}
	if !strings.Contains(config, `'^web/.*\.(vue|js|ts|jsx|tsx)$'`) {
		t.Errorf("files: pattern should be anchored to the declared directory:\n%s", config)
	}
	if strings.Contains(config, "{{") {
		t.Error("unexpanded placeholder in generated config")
	}
}

// A root component has no path prefix to anchor with: "^\./" matches nothing
// pre-commit ever passes.
func TestIntegration_RootComponentDropsThePathAnchor(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), detected("vue"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(config, `'^./`) {
		t.Errorf("root component should not anchor on ./:\n%s", config)
	}
	if !strings.Contains(config, `'^.*\.(vue|js|ts|jsx|tsx)$'`) {
		t.Errorf("root component should match unanchored:\n%s", config)
	}
}

// Two frontends in one repo need distinguishable hooks — pre-commit reports
// failures by hook name, and two called vue-eslint name nothing.
func TestIntegration_MultipleComponentsGetSuffixedHooks(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), at("vue", "client", "node", "server"), nil)
	if err != nil {
		t.Fatal(err)
	}

	hooks := getHookIDs(config)
	for _, id := range []string{"vue-eslint-client", "vue-eslint-server"} {
		if !contains(hooks, id) {
			t.Errorf("missing hook %s, got %v", id, hooks)
		}
	}
	if contains(hooks, "vue-eslint") {
		t.Error("unsuffixed hook should not survive alongside suffixed ones")
	}
}

// The Go hooks walk every go.mod in the repo, so two modules must not produce
// two identical copies of the block.
func TestIntegration_IdenticalRendersCollapse(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), at("go", "api", "go", "cli"), nil)
	if err != nil {
		t.Fatal(err)
	}

	hooks := getHookIDs(config)
	count := 0
	for _, id := range hooks {
		if id == "go-vet-repo-mod" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("go-vet-repo-mod appears %d times, want 1: %v", count, hooks)
	}
}

// The SQL block is gated by a declared dialect, not by a component — a repo's
// .sql files have no build directory of their own, and nothing about them says
// postgres rather than sqlite.
func TestIntegration_SQLBlockFollowsTheDeclaredDialect(t *testing.T) {
	blocks := realBlocks(t)

	withDialect := &config.Toolchain{SQLDialect: "postgres"}
	config, err := Generate(blocks, testToolchain(t), withDialect, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(getHookIDs(config), "sqlfluff-lint") {
		t.Error("a declared dialect should pull in the SQL block")
	}
	if !strings.Contains(config, "--dialect, \"postgres\"") {
		t.Errorf("the declared dialect should reach the hook args:\n%s", config)
	}

	without, err := Generate(blocks, testToolchain(t), detected("python"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if contains(getHookIDs(without), "sqlfluff-lint") {
		t.Error("no declared dialect should mean no SQL block")
	}
}

func TestIntegration_FullStack(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), detected("python", "go", "vue", "docker", "actions", "terraform"), nil)
	if err != nil {
		t.Fatal(err)
	}

	genBlocks := getGeneratedBlocks(config)
	for _, name := range []string{"python-format", "python-lint", "go", "vue", "docker", "github-actions", "terraform"} {
		if !contains(genBlocks, name) {
			t.Errorf("missing block: %s", name)
		}
	}
}

func TestIntegration_GenericOnly(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, testToolchain(t), detected(), nil)
	if err != nil {
		t.Fatal(err)
	}

	genBlocks := getGeneratedBlocks(config)
	for _, name := range []string{"conventional-commits", "file-checks", "markdown", "shell", "codespell"} {
		if !contains(genBlocks, name) {
			t.Errorf("missing generic block: %s", name)
		}
	}
	for _, name := range []string{"python-format", "python-lint", "go", "vue", "docker", "terraform"} {
		if contains(genBlocks, name) {
			t.Errorf("block should not be present: %s", name)
		}
	}
}

func TestIntegration_NoDuplicateHookIDs(t *testing.T) {
	blocks := realBlocks(t)
	stacks := []struct {
		name       string
		components *config.Toolchain
	}{
		{"python", detected("python")},
		{"go", detected("go")},
		{"full", detected("python", "go", "vue", "rust", "lua", "docker", "actions", "terraform")},
		{"multi-vue", at("vue", "client", "node", "server")},
		{"empty", detected()},
	}

	for _, tc := range stacks {
		t.Run(tc.name, func(t *testing.T) {
			config, err := Generate(blocks, testToolchain(t), tc.components, nil)
			if err != nil {
				t.Fatal(err)
			}
			hooks := getHookIDs(config)
			seen := make(map[string]int)
			for _, id := range hooks {
				seen[id]++
			}
			for id, count := range seen {
				if count > 1 {
					t.Errorf("duplicate hook ID: %s (appears %d times)", id, count)
				}
			}
		})
	}
}

func TestIntegration_CustomBetweenBlocks(t *testing.T) {
	blocks := realBlocks(t)
	custom := map[string]string{
		"after:go": "# > custom:after:go - Script tests\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: my-test-runner\n" +
			"        name: test runner\n" +
			"        entry: ./run_tests.sh\n" +
			"        language: system\n" +
			"        pass_filenames: false",
	}

	config, err := Generate(blocks, testToolchain(t), detected("go", "actions"), custom)
	if err != nil {
		t.Fatal(err)
	}

	hooks := getHookIDs(config)
	if !contains(hooks, "my-test-runner") {
		t.Fatal("custom hook not found")
	}
	if !contains(hooks, "go-fumpt-repo") {
		t.Fatal("go hook not found")
	}
	if !contains(hooks, "actionlint") {
		t.Fatal("actionlint hook not found")
	}

	if indexOf(hooks, "golangci-lint-repo-mod") >= indexOf(hooks, "my-test-runner") {
		t.Error("my-test-runner should be after go hooks")
	}
	if indexOf(hooks, "my-test-runner") >= indexOf(hooks, "actionlint") {
		t.Error("my-test-runner should be before actionlint")
	}

	// Roundtrip
	extracted := ExtractCustomSections(config)
	config2, err := Generate(blocks, testToolchain(t), detected("go", "actions"), extracted)
	if err != nil {
		t.Fatal(err)
	}
	if config != config2 {
		t.Error("roundtrip produced different output")
	}
}

// testToolchain loads the real manifest. Unit tests using fstest.MapFS blocks
// carry repo URLs the manifest does not manage, which ApplyRevs leaves alone —
// exactly the behavior an unmanaged repo should get.
func testToolchain(t *testing.T) *toolchain.Toolchain {
	t.Helper()
	manifest, err := toolchain.Load(os.DirFS("../pre-commit"))
	if err != nil {
		t.Fatalf("toolchain.Load: %v", err)
	}
	return manifest
}

func TestGeneratedConfigCarriesToolchainVersion(t *testing.T) {
	manifest := testToolchain(t)
	blocks := os.DirFS("../pre-commit/blocks")

	config, err := Generate(blocks, manifest, detected("go"), nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := fmt.Sprintf("# forge-toolchain: %d", manifest.Version)
	if !strings.HasPrefix(config, want) {
		t.Errorf("config must start with %q, got %q", want, strings.SplitN(config, "\n", 2)[0])
	}
}

// A repo whose category renders in several directories gets suffixed hook ids,
// which appear in no block file. The safety check compared against the raw
// blocks, so such a repo was rejected by its own generated config on the second
// sync and could never move off the version that produced it.
func TestSafetyCheckAcceptsItsOwnSuffixedHooks(t *testing.T) {
	// A block carrying {{dir}} renders differently per directory, which is what
	// triggers suffixing; the Go block runs repo-wide and collapses instead.
	blocks := makeTestBlocks()
	blocks["50-vue.yml"] = &fstest.MapFile{
		Data: []byte("  # Vue\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: vue-eslint\n" +
			"        entry: bash -c 'cd {{dir}} && npm run lint'\n"),
	}
	declared := at("vue", "client", "node", "server")

	generated, err := Generate(blocks, testToolchain(t), declared, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(generated, "vue-eslint-client") {
		t.Fatalf("expected suffixed ids in a multi-directory render, got:\n%s", generated)
	}

	unknown, err := SafetyCheck(generated, blocks, declared, nil)
	if err != nil {
		t.Fatalf("SafetyCheck: %v", err)
	}
	if len(unknown) > 0 {
		t.Errorf("generated config rejected by its own safety check: %v", unknown)
	}

	// The widening is built from declared directories, so an unrelated hook that
	// merely starts with a standard id is still reported.
	stray := "repos:\n  - repo: local\n    hooks:\n      - id: vue-eslint-elsewhere\n"
	unknown, err = SafetyCheck(stray, blocks, declared, nil)
	if err != nil {
		t.Fatalf("SafetyCheck: %v", err)
	}
	if len(unknown) != 1 || unknown[0] != "vue-eslint-elsewhere" {
		t.Errorf("an undeclared suffix should still be unknown, got %v", unknown)
	}
}

func TestGenerateEmitsDeclaredExclude(t *testing.T) {
	manifest := testToolchain(t)
	blocks := os.DirFS("../pre-commit/blocks")

	declared := detected("python")
	declared.Exclude = "^tests/fixtures/"

	config, err := Generate(blocks, manifest, declared, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.Contains(config, "\nexclude: ^tests/fixtures/\n") {
		t.Error("declared exclude should appear as pre-commit's top-level exclude")
	}
	// It has to precede repos:, or pre-commit reads it as part of a hook entry.
	if strings.Index(config, "exclude:") > strings.Index(config, "repos:") {
		t.Error("exclude must be emitted before repos:")
	}

	bare, err := Generate(blocks, manifest, detected("python"), nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(bare, "\nexclude:") {
		t.Error("a repo declaring no exclude should get no top-level exclude")
	}
}

// Check is what the can-generate die uses to tell a clean sync from one that
// would abort. A hook no standard block provides must keep failing it — the fix
// is a marker, not silently dropping the hook's coverage.
func TestCheckReportsOnlyUnmarkedNonStandardHooks(t *testing.T) {
	// Absolute before any Chdir — a relative DirFS resolves lazily and would
	// point at the temp dir instead.
	blocksDir, err := filepath.Abs("../pre-commit/blocks")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	blocks := os.DirFS(blocksDir)

	t.Run("aliased hook is not reported", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeConfig(t, "repos:\n  - repo: local\n    hooks:\n      - id: eslint\n")

		unknown, err := Check(blocks, nil)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(unknown) != 0 {
			t.Errorf("eslint is replaced by vue-eslint and should not abort, got %v", unknown)
		}
	})

	t.Run("genuinely custom hook is reported", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeConfig(t, "repos:\n  - repo: local\n    hooks:\n      - id: sqlc-diff\n")

		unknown, err := Check(blocks, nil)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(unknown) != 1 || unknown[0] != "sqlc-diff" {
			t.Errorf("Check() = %v, want [sqlc-diff]", unknown)
		}
	})

	t.Run("marked custom hook is not reported", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeConfig(t, "repos:\n  - repo: local\n    hooks:\n      - id: x\n\n# > custom:after:all - project hooks\n  - repo: local\n    hooks:\n      - id: sqlc-diff\n")

		unknown, err := Check(blocks, nil)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		for _, id := range unknown {
			if id == "sqlc-diff" {
				t.Error("a marked hook must not abort")
			}
		}
	})
}

func writeConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(".pre-commit-config.yaml", []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}
