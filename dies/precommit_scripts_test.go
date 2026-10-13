package dies

import (
	"strings"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

// trackAll turns the fixture into a real repo with everything staged, which is
// what shebangScripts enumerates. The fixture's .git is a bare directory with
// hook stubs in it and holds no index, so a scan run against it finds nothing.
func trackAll(t *testing.T, target reconcile.Target) {
	t.Helper()
	if _, err := runIn(target.Repo.Path, "git", "init", "-q"); err != nil {
		t.Fatalf("git init: %s", err)
	}
	if _, err := runIn(target.Repo.Path, "git", "add", "-A"); err != nil {
		t.Fatalf("git add: %s", err)
	}
}

// The scan is what makes a `uv run --script` app reachable. Selecting it by
// extension or by identify's tags cannot: it has no extension, and its shebang
// names uv.
func TestShebangScriptsFindsWhatIdentifyCannotTag(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{
		"apps/worktree":  "#!/usr/bin/env -S uv run --script\nimport os\n",
		"apps/notes":     "#!/usr/bin/env bash\necho hi\n",
		"apps/legacy":    "#!/usr/bin/python3\nimport os\n",
		"tools/build.py": "#!/usr/bin/env -S uv run --script\nimport os\n",
		"README.md":      "# fixture\n",
	})
	trackAll(t, target)

	got := shebangScripts(target.Repo.Path, true)

	want := []string{"apps/worktree"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("shebangScripts = %v, want %v", got, want)
	}
}

// A maintained directory has no index to enumerate, and can be a home
// directory. Walking one on every check is not a cost this finding is worth.
func TestShebangScriptsSkipsAnUnversionedTarget(t *testing.T) {
	target := unversionedFixture(t, stacks("python"), map[string]string{
		"apps/worktree": "#!/usr/bin/env -S uv run --script\nimport os\n",
	})

	if got := shebangScripts(target.Repo.Path, false); got != nil {
		t.Errorf("shebangScripts = %v, want nothing for a target git does not version", got)
	}
}

// The generated config has to name the scripts, or the hooks are installed
// against a pattern that matches nothing and every commit reports them passing.
func TestPreCommitConfigReachesTheScannedScripts(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{
		"apps/worktree": "#!/usr/bin/env -S uv run --script\nimport os\n",
	})
	trackAll(t, target)

	applyAll(t, target, PreCommit{})

	generated := readFile(t, target.Path(".pre-commit-config.yaml"))
	if !strings.Contains(generated, "mypy-scripts") {
		t.Errorf("the script hooks are missing from the config:\n%s", generated)
	}
	if !strings.Contains(generated, `files: '^(apps/worktree)$'`) {
		t.Errorf("the scanned path did not reach the config:\n%s", generated)
	}
}

// A repo with no such file is every repo but one. The block appearing there
// would be three hooks that can never fire, and pre-commit reports a hook that
// matched nothing as passing.
func TestPreCommitLeavesTheScriptBlockOutOfAnOrdinaryRepo(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{
		"tools/build.py": "import os\n",
	})
	trackAll(t, target)

	applyAll(t, target, PreCommit{})

	generated := readFile(t, target.Path(".pre-commit-config.yaml"))
	if strings.Contains(generated, "python-scripts") {
		t.Errorf("a repo with no untagged scripts got the block anyway:\n%s", generated)
	}
}
