package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/internal/config"
)

func TestFilterRepos(t *testing.T) {
	repos := []config.Repo{
		{Name: "alpha", Path: "/a"},
		{Name: "beta", Path: "/b"},
		{Name: "gamma", Path: "/c"},
	}

	t.Run("filter by name", func(t *testing.T) {
		got := FilterRepos(repos, []string{"alpha", "gamma"})
		if len(got) != 2 {
			t.Errorf("len = %d, want 2", len(got))
		}
		if got[0].Name != "alpha" || got[1].Name != "gamma" {
			t.Errorf("got %v, want alpha and gamma", got)
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		got := FilterRepos(repos, []string{"nonexistent"})
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}

func TestExecuteInRepo(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		repo := config.Repo{Name: "ghost", Path: "/nonexistent/path"}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo", "hi"}})
		if r.Status != "SKIP (not found)" {
			t.Errorf("status = %q, want SKIP (not found)", r.Status)
		}
	})

	t.Run("not a git repo", func(t *testing.T) {
		dir := t.TempDir()
		repo := config.Repo{Name: "no-git", Path: dir}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo", "hi"}})
		if r.Status != "SKIP (not a git repo)" {
			t.Errorf("status = %q, want SKIP (not a git repo)", r.Status)
		}
	})

	t.Run("successful inline command", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		repo := config.Repo{Name: "good", Path: dir}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo", "hello"}})
		if r.Status != "OK" {
			t.Errorf("status = %q, want OK", r.Status)
		}
	})

	t.Run("failing command", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		repo := config.Repo{Name: "bad", Path: dir}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"false"}})
		if r.Status != "FAIL (exit 1)" {
			t.Errorf("status = %q, want FAIL (exit 1)", r.Status)
		}
	})

	t.Run("exit 2 is skip", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(t.TempDir(), "skip.sh")
		if err := os.WriteFile(script, []byte("#!/bin/bash\nexit 2\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		repo := config.Repo{Name: "skipped", Path: dir}
		r := ExecuteInRepo(repo, Opts{ScriptFile: script})
		if r.Status != "SKIP (nothing to do)" {
			t.Errorf("status = %q, want SKIP (nothing to do)", r.Status)
		}
	})

	t.Run("successful script", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(t.TempDir(), "test.sh")
		if err := os.WriteFile(script, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		repo := config.Repo{Name: "scripted", Path: dir}
		r := ExecuteInRepo(repo, Opts{ScriptFile: script})
		if r.Status != "OK" {
			t.Errorf("status = %q, want OK", r.Status)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		repo := config.Repo{Name: "any", Path: "/does/not/matter"}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo"}, DryRun: true})
		if r.Status != "OK" {
			t.Errorf("status = %q, want OK", r.Status)
		}
	})
}
