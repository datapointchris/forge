package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/goselfupdate/autoupdate"
)

func execRegistry(t *testing.T, names ...string) string {
	t.Helper()
	var entries []string
	for _, name := range names {
		checkout := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, `{"name": "`+name+`", "path": "`+checkout+`", "status": "active", "owner": "me"}`)
	}
	registry := filepath.Join(t.TempDir(), "repos.json")
	body := `{"owner": "me", "repos": [` + strings.Join(entries, ", ") + `]}`
	if err := os.WriteFile(registry, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return registry
}

func runForge(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	original := os.Args
	os.Args = append([]string{"forge"}, args...)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		os.Args = original
		cfgPath = ""
		execDryRun = false
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := run(autoupdate.Config{Suppress: true}); err != nil {
		t.Fatalf("forge %s: %v", strings.Join(args, " "), err)
	}
	return out.String(), errOut.String()
}

// A tally over many repos reads stdout alone, so a status line or a glyph there
// is a row the tally miscounts.
func TestExecStdoutIsTheCommandsTaggedOutputAlone(t *testing.T) {
	registry := execRegistry(t, "alpha", "beta")
	stdout, stderr := runForge(t, "repos", "exec", "-c", registry, "--", "echo", "hi")

	if want := "alpha\thi\nbeta\thi\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if !strings.Contains(stderr, "2 ok") {
		t.Errorf("stderr = %q, want each repo's status and the tally", stderr)
	}
}

func TestExecDryRunListsTheReposOnStdout(t *testing.T) {
	registry := execRegistry(t, "alpha", "beta")
	stdout, stderr := runForge(t, "repos", "exec", "-c", registry, "-n", "--", "echo", "hi")

	if stdout != "alpha\nbeta\n" {
		t.Errorf("stdout = %q, want one repo name per line", stdout)
	}
	if !strings.Contains(stderr, "nothing ran") {
		t.Errorf("stderr = %q, want it to say nothing ran", stderr)
	}
}
