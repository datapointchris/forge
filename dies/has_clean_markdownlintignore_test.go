package dies

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The check reads only the working directory, so unlike the sync dies it needs
// neither a registry nor forge on PATH.
func runMarkdownlintignoreCheck(t *testing.T, files map[string]string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", filepath.Join(forgeRoot(t), "dies", "checks", "has-clean-markdownlintignore.sh"))
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func TestHasCleanMarkdownlintignore_SkipsWithoutAChangelog(t *testing.T) {
	_, code := runMarkdownlintignoreCheck(t, map[string]string{".markdownlintignore": "PLANNING.md\n"})
	if code != 2 {
		t.Fatalf("a repo with nothing to protect is a SKIP, got exit %d", code)
	}
}

func TestHasCleanMarkdownlintignore_FailsWhenTheChangelogIsUnignored(t *testing.T) {
	_, code := runMarkdownlintignoreCheck(t, map[string]string{
		"CHANGELOG.md":        "# Changelog\n",
		".markdownlintignore": "PLANNING.md\n",
	})
	if code != 1 {
		t.Fatalf("expected FAIL, got exit %d", code)
	}
}

func TestHasCleanMarkdownlintignore_FailsWhenTheIgnoreFileIsMissing(t *testing.T) {
	_, code := runMarkdownlintignoreCheck(t, map[string]string{"CHANGELOG.md": "# Changelog\n"})
	if code != 1 {
		t.Fatalf("expected FAIL, got exit %d", code)
	}
}

func TestHasCleanMarkdownlintignore_PassesWhenIgnored(t *testing.T) {
	_, code := runMarkdownlintignoreCheck(t, map[string]string{
		"CHANGELOG.md":        "# Changelog\n",
		".markdownlintignore": "PLANNING.md\nCHANGELOG.md\n",
	})
	if code != 0 {
		t.Fatalf("expected OK, got exit %d", code)
	}
}

// A substring match would pass on docs/changelog/CHANGELOG.md, which ignores a
// different file entirely.
func TestHasCleanMarkdownlintignore_RequiresAWholeLineMatch(t *testing.T) {
	_, code := runMarkdownlintignoreCheck(t, map[string]string{
		"CHANGELOG.md":        "# Changelog\n",
		".markdownlintignore": "docs/changelog/CHANGELOG.md\n",
	})
	if code != 1 {
		t.Fatalf("expected FAIL, got exit %d", code)
	}
}
