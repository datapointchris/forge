package dies

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runMarkdownlintignoreDie(t *testing.T, repoDir string, extraEnv ...string) (string, int) {
	t.Helper()
	script := filepath.Join(forgeRoot(t), "dies", "maintenance", "sync-markdownlintignore.sh")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"PATH="+forgeOnPath(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_DATA_HOME="+filepath.Dir(repoDir))
	cmd.Env = append(cmd.Env, extraEnv...)

	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func markdownlintignoreLines(t *testing.T, repoDir string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoDir, ".markdownlintignore"))
	if err != nil {
		t.Fatalf("reading .markdownlintignore: %s", err)
	}
	var lines []string
	for _, line := range strings.Split(string(content), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestSyncMarkdownlintignore_CreatesTheFileWhenAbsent(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), nil)

	if _, code := runMarkdownlintignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	if !contains(markdownlintignoreLines(t, dir), "CHANGELOG.md") {
		t.Error("a new repo is exactly the case that gets the changelog churn")
	}
}

// The whole reason this cannot be a file deploy like .markdownlint.json.
func TestSyncMarkdownlintignore_PreservesProjectEntries(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), map[string]string{
		".markdownlintignore": "PLANNING.md\nanalysis/**\n",
	})

	if _, code := runMarkdownlintignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	lines := markdownlintignoreLines(t, dir)
	for _, entry := range []string{"PLANNING.md", "analysis/**"} {
		if !contains(lines, entry) {
			t.Errorf("project entry was lost: %s", entry)
		}
	}
}

func TestSyncMarkdownlintignore_IsIdempotent(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), nil)

	runMarkdownlintignoreDie(t, dir)
	first := markdownlintignoreLines(t, dir)

	out, code := runMarkdownlintignoreDie(t, dir)
	if code != exitSkip {
		t.Fatalf("second run should SKIP, got exit %d: %s", code, out)
	}

	if len(first) != len(markdownlintignoreLines(t, dir)) {
		t.Errorf("rerun changed the file: %v", first)
	}
}

// Without the newline guard the first new entry is glued to the last line,
// silently corrupting both.
func TestSyncMarkdownlintignore_HandlesMissingTrailingNewline(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), map[string]string{
		".markdownlintignore": "PLANNING.md",
	})

	if _, code := runMarkdownlintignoreDie(t, dir); code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}

	lines := markdownlintignoreLines(t, dir)
	if !contains(lines, "PLANNING.md") || !contains(lines, "CHANGELOG.md") {
		t.Errorf("entries were merged: %v", lines)
	}
}

func TestSyncMarkdownlintignore_CheckModeWritesNothing(t *testing.T) {
	dir := makeTempRepo(t, stacksAt("go"), nil)

	out, code := runMarkdownlintignoreDie(t, dir, "FORGE_CHECK=1")
	if code != 0 {
		t.Fatalf("expected OK, got exit %d: %s", code, out)
	}

	if _, err := os.Stat(filepath.Join(dir, ".markdownlintignore")); !os.IsNotExist(err) {
		t.Error("--check created the file")
	}
}
