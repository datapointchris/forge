package dies

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/forge/v3/runner"
)

// The die's SKIP contract, shared with the runner that interprets it.
const exitSkip = runner.ExitSkip

// runGitignoreDie runs the sync-gitignore die in the given repo directory and
// returns its combined output and exit code. Exit 2 is SKIP, not a failure, so
// the code is returned rather than asserted on here.
func runGitignoreDie(t *testing.T, repoDir string, extraEnv ...string) (string, int) {
	t.Helper()
	script := filepath.Join(forgeRoot(t), "dies", "maintenance", "sync-gitignore.sh")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"PATH="+forgeOnPath(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FORGE_REPOS_FILE="+filepath.Join(filepath.Dir(repoDir), "repos.json"))
	cmd.Env = append(cmd.Env, extraEnv...)

	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func gitignoreLines(t *testing.T, repoDir string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %s", err)
	}
	var lines []string
	for _, line := range strings.Split(string(content), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestSyncGitignore_PythonRepoGetsCoverageAndBuildArtifacts(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		".gitignore": ".planning\n",
	})

	if _, code := runGitignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	lines := gitignoreLines(t, dir)
	for _, entry := range []string{".coverage", "coverage.xml", "dist/", "*.egg-info/"} {
		if !contains(lines, entry) {
			t.Errorf("missing entry: %s", entry)
		}
	}
}

func TestSyncGitignore_GoRepoGetsNoPythonEntries(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), map[string]string{
		".gitignore": ".planning\n",
	})

	// Already has the only universal entry, so there is nothing to do.
	if _, code := runGitignoreDie(t, dir); code != exitSkip {
		t.Fatalf("expected SKIP, got exit %d", code)
	}

	lines := gitignoreLines(t, dir)
	if contains(lines, ".coverage") {
		t.Error(".coverage leaked into a repo declaring no python component")
	}
}

func TestSyncGitignore_AddsPlanningToAnyStack(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), map[string]string{
		".gitignore": "bin/\n",
	})

	if _, code := runGitignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	lines := gitignoreLines(t, dir)
	if !contains(lines, ".planning") {
		t.Error("has-clean-gitignore fails a repo without .planning, so the die must add it")
	}
}

// The whole reason this cannot be a file deploy like .editorconfig: a repo's own
// entries are not drift.
func TestSyncGitignore_PreservesProjectEntries(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		".gitignore": ".planning\nsecrets.env\nfixtures/large/\n",
	})

	if _, code := runGitignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	lines := gitignoreLines(t, dir)
	for _, entry := range []string{"secrets.env", "fixtures/large/"} {
		if !contains(lines, entry) {
			t.Errorf("project entry was lost: %s", entry)
		}
	}
}

func TestSyncGitignore_IsIdempotent(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		".gitignore": ".planning\n",
	})

	runGitignoreDie(t, dir)
	first := gitignoreLines(t, dir)

	out, code := runGitignoreDie(t, dir)
	if code != exitSkip {
		t.Fatalf("second run should SKIP, got exit %d: %s", code, out)
	}

	second := gitignoreLines(t, dir)
	if len(first) != len(second) {
		t.Errorf("rerun changed the file: %v then %v", first, second)
	}
}

// A .gitignore whose last line has no newline would otherwise get the first new
// entry glued onto it, silently corrupting both.
func TestSyncGitignore_HandlesMissingTrailingNewline(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		".gitignore": ".planning",
	})

	if _, code := runGitignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	lines := gitignoreLines(t, dir)
	if !contains(lines, ".planning") {
		t.Error(".planning was corrupted by the append")
	}
	if !contains(lines, ".coverage") {
		t.Error(".coverage did not land on its own line")
	}
}

func TestSyncGitignore_CheckWritesNothing(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		".gitignore": ".planning\n",
	})

	out, code := runGitignoreDie(t, dir, "FORGE_CHECK=1")
	if code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}
	if !strings.Contains(out, "would add") {
		t.Errorf("check mode should report what it would add, got: %s", out)
	}

	lines := gitignoreLines(t, dir)
	if contains(lines, ".coverage") {
		t.Error("check mode wrote to .gitignore — the exact failure supports_check exists to prevent")
	}
}

// A new repo is where the golden path is needed most, so an absent .gitignore is
// created rather than skipped. Append-only protects a repo's own entries, and a
// file that does not exist has none.
func TestSyncGitignore_CreatesGitignoreWhenAbsent(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"test\"\n",
	})

	out, code := runGitignoreDie(t, dir)
	if code != 0 {
		t.Fatalf("expected OK, got exit %d: %s", code, out)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("output should distinguish creating from appending, got: %s", out)
	}

	lines := gitignoreLines(t, dir)
	for _, entry := range []string{".planning", ".coverage", "coverage.xml", "dist/", "*.egg-info/"} {
		if !contains(lines, entry) {
			t.Errorf("created .gitignore missing %q, got %v", entry, lines)
		}
	}
}

// The created file must not open with a blank line: creation writes an empty
// file and the append path then runs its trailing-newline guard over it.
func TestSyncGitignore_CreatedFileHasNoLeadingBlankLine(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), map[string]string{
		"go.mod": "module test\n",
	})

	if _, code := runGitignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %s", err)
	}
	if strings.HasPrefix(string(content), "\n") {
		t.Errorf("created .gitignore opens with a blank line: %q", string(content))
	}
}

func TestSyncGitignore_CheckDoesNotCreateGitignore(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("python"), map[string]string{
		"pyproject.toml": "[project]\nname = \"test\"\n",
	})

	out, code := runGitignoreDie(t, dir, "FORGE_CHECK=1")
	if code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}
	if !strings.Contains(out, "would create") {
		t.Errorf("check mode should report the creation, got: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Error("check mode created .gitignore — the exact failure supports_check exists to prevent")
	}
}
