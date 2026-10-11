package runner

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/forge/config"
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

// Reference is derived by config.LoadSyncerConfig from the registry's own
// owner, so a fixture built as literals states it the way Load would.
func TestOwnedRepos(t *testing.T) {
	repos := []config.Repo{
		{Name: "mimic", Path: "~/mimic"},
		{Name: "mimic", Path: "~/code/refs/mimic", Owner: "someoneelse", Reference: true},
		{Name: "httpx", Path: "~/code/refs/httpx", Owner: "encode", Reference: true},
		{Name: "forge", Path: "~/src/forge"},
	}

	got := OwnedRepos(repos)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.Reference {
			t.Errorf("reference clone leaked through: %+v", r)
		}
	}
	// The portfolio repo must survive despite sharing a name with a clone.
	if got[0].Path != "~/mimic" {
		t.Errorf("got[0].Path = %q, want ~/mimic", got[0].Path)
	}
}

func TestSelectRepos(t *testing.T) {
	repos := []config.Repo{
		{Name: "forge", Path: "~/src/forge", Status: "active"},
		{Name: "httpx", Path: "~/code/refs/httpx", Owner: "encode", Reference: true},
		{Name: "sess", Path: "~/src/sess", Status: "retired"},
		{Name: "reddit-nlp", Path: "~/code/reddit-nlp", Status: "dormant"},
	}

	t.Run("implicit selection excludes reference clones", func(t *testing.T) {
		got := SelectRepos(repos, nil)
		if len(got) != 1 || got[0].Name != "forge" {
			t.Errorf("got %v, want only forge", got)
		}
	})

	// Most of the portfolio is dormant and none of it takes another release,
	// so sweeping it is churn.
	t.Run("implicit selection excludes dormant repos", func(t *testing.T) {
		for _, r := range SelectRepos(repos, nil) {
			if r.Status == "dormant" {
				t.Errorf("dormant repo leaked into an implicit sweep: %+v", r)
			}
		}
	})

	t.Run("explicit -F reaches a dormant repo", func(t *testing.T) {
		got := SelectRepos(repos, []string{"reddit-nlp"})
		if len(got) != 1 || got[0].Name != "reddit-nlp" {
			t.Errorf("got %v, want reddit-nlp — dormant is excluded by default, not hidden", got)
		}
	})

	// The registry suite refuses an entry with no status, so the only way one
	// arrives is a registry that skipped it. forge supplies no meaning for it.
	t.Run("an entry with no status is not swept implicitly", func(t *testing.T) {
		fresh := []config.Repo{{Name: "fresh", Path: "~/src/fresh"}}
		if got := SelectRepos(fresh, nil); len(got) != 0 {
			t.Errorf("got %v, want none — an absent status is not active", got)
		}
		if got := SelectRepos(fresh, []string{"fresh"}); len(got) != 1 {
			t.Errorf("got %v, want fresh — naming it with -F still reaches it", got)
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
	body := "#!/bin/bash\n[ \"$FORGE_REPO_NAME\" = widget-config-alpha ] || exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := config.Repo{Name: "widget-config-alpha", Path: dir}
	if r := ExecuteInRepo(repo, Opts{ScriptFile: script}, io.Discard, io.Discard); r.Status != "OK" {
		t.Errorf("status = %q, want OK (FORGE_REPO_NAME not exported)", r.Status)
	}
}

func TestExecuteInRepo(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		repo := config.Repo{Name: "ghost", Path: "/nonexistent/path"}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo", "hi"}}, io.Discard, io.Discard)
		if r.Status != "SKIP (not found)" {
			t.Errorf("status = %q, want SKIP (not found)", r.Status)
		}
	})

	t.Run("not a git repo", func(t *testing.T) {
		dir := t.TempDir()
		repo := config.Repo{Name: "no-git", Path: dir}
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo", "hi"}}, io.Discard, io.Discard)
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
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"echo", "hello"}}, io.Discard, io.Discard)
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
		r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"false"}}, io.Discard, io.Discard)
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
		r := ExecuteInRepo(repo, Opts{ScriptFile: script}, io.Discard, io.Discard)
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
		r := ExecuteInRepo(repo, Opts{ScriptFile: script}, io.Discard, io.Discard)
		if r.Status != "OK" {
			t.Errorf("status = %q, want OK", r.Status)
		}
	})
}

func gitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A reader of many repos' output in one stream needs each line to say which
// repo it came from, on the stream the command wrote it to.
func TestEveryOutputLineCarriesTheRepoName(t *testing.T) {
	repo := config.Repo{Name: "alpha", Path: gitDir(t)}
	script := filepath.Join(t.TempDir(), "speak.sh")
	body := "#!/bin/bash\necho one\necho two\necho oops >&2\nprintf 'no newline'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if r := ExecuteInRepo(repo, Opts{ScriptFile: script}, &stdout, &stderr); r.Status != "OK" {
		t.Fatalf("status = %q, want OK", r.Status)
	}
	if want := "alpha\tone\nalpha\ttwo\nalpha\tno newline\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if want := "alpha\toops\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestALineSplitAcrossWritesCarriesOneTag(t *testing.T) {
	var out bytes.Buffer
	w := &taggedLines{out: &out, tag: "alpha\t"}
	for _, chunk := range []string{"par", "tial\nnext", " line\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if want := "alpha\tpartial\nalpha\tnext line\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

// A command that cannot start has no exit code. Reading one gives -1, and the
// start error that says why is the only account of the failure.
func TestACommandThatCannotStartSaysWhy(t *testing.T) {
	repo := config.Repo{Name: "alpha", Path: gitDir(t)}
	var stderr bytes.Buffer
	r := ExecuteInRepo(repo, Opts{InlineArgs: []string{"forge-no-such-command"}}, io.Discard, &stderr)
	if r.Status != "FAIL (did not start)" {
		t.Errorf("status = %q, want FAIL (did not start)", r.Status)
	}
	if !strings.HasPrefix(stderr.String(), "alpha\t") || !strings.Contains(stderr.String(), "forge-no-such-command") {
		t.Errorf("stderr = %q, want the start error tagged with the repo", stderr.String())
	}
}
