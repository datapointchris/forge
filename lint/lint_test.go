package lint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/gitenv"
	"github.com/datapointchris/forge/precommit"
)

const committedHooks = `repos:
  # generated:shell
  - repo: local
    hooks:
      - id: fixer
        name: fixer
        entry: true
        language: system
  # > custom:after:all - the repo's own
  - repo: local
    hooks:
      - id: needs-a-workstation
        name: needs a workstation
        entry: true
        language: system
`

// repoWith commits files into a fresh repository under a temporary home, so
// the clone lands in a cache the test owns.
func repoWith(t *testing.T, files map[string]string) config.Repo {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(gitenv.WithoutRepoTarget(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	for rel, body := range files {
		write(t, filepath.Join(root, rel), body)
		run("add", "--", rel)
	}
	run("commit", "--quiet", "--no-verify", "-m", "fixture")
	return config.Repo{Name: "fixture", Path: root}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A fixer writes into whatever tree it runs in, and someone may be working in
// the checkout. So the hooks see HEAD in a clone, an uncommitted edit stays
// where it was, and the clone is gone afterwards.
func TestTheHooksRunInAThrowawayCloneOfHEAD(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks, "script.sh": "committed\n"})
	write(t, filepath.Join(repo.Path, "script.sh"), "uncommitted\n")

	var ran []string
	var seen, dir string
	result := RunWith(repo, func(_ context.Context, in, hook string) (int, string) {
		ran = append(ran, hook)
		dir = in
		body, _ := os.ReadFile(filepath.Join(in, "script.sh"))
		seen = string(body)
		write(t, filepath.Join(in, "script.sh"), "rewritten by a fixer\n")
		return 0, ""
	})

	if result.Outcome != Passed {
		t.Fatalf("outcome = %q (%s)", result.Outcome, result.Note)
	}
	if !slices.Equal(ran, []string{"fixer"}) {
		t.Errorf("ran %v, want the standard hook alone", ran)
	}
	if seen != "committed\n" {
		t.Errorf("the hook saw %q, want HEAD's content", seen)
	}
	if body, _ := os.ReadFile(filepath.Join(repo.Path, "script.sh")); string(body) != "uncommitted\n" {
		t.Errorf("the checkout was written: %q", body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the clone at %s outlived the run", dir)
	}
}

// CI runs the committed config, so a config nobody committed lints nothing.
func TestAnUncommittedConfigHasNoHooks(t *testing.T) {
	repo := repoWith(t, map[string]string{"script.sh": "committed\n"})
	write(t, filepath.Join(repo.Path, precommit.ConfigPath), committedHooks)

	result := RunWith(repo, func(context.Context, string, string) (int, string) {
		t.Error("a hook ran from a config HEAD does not hold")
		return 0, ""
	})
	if result.Outcome != NoHooks {
		t.Errorf("outcome = %q, want %q", result.Outcome, NoHooks)
	}
}

// The vue hooks resolve their tools in node_modules, which no clone carries.
// Each installed package is linked in. The caches tools keep in its
// dot-directories are the clone's own, so a hook's write never reaches the
// checkout, and removing the clone never removes what a link points at.
func TestInstalledPackagesAreLinkedAndTheirCachesStayInTheClone(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks, "web/package.json": "{}\n"})
	modules := filepath.Join(repo.Path, "web", "node_modules")
	write(t, filepath.Join(modules, "eslint", "package.json"), "{}\n")
	write(t, filepath.Join(modules, ".bin", "eslint"), "#!/bin/sh\n")
	write(t, filepath.Join(modules, ".cache", "checkout-only"), "\n")

	var sawPackage, sawBin, sawCache bool
	RunWith(repo, func(_ context.Context, in, _ string) (int, string) {
		linked := filepath.Join(in, "web", "node_modules")
		sawPackage = exists(filepath.Join(linked, "eslint", "package.json"))
		sawBin = exists(filepath.Join(linked, ".bin", "eslint"))
		sawCache = exists(filepath.Join(linked, ".cache", "checkout-only"))
		write(t, filepath.Join(linked, ".cache", "jiti", "config.cjs"), "\n")
		return 0, ""
	})

	if !sawPackage || !sawBin {
		t.Errorf("the hook saw package=%v .bin=%v, want both linked", sawPackage, sawBin)
	}
	if sawCache {
		t.Error("the hook saw the checkout's cache")
	}
	if exists(filepath.Join(modules, ".cache", "jiti")) {
		t.Error("a cache written in the clone reached the checkout")
	}
	if !exists(filepath.Join(modules, "eslint", "package.json")) {
		t.Error("removing the clone removed the installed packages")
	}
}

// npm ci runs postinstall in CI, and Nuxt's generates the types its typecheck
// reads. So the clone runs it against HEAD, and the checkout is not written.
func TestAPackagesPostinstallRunsInTheClone(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is not installed")
	}
	repo := repoWith(t, map[string]string{
		precommit.ConfigPath: committedHooks,
		"web/package.json":   `{"scripts":{"postinstall":"echo generated > .generated"}}` + "\n",
	})
	write(t, filepath.Join(repo.Path, "web", "node_modules", "eslint", "package.json"), "{}\n")

	var generated bool
	result := RunWith(repo, func(_ context.Context, in, _ string) (int, string) {
		generated = exists(filepath.Join(in, "web", ".generated"))
		return 0, ""
	})

	if result.Outcome != Passed {
		t.Fatalf("outcome = %q (%s)", result.Outcome, result.Note)
	}
	if !generated {
		t.Error("the hooks ran before postinstall generated its files")
	}
	if exists(filepath.Join(repo.Path, "web", ".generated")) {
		t.Error("postinstall wrote into the checkout")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A tool missing from this machine says nothing about the code, so it must not
// read as a failure. A real failure still outranks it.
func TestAMissingToolIsUnknownAndAFailureOutranksIt(t *testing.T) {
	repo := repoWith(t, map[string]string{precommit.ConfigPath: committedHooks + `  # generated:python
  - repo: local
    hooks:
      - id: typecheck
        name: typecheck
        entry: true
        language: system
`})
	outputs := map[string]struct {
		code   int
		output string
	}{
		"fixer":     {1, "Executable `shfmt` not found"},
		"typecheck": {1, "- hook id: typecheck\n- exit code: 1\n\nfound 2 errors"},
	}
	result := RunWith(repo, func(_ context.Context, _, hook string) (int, string) {
		return outputs[hook].code, outputs[hook].output
	})

	if result.Outcome != Failed {
		t.Errorf("outcome = %q, want failed", result.Outcome)
	}
	byID := map[string]Outcome{}
	for _, hook := range result.Hooks {
		byID[hook.ID] = hook.Outcome
	}
	if byID["fixer"] != Unknown || byID["typecheck"] != Failed {
		t.Errorf("hook outcomes = %v, want fixer unknown and typecheck failed", byID)
	}
}
