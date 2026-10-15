package precommit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// batsEntry is the command the shipped shell block gives its bats hook.
func batsEntry(t *testing.T) string {
	t.Helper()
	config, err := Generate(realBlocks(t), testToolchain(t), detected("shell"), nil, Observed{Versioning: Unversioned})
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range GeneratedHooks(config) {
		if hook.ID == "bats" {
			return hook.Entry
		}
	}
	t.Fatal("the shell block generated no bats hook")
	return ""
}

// runBatsEntry runs the hook's entry in a repo holding the given files, on a
// PATH of stand-ins: a bats that records its arguments, and a parallel only
// where withParallel asks for one. It returns those arguments, or false where
// bats never ran. The real bash, git and getconf are linked in by name, so a
// parallel installed on the host cannot leak into the run that has none.
func runBatsEntry(t *testing.T, files []string, withParallel bool) (string, bool) {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"bash", "git", "getconf"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s is not on PATH: %v", tool, err)
		}
		if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	record := filepath.Join(t.TempDir(), "bats-args")
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("bats", `printf '%s\n' "$*" > "$BATS_ARGS"`)
	if withParallel {
		stub("parallel", "exit 0")
	}

	repo := t.TempDir()
	for _, file := range files {
		path := filepath.Join(repo, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(filepath.Join(bin, "bash"), "-c", batsEntry(t))
	cmd.Dir = repo
	cmd.Env = []string{"PATH=" + bin, "HOME=" + t.TempDir(), "BATS_ARGS=" + record}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bats hook failed: %v\n%s", err, out)
	}
	args, err := os.ReadFile(record)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(args)), true
}

func TestBatsHookRunsAJobPerCoreWhereParallelIsOnPath(t *testing.T) {
	cores, err := exec.Command("getconf", "_NPROCESSORS_ONLN").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "--jobs " + strings.TrimSpace(string(cores)) + " tests/"

	if got, ran := runBatsEntry(t, []string{"tests/a.bats"}, true); !ran || got != want {
		t.Errorf("bats ran %v with %q, want %q", ran, got, want)
	}
}

func TestBatsHookRunsSeriallyWhereParallelIsMissing(t *testing.T) {
	if got, ran := runBatsEntry(t, []string{"tests/a.bats"}, false); !ran || got != "tests/" {
		t.Errorf("bats ran %v with %q, want %q", ran, got, "tests/")
	}
}

func TestBatsHookSkipsARepoWithNoSuites(t *testing.T) {
	if got, ran := runBatsEntry(t, []string{"tests/nested/a.bats"}, true); ran {
		t.Errorf("bats ran with %q in a repo with no tests/*.bats", got)
	}
}
