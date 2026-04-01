package precommit

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
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

func detected(techs ...string) map[string]bool {
	m := make(map[string]bool)
	for _, t := range techs {
		m[t] = true
	}
	return m
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
	det := detected("python", "actions")

	if !ShouldIncludeBlock("conventional-commits", det) {
		t.Error("generic blocks should always be included")
	}
	if !ShouldIncludeBlock("file-checks", det) {
		t.Error("file-checks should always be included")
	}
	if !ShouldIncludeBlock("python-format", det) {
		t.Error("python-format should be included for python")
	}
	if ShouldIncludeBlock("go", det) {
		t.Error("go should not be included without go detected")
	}
	if ShouldIncludeBlock("vue", det) {
		t.Error("vue should not be included without vue detected")
	}
}

func TestGenerateSimpleConfig(t *testing.T) {
	blocks := makeTestBlocks()
	config, err := Generate(blocks, detected("python"), nil)
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

	config, err := Generate(blocks, detected("python"), custom)
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

func TestRoundtripPreservesCustom(t *testing.T) {
	blocks := makeTestBlocks()
	custom := map[string]string{
		"before:file-checks": "# > custom:before:file-checks - Capture\n" +
			"  - repo: local\n" +
			"    hooks:\n" +
			"      - id: devstats-capture",
	}

	config1, err := Generate(blocks, detected("python"), custom)
	if err != nil {
		t.Fatal(err)
	}

	extracted := ExtractCustomSections(config1)
	config2, err := Generate(blocks, detected("python"), extracted)
	if err != nil {
		t.Fatal(err)
	}

	if config1 != config2 {
		t.Error("roundtrip should produce identical output")
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

	unknown, err := SafetyCheck(configText, blocks, nil)
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

func TestSafetyCheckSkipsWithCustomMarkers(t *testing.T) {
	blocks := makeTestBlocks()
	configText := "repos:\n" +
		"  - repo: local\n" +
		"    hooks:\n" +
		"      - id: unknown-hook\n"

	customSections := map[string]string{
		"after:all": "# > custom:after:all - Stuff\n  - repo: local\n    hooks:\n      - id: unknown-hook",
	}

	unknown, err := SafetyCheck(configText, blocks, customSections)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) > 0 {
		t.Error("safety check should be skipped when custom markers exist")
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

	config, err := Generate(blocks, detected("python"), custom)
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
	config, err := Generate(blocks, detected("python"), nil)
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
	config, err := Generate(blocks, detected("go"), nil)
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

func TestIntegration_FullStack(t *testing.T) {
	blocks := realBlocks(t)
	config, err := Generate(blocks, detected("python", "go", "vue", "docker", "actions", "terraform"), nil)
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
	config, err := Generate(blocks, detected(), nil)
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
		name string
		det  map[string]bool
	}{
		{"python", detected("python")},
		{"go", detected("go")},
		{"full", detected("python", "go", "vue", "docker", "actions", "terraform")},
		{"empty", detected()},
	}

	for _, tc := range stacks {
		t.Run(tc.name, func(t *testing.T) {
			config, err := Generate(blocks, tc.det, nil)
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

	config, err := Generate(blocks, detected("go", "actions"), custom)
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
	config2, err := Generate(blocks, detected("go", "actions"), extracted)
	if err != nil {
		t.Fatal(err)
	}
	if config != config2 {
		t.Error("roundtrip produced different output")
	}
}
