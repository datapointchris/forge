package precommit

import (
	"reflect"
	"testing"
)

func TestGeneratedHooksLeavesOutACustomSection(t *testing.T) {
	config := `# forge-toolchain: 1
repos:

# generated:file-checks - File checks
  - repo: https://example.com/hooks
    rev: v1
    hooks:
      - id: check-yaml

# > custom:after:file-checks - ours
  - repo: https://example.com/ours
    rev: v1
    hooks:
      - id: our-hook

# generated:python-scripts - Scripts
  - repo: https://example.com/ruff
    rev: v1
    hooks:
      - id: ruff-format
        alias: ruff-format-scripts
        stages: [pre-commit, "pre-push"]
`
	want := []GeneratedHook{
		{Block: "file-checks", Repo: "https://example.com/hooks", ID: "check-yaml"},
		{Block: "python-scripts", Repo: "https://example.com/ruff", ID: "ruff-format-scripts", Stages: []string{"pre-commit", "pre-push"}},
	}
	if got := GeneratedHooks(config); !reflect.DeepEqual(got, want) {
		t.Errorf("GeneratedHooks =\n%+v\nwant\n%+v", got, want)
	}
}

func TestAGenericBlockIsItsOwnCategory(t *testing.T) {
	for block, want := range map[string]string{"shell": "shell", "python-lint": "python", "file-checks": "file-checks"} {
		if got := BlockCategory(block); got != want {
			t.Errorf("BlockCategory(%q) = %q, want %q", block, got, want)
		}
	}
}
