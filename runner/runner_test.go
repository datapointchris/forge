package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/v3/config"
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

func TestOwnedRepos(t *testing.T) {
	repos := []config.Repo{
		{Name: "homelab", Path: "~/homelab"},
		{Name: "homelab", Path: "~/code/refs/homelab", Owner: "khuedoan"},
		{Name: "httpx", Path: "~/code/refs/httpx", Owner: "encode"},
		{Name: "forge", Path: "~/tools/forge"},
	}

	got := OwnedRepos(repos)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.Owner != "" {
			t.Errorf("reference clone leaked through: %+v", r)
		}
	}
	// The portfolio homelab must survive despite sharing a name with a clone.
	if got[0].Path != "~/homelab" {
		t.Errorf("got[0].Path = %q, want ~/homelab", got[0].Path)
	}
}

func TestSelectRepos(t *testing.T) {
	repos := []config.Repo{
		{Name: "forge", Path: "~/tools/forge"},
		{Name: "httpx", Path: "~/code/refs/httpx", Owner: "encode"},
		{Name: "sess", Path: "~/tools/sess", Status: "retired"},
	}

	t.Run("implicit selection excludes reference clones", func(t *testing.T) {
		got := SelectRepos(repos, nil)
		if len(got) != 1 || got[0].Name != "forge" {
			t.Errorf("got %v, want only forge", got)
		}
	})

	t.Run("explicit -F reaches a reference clone", func(t *testing.T) {
		got := SelectRepos(repos, []string{"httpx"})
		if len(got) != 1 || got[0].Name != "httpx" {
			t.Errorf("got %v, want httpx — naming a repo explicitly must override", got)
		}
	})

	t.Run("retired repos stay excluded either way", func(t *testing.T) {
		if got := SelectRepos(repos, []string{"sess"}); len(got) != 0 {
			t.Errorf("got %v, want none — retired repos are never selected", got)
		}
	})
}

func TestExecuteInRepoExportsRepoName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "name.sh")
	// The registry name differs from the cwd basename, so a basename-derived
	// value would fail here — which is the bug this env var exists to prevent.
	body := "#!/bin/bash\n[ \"$FORGE_REPO_NAME\" = zmk-config-corne42 ] || exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := config.Repo{Name: "zmk-config-corne42", Path: dir}
	if r := ExecuteInRepo(repo, Opts{ScriptFile: script}); r.Status != "OK" {
		t.Errorf("status = %q, want OK (FORGE_REPO_NAME not exported)", r.Status)
	}
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
