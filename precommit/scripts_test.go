package precommit

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The five apps behind this all read `#!/usr/bin/env -S uv run --script`, and
// identify tags every one of them executable/file/text. A shebang naming a
// python interpreter is left out on purpose: identify already tags those, and
// selecting them here would hand the same file to ruff twice.
func TestUntaggedPythonScript(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		firstLine string
		want      bool
	}{
		{"uv run script", "apps/common/worktree", "#!/usr/bin/env -S uv run --script", true},
		{"uv run with inline deps", "apps/common/prs", "#!/usr/bin/env -S uv run --with rich", true},
		{"bare uv", "bin/thing", "#!/usr/bin/uv run --script", true},
		{"env python is already tagged", "apps/common/_aws-profiles", "#!/usr/bin/env python3", false},
		{"absolute python is already tagged", "deploy-cluster", "#!/usr/bin/python3", false},
		{"bash", "apps/common/notes", "#!/usr/bin/env bash", false},
		{"sh", "install", "#!/bin/sh", false},
		{"an extension is enough for identify", "tools/build.py", "#!/usr/bin/env -S uv run --script", false},
		{"no shebang", "LICENSE", "MIT License", false},
		{"empty file", "Makefile", "", false},
		{"shebang with nothing after it", "weird", "#!", false},
		{"env with no interpreter", "weird2", "#!/usr/bin/env -S", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UntaggedPythonScript(tc.path, tc.firstLine); got != tc.want {
				t.Errorf("UntaggedPythonScript(%q, %q) = %v, want %v", tc.path, tc.firstLine, got, tc.want)
			}
		})
	}
}

// The pattern is anchored and every path is quoted, because a files: key is a
// regex and an unescaped path would match more than the file it names.
func TestScriptsPattern(t *testing.T) {
	if got := ScriptsPattern(nil); got != "" {
		t.Errorf("ScriptsPattern(nil) = %q, want empty so the block is left out", got)
	}

	got := ScriptsPattern([]string{"apps/common/prs", "bin/a.out"})
	want := `^(apps/common/prs|bin/a\.out)$`
	if got != want {
		t.Errorf("ScriptsPattern() = %q, want %q", got, want)
	}
}

// scriptBlocks is a fixture carrying the placeholder, kept apart from
// makeTestBlocks so the other generator tests keep the block set they assert on.
func scriptBlocks() fstest.MapFS {
	blocks := makeTestBlocks()
	blocks["36-python-scripts.yml"] = &fstest.MapFile{
		Data: []byte("  # Python: apps identify cannot tag\n" +
			"  - repo: https://example.com/ruff\n" +
			"    hooks:\n" +
			"      - id: ruff-check\n" +
			"        alias: ruff-check-scripts\n" +
			"        files: '{{scripts}}'\n"),
	}
	return blocks
}

// A repo with none of these files is every repo but one, so the block appearing
// there would be hooks that can never fire — and pre-commit reports a hook that
// matched nothing as passing, which reads as coverage.
func TestScriptBlockFollowsTheScan(t *testing.T) {
	blocks := scriptBlocks()

	without, err := Generate(blocks, testToolchain(t), detected("python"), nil, true, nil)
	if err != nil {
		t.Fatalf("Generate: %s", err)
	}
	if contains(getGeneratedBlocks(without), "python-scripts") {
		t.Error("a repo with no untagged scripts got the block anyway")
	}
	if contains(getHookIDs(without), "ruff-check") {
		t.Error("the script hooks reached a repo the scan found nothing in")
	}

	with, err := Generate(blocks, testToolchain(t), detected("python"), nil, true, []string{"apps/common/worktree"})
	if err != nil {
		t.Fatalf("Generate: %s", err)
	}
	if !contains(getGeneratedBlocks(with), "python-scripts") {
		t.Fatalf("the block is missing from a repo the scan found scripts in:\n%s", with)
	}
	if !strings.Contains(with, `files: '^(apps/common/worktree)$'`) {
		t.Errorf("the scanned path did not reach the files: key:\n%s", with)
	}
	if strings.Contains(with, "{{scripts}}") {
		t.Error("the placeholder survived into the generated config")
	}
}

// The block is gated by a category Generate seeds, the way sql and git are, so
// a python component alone must not pull it in.
func TestScriptCategoryIsNotAComponent(t *testing.T) {
	dirs := dirsByCategory(detected("python").Components)

	include, err := ShouldIncludeBlock("python-scripts", dirs)
	if err != nil {
		t.Fatalf("ShouldIncludeBlock: %s", err)
	}
	if include {
		t.Error("declaring a python component pulled in the script block")
	}

	dirs[ScriptCategory] = []string{"."}
	include, err = ShouldIncludeBlock("python-scripts", dirs)
	if err != nil {
		t.Fatalf("ShouldIncludeBlock: %s", err)
	}
	if !include {
		t.Error("seeding the category did not pull in the script block")
	}
}
