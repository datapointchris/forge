package dies

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forgeRoot returns the root of the forge repo (one level up from dies/).
func forgeRoot(t *testing.T) string {
	t.Helper()
	// This file is at dies/sync_precommit_test.go
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..")
}

// makeTempRepo creates a temp directory with a .git dir and optional marker files.
func makeTempRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runSyncDie runs the sync-pre-commit die in the given repo directory.
// Returns the generated .pre-commit-config.yaml content.
func runSyncDie(t *testing.T, repoDir string) string {
	t.Helper()
	root := forgeRoot(t)
	script := filepath.Join(root, "dies", "maintenance", "sync-pre-commit.sh")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoDir
	// pre-commit install will fail in temp repos, that's fine
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Exit 2 = SKIP (nothing to do) — valid for idempotent reruns
		if cmd.ProcessState.ExitCode() != 2 {
			t.Fatalf("sync die failed: %s\n%s", err, out)
		}
	}

	content, err := os.ReadFile(filepath.Join(repoDir, ".pre-commit-config.yaml"))
	if err != nil {
		t.Fatalf("generated config not found: %s", err)
	}
	return string(content)
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

func TestSyncDie_PythonRepo(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"test\"\n",
	})

	config := runSyncDie(t, dir)
	blocks := getGeneratedBlocks(config)
	hooks := getHookIDs(config)

	// Python blocks present
	if !contains(blocks, "python-format") {
		t.Error("missing python-format block")
	}
	if !contains(blocks, "python-lint") {
		t.Error("missing python-lint block")
	}

	// Python hooks present
	for _, id := range []string{"ruff-format", "ruff-check", "mypy", "uv-lock"} {
		if !contains(hooks, id) {
			t.Errorf("missing hook: %s", id)
		}
	}

	// Go/Vue blocks absent
	if contains(blocks, "go") {
		t.Error("go block should not be present")
	}
	if contains(blocks, "vue") {
		t.Error("vue block should not be present")
	}
}

func TestSyncDie_GoRepo(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"go.mod":  "module example.com/test\n\ngo 1.23\n",
		"main.go": "package main\n",
	})

	config := runSyncDie(t, dir)
	blocks := getGeneratedBlocks(config)
	hooks := getHookIDs(config)

	if !contains(blocks, "go") {
		t.Error("missing go block")
	}
	if !contains(hooks, "go-fumpt-repo") {
		t.Error("missing go-fumpt-repo hook")
	}
	if contains(blocks, "python-format") {
		t.Error("python block should not be present")
	}
}

func TestSyncDie_GenericOnly(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"README.md": "# Test\n",
	})

	config := runSyncDie(t, dir)
	blocks := getGeneratedBlocks(config)

	expected := []string{"conventional-commits", "file-checks", "markdown", "shell", "codespell"}
	for _, b := range expected {
		if !contains(blocks, b) {
			t.Errorf("missing generic block: %s", b)
		}
	}

	absent := []string{"python-format", "python-lint", "go", "vue", "docker", "terraform"}
	for _, b := range absent {
		if contains(blocks, b) {
			t.Errorf("block should not be present: %s", b)
		}
	}
}

func TestSyncDie_NoDuplicateHookIDs(t *testing.T) {
	stacks := []struct {
		name  string
		files map[string]string
	}{
		{"python", map[string]string{"pyproject.toml": "[project]\nname = \"t\"\n"}},
		{"go", map[string]string{"go.mod": "module t\n\ngo 1.23\n", "main.go": "package main\n"}},
		{"full", map[string]string{
			"pyproject.toml":           "[project]\nname = \"t\"\n",
			"go.mod":                   "module t\n\ngo 1.23\n",
			"main.go":                  "package main\n",
			"frontend/package.json":    "{}\n",
			"Dockerfile":               "FROM alpine\n",
			".github/workflows/ci.yml": "on: push\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo\n",
		}},
	}

	for _, tc := range stacks {
		t.Run(tc.name, func(t *testing.T) {
			dir := makeTempRepo(t, tc.files)
			config := runSyncDie(t, dir)
			hooks := getHookIDs(config)

			seen := map[string]int{}
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

func TestSyncDie_CustomHooksPreserved(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"go.mod":                   "module t\n\ngo 1.23\n",
		"main.go":                  "package main\n",
		".github/workflows/ci.yml": "on: push\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo\n",
		".pre-commit-config.yaml": strings.Join([]string{
			"# > custom:before:file-checks - Stats capture",
			"  - repo: local",
			"    hooks:",
			"      - id: devstats-capture",
			"        name: devstats capture",
			"        entry: echo captured",
			"        language: system",
			"",
			"# > custom:after:go - Script tests",
			"  - repo: local",
			"    hooks:",
			"      - id: my-test-runner",
			"        name: test runner",
			"        entry: ./run_tests.sh",
			"        language: system",
			"",
			"# > custom:after:all - End hooks",
			"  - repo: local",
			"    hooks:",
			"      - id: devstats-collect",
			"        name: devstats collect",
			"        entry: echo collected",
			"        language: system",
		}, "\n"),
	})

	config := runSyncDie(t, dir)
	hooks := getHookIDs(config)

	// Custom hooks present
	for _, id := range []string{"devstats-capture", "my-test-runner", "devstats-collect"} {
		if !contains(hooks, id) {
			t.Errorf("custom hook lost: %s", id)
		}
	}

	// Ordering: devstats-capture before check-yaml
	if indexOf(hooks, "devstats-capture") > indexOf(hooks, "check-yaml") {
		t.Error("devstats-capture should be before check-yaml")
	}

	// Ordering: my-test-runner after go hooks, before actionlint
	if indexOf(hooks, "my-test-runner") < indexOf(hooks, "golangci-lint-repo-mod") {
		t.Error("my-test-runner should be after go hooks")
	}
	if indexOf(hooks, "my-test-runner") > indexOf(hooks, "actionlint") {
		t.Error("my-test-runner should be before actionlint")
	}

	// Ordering: devstats-collect is last
	if indexOf(hooks, "devstats-collect") != len(hooks)-1 {
		t.Error("devstats-collect should be last hook")
	}

	// Roundtrip: regenerate should be identical
	config2 := runSyncDie(t, dir)
	if config != config2 {
		t.Error("roundtrip produced different output")
	}
}

func TestSyncDie_CustomHookDedupsStandard(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"t\"\n",
		".pre-commit-config.yaml": strings.Join([]string{
			"# > custom:after:python-lint - Custom mypy",
			"  - repo: local",
			"    hooks:",
			"      - id: mypy",
			"        name: custom mypy",
			"        entry: uv run mypy custom-dir",
			"        language: system",
		}, "\n"),
	})

	config := runSyncDie(t, dir)
	hooks := getHookIDs(config)

	count := 0
	for _, id := range hooks {
		if id == "mypy" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("mypy appears %d times, want 1", count)
	}

	if !strings.Contains(config, "custom-dir") {
		t.Error("custom mypy entry should be present, not the standard one")
	}
}

func TestSyncDie_SafetyAbortsOnUnknownHooks(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"t\"\n",
		".pre-commit-config.yaml": strings.Join([]string{
			"repos:",
			"  - repo: local",
			"    hooks:",
			"      - id: check-yaml",
			"      - id: my-unknown-hook",
			"      - id: another-unknown",
		}, "\n"),
	})

	root := forgeRoot(t)
	script := filepath.Join(root, "dies", "maintenance", "sync-pre-commit.sh")

	cmd := exec.Command("bash", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("expected die to fail on unknown hooks, but it succeeded")
	}
	if !strings.Contains(string(out), "ABORT") {
		t.Errorf("expected ABORT message, got: %s", out)
	}
	if !strings.Contains(string(out), "my-unknown-hook") {
		t.Errorf("expected unknown hook name in output, got: %s", out)
	}
}

func TestSyncDie_DeploysMarkdownlintConfig(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"README.md": "# Test\n",
	})

	runSyncDie(t, dir)

	mdlint := filepath.Join(dir, ".markdownlint.json")
	if _, err := os.Stat(mdlint); err != nil {
		t.Error(".markdownlint.json not deployed")
	}
}

func TestSyncDie_DeploysGolangciConfig(t *testing.T) {
	dir := makeTempRepo(t, map[string]string{
		"go.mod":  "module t\n\ngo 1.23\n",
		"main.go": "package main\n",
	})

	runSyncDie(t, dir)

	gc := filepath.Join(dir, ".golangci.yml")
	if _, err := os.Stat(gc); err != nil {
		t.Error(".golangci.yml not deployed")
	}
}
