package precommit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each line of refused.cases is a call to YouTube the hook refuses on its own,
// and each line of passed.cases is a mention it lets through. They are files
// rather than strings here because this repo runs the hook too, and it never
// reads a file that identify gives no language.
func TestTheYouTubeDoorHookRefusesACallAndPassesAMention(t *testing.T) {
	ctx := t.Context()
	config, err := Generate(realBlocks(t), testToolchain(t), detected(), nil, Observed{Versioning: Versioned}, UsesTheYouTubeDoor)
	if err != nil {
		t.Fatal(err)
	}
	var entry string
	for _, hook := range GeneratedHooks(config) {
		if hook.ID == "youtube-door" {
			entry = hook.Entry
		}
	}
	if entry == "" {
		t.Fatal("a versioned repo got no youtube-door hook")
	}

	for _, kind := range []struct {
		file    string
		refused bool
	}{{"refused.cases", true}, {"passed.cases", false}} {
		data, err := os.ReadFile(filepath.Join("testdata", "youtube-door", kind.file))
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(data)) {
			if strings.TrimSpace(line) == "" {
				continue
			}
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.CommandContext(ctx, "bash", "-c", entry+` "$0"`, path).CombinedOutput()
			var exit *exec.ExitError
			switch {
			case err == nil:
				if kind.refused {
					t.Errorf("passed %q", line)
				}
			case errors.As(err, &exit) && exit.ExitCode() == 1:
				if !kind.refused {
					t.Errorf("refused %q:\n%s", line, out)
				}
			default:
				t.Fatalf("hook errored on %q: %v\n%s", line, err, out)
			}
		}
	}
}
