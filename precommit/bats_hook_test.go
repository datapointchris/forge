package precommit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Without the unset, bats inherits the committing repository's GIT_DIR, and a
// test's `git config user.email` sets the identity every later commit there
// carries.
func TestTheBatsHookHidesTheCommittingRepositoryFromItsTests(t *testing.T) {
	data, err := os.ReadFile("../pre-commit/blocks/15-shell.yml")
	if err != nil {
		t.Fatal(err)
	}
	var entry string
	for _, hook := range GeneratedHooks("# generated:shell\n" + string(data)) {
		if hook.ID == "bats" {
			entry = hook.Entry
		}
	}
	if entry == "" {
		t.Fatal("the shell block carries no bats entry")
	}

	committing := filepath.Join(t.TempDir(), "committing")
	if out, err := exec.Command("git", "init", "-q", committing).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %s", err, out)
	}
	suite := t.TempDir()
	if err := os.MkdirAll(filepath.Join(suite, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(suite, "tests", "fixture.bats"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	seen := filepath.Join(t.TempDir(), "environment")
	if err := os.WriteFile(filepath.Join(bin, "bats"), []byte("#!/bin/sh\nenv > \"$SEEN\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SEEN", seen)
	exported := map[string]string{
		"GIT_DIR":        filepath.Join(committing, ".git"),
		"GIT_INDEX_FILE": filepath.Join(committing, ".git", "index"),
		"GIT_WORK_TREE":  committing,
	}
	for name, value := range exported {
		t.Setenv(name, value)
	}

	hook := exec.Command("sh", "-c", entry)
	hook.Dir = suite
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("the hook failed: %s: %s", err, out)
	}
	environment, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("bats never ran: %s", err)
	}
	for _, line := range strings.Split(string(environment), "\n") {
		name, _, _ := strings.Cut(line, "=")
		if _, leaked := exported[name]; leaked {
			t.Errorf("bats saw %s", line)
		}
	}
}
