package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigReadsMaintainedDirectories(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	path := writeConfig(t, `
maintained_directories:
  - name: dev
    path: ~/dev
    description: Workspace root.
    toolchain:
      components: []

  - name: claude
    path: ~/.claude
    toolchain:
      components:
        - {stack: python, dir: .}
        - {stack: shell, dir: .}
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if len(cfg.MaintainedDirectories) != 2 {
		t.Fatalf("len(MaintainedDirectories) = %d, want 2", len(cfg.MaintainedDirectories))
	}

	// Sorted by name, so claude comes first however the file was edited.
	claude := cfg.MaintainedDirectories[0]
	if claude.Name != "claude" {
		t.Errorf("first directory = %q, want claude", claude.Name)
	}
	if want := filepath.Join(home, ".claude"); claude.Path != want {
		t.Errorf("claude.Path = %q, want %q", claude.Path, want)
	}
	if got := claude.Toolchain.Stacks(); !reflect.DeepEqual(got, []string{"python", "shell"}) {
		t.Errorf("claude stacks = %v, want [python shell]", got)
	}

	// components: [] is not the same as no toolchain — the precommit die bails
	// on a nil Toolchain, so an empty declaration is how a directory asks for
	// the generic blocks and nothing else.
	dev := cfg.MaintainedDirectories[1]
	if dev.Toolchain == nil {
		t.Fatal("dev.Toolchain = nil, want a non-nil empty declaration")
	}
	if got := dev.Toolchain.Stacks(); len(got) != 0 {
		t.Errorf("dev stacks = %v, want none", got)
	}
}

func TestLoadConfigMissingFileIsNotAnError(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil {
		t.Fatalf("LoadConfig() on a missing file: %v", err)
	}
	if len(cfg.MaintainedDirectories) != 0 {
		t.Errorf("MaintainedDirectories = %v, want none", cfg.MaintainedDirectories)
	}
}

// The shipped template is comments and nothing else, which yaml decodes to EOF.
func TestLoadConfigCommentsOnlyIsAnEmptyConfig(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, "# forge — machine config.\n# Nothing declared yet.\n"))
	if err != nil {
		t.Fatalf("LoadConfig() on a comments-only file: %v", err)
	}
	if len(cfg.MaintainedDirectories) != 0 {
		t.Errorf("MaintainedDirectories = %v, want none", cfg.MaintainedDirectories)
	}
}

// A misspelled key must fail loudly. Silently dropping it would leave a
// directory undeclared, and an undeclared directory reads as a converged one.
func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "maintained_dirs:\n  - name: dev\n    path: ~/dev\n"))
	if err == nil {
		t.Fatal("LoadConfig() accepted an unknown key, want an error")
	}
	if !strings.Contains(err.Error(), "maintained_dirs") {
		t.Errorf("error does not name the bad key: %v", err)
	}
}

// Repo carries both json and yaml tags. yaml.v3 defaults to lowercasing the Go
// field name, which disagrees with the JSON spelling on every multi-word key —
// sql_dialect would arrive as nil rather than failing. This is the assertion
// that keeps the two formats describing one struct.
func TestRepoParsesIdenticallyFromJSONAndYAML(t *testing.T) {
	const asJSON = `{
		"name": "logsift",
		"path": "~/tools/logsift",
		"status": "active",
		"toolchain": {
			"components": [{"stack": "go", "dir": "."}],
			"sql_dialect": "sqlite",
			"shellcheck_disable": [{"rule": "SC2029", "reason": "deliberate remote interpolation"}],
			"exclude": "^fixtures/"
		},
		"synced_dirs": [{"dir": "stats/data", "as": "stats"}]
	}`

	const asYAML = `
name: logsift
path: ~/tools/logsift
status: active
toolchain:
  components:
    - {stack: go, dir: .}
  sql_dialect: sqlite
  shellcheck_disable:
    - {rule: SC2029, reason: deliberate remote interpolation}
  exclude: "^fixtures/"
synced_dirs:
  - {dir: stats/data, as: stats}
`

	var fromJSON Repo
	if err := json.Unmarshal([]byte(asJSON), &fromJSON); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	path := writeConfig(t, "maintained_directories:\n"+indent(asYAML))
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.MaintainedDirectories) != 1 {
		t.Fatalf("len(MaintainedDirectories) = %d, want 1", len(cfg.MaintainedDirectories))
	}
	fromYAML := cfg.MaintainedDirectories[0]

	// LoadConfig expands the tilde; the JSON path did not go through a loader.
	expanded, err := ExpandTilde(fromJSON.Path)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON.Path = expanded

	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Errorf("JSON and YAML disagree:\n json: %+v\n yaml: %+v", fromJSON, fromYAML)
	}
}

// indent turns a top-level YAML document into one list item.
func indent(body string) string {
	var out strings.Builder
	first := true
	for line := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		if first {
			out.WriteString("  - " + line + "\n")
			first = false
			continue
		}
		out.WriteString("    " + line + "\n")
	}
	return out.String()
}
