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
  - repo: local
    hooks:
      - id: mypy-scripts
        name: mypy (uv run scripts)
        entry: 'uv run mypy --scripts-are-modules'
        language: system
      - id: bats
        entry: bash -c 'compgen -G "tests/*.bats" > /dev/null || exit 0; exec bats tests/'
`
	want := []GeneratedHook{
		{Block: "file-checks", Repo: "https://example.com/hooks", ID: "check-yaml"},
		{Block: "python-scripts", Repo: "https://example.com/ruff", ID: "ruff-format", Alias: "ruff-format-scripts", Stages: []string{"pre-commit", "pre-push"}},
		{Block: "python-scripts", Repo: "local", ID: "mypy-scripts", Entry: "uv run mypy --scripts-are-modules"},
		{Block: "python-scripts", Repo: "local", ID: "bats", Entry: `bash -c 'compgen -G "tests/*.bats" > /dev/null || exit 0; exec bats tests/'`},
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

func TestShadowedNamesTheStandardHookACustomSectionReplaces(t *testing.T) {
	standard := "# generated:refcheck - Reference checking\n" +
		"  - repo: https://example.com/refcheck\n" +
		"    rev: v2\n" +
		"    hooks:\n" +
		"      - id: refcheck\n"
	custom := map[string]string{"after:all": "# > custom:after:all - ours\n" +
		"  - repo: https://example.com/refcheck\n" +
		"    rev: v0\n" +
		"    hooks:\n" +
		"      - id: refcheck\n" +
		"      - id: our-own\n"}

	if got := Shadowed(standard, custom); !reflect.DeepEqual(got, []string{"refcheck"}) {
		t.Errorf("Shadowed = %v, want [refcheck]", got)
	}
}
