package precommit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A grep the entry's quoting broke would exit 2, which the hook's && reads as
// no match, so every message would pass. Running the shipped entry is what
// catches that.
func TestTheSkipMarkerHookRefusesEachDocumentedSpelling(t *testing.T) {
	config, err := Generate(realBlocks(t), testToolchain(t), detected(), nil, Observed{Versioning: Versioned})
	if err != nil {
		t.Fatal(err)
	}
	var entry string
	for _, hook := range GeneratedHooks(config) {
		if hook.ID == "no-ci-skip-marker" {
			entry = hook.Entry
		}
	}
	if entry == "" {
		t.Fatal("a versioned repo got no skip-marker hook")
	}

	cases := []struct {
		message string
		refused bool
	}{
		{"fix: a\n\n[skip ci]\n", true},
		{"fix: a [ci skip]\n", true},
		{"fix: a\n\nbody [no ci] here\n", true},
		{"[skip actions] fix: a\n", true},
		{"fix: a\n\n[actions skip]\n", true},
		{"fix: a\n\n[Skip CI]\n", true},
		{"fix: a\n\n\nskip-checks:true\n", true},
		{"fix: a\n\n\nskip-checks: true\n", true},
		{"fix: a\n\nA skip-checks: true trailer skips the run.\n", false},
		{"fix: a\n# [skip ci] in a template comment\n", false},
		{"fix: describe the bracketed skip marker in words\n", false},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
		if err := os.WriteFile(path, []byte(tc.message), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("bash", "-c", entry+` "$0"`, path).CombinedOutput()
		var exit *exec.ExitError
		switch {
		case err == nil:
			if tc.refused {
				t.Errorf("passed %q", tc.message)
			}
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			if !tc.refused {
				t.Errorf("refused %q:\n%s", tc.message, out)
			}
		default:
			t.Fatalf("hook errored on %q: %v\n%s", tc.message, err, out)
		}
	}
}
