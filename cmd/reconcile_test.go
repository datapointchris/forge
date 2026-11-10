package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/runner"
)

// The two nouns are one implementation, and this is what keeps them one.
// cli-design.md asks that two front doors on the same dataset spell everything
// identically; asserting it beats remembering it, because the drift shows up as
// a flag that exists on one verb and not its twin.
func TestBothNounsSpellTheSharedVerbsIdentically(t *testing.T) {
	shared := []string{"check", "plan", "apply", "list"}

	for _, verb := range shared {
		t.Run(verb, func(t *testing.T) {
			fromRepos := findSub(t, repos.command(), verb)
			fromDirs := findSub(t, directories.command(), verb)

			if got, want := flagNames(fromDirs), flagNames(fromRepos); !slices.Equal(got, want) {
				t.Errorf("flags differ:\n  repos:       %v\n  directories: %v", want, got)
			}
			if fromDirs.Args == nil != (fromRepos.Args == nil) {
				t.Errorf("one of the two constrains its arguments and the other does not")
			}
		})
	}
}

// The asymmetry is deliberate and belongs to exactly one noun each: exec sweeps
// repos, and run exists because a directory has neither a git hook nor CI to
// execute its config. ci.md bans `pre-commit run` before a commit, so run must
// not appear under repos.
func TestEachNounKeepsItsOwnVerb(t *testing.T) {
	reposVerbs := subNames(repos.command())
	dirVerbs := subNames(directories.command())

	if !slices.Contains(reposVerbs, "exec") {
		t.Errorf("repos lost exec: %v", reposVerbs)
	}
	if slices.Contains(dirVerbs, "exec") {
		t.Errorf("directories grew exec, which sweeps repos: %v", dirVerbs)
	}
	if slices.Contains(reposVerbs, "run") {
		t.Errorf("repos grew run — a repo's hooks are run by git and CI: %v", reposVerbs)
	}
}

// The regression guard for the whole reason directories are not in the
// registry: nothing that means "repos" may return one.
func TestSelectReposNeverReturnsAMaintainedDirectory(t *testing.T) {
	registry := []config.Repo{
		{Name: "forge", Path: "~/src/forge", Status: "active"},
	}
	maintained := []config.Repo{
		{Name: "claude", Path: "~/.claude"},
	}

	selected := runner.SelectRepos(registry, nil)
	for _, got := range selected {
		for _, dir := range maintained {
			if got.Name == dir.Name {
				t.Errorf("SelectRepos returned the maintained directory %q", got.Name)
			}
		}
	}
	if len(selected) != 1 {
		t.Errorf("SelectRepos returned %d entries, want just the registry's 1", len(selected))
	}
}

// The count is the part a caller can already see, so an error that only reports
// it leaves them with no way to know that one name and no name are both legal
// and mean different scopes. Both nouns' three die-taking verbs are asserted,
// because the factory is what keeps them one and the test is what proves it did.
func TestTooManyDieNamesNamesBothWaysOut(t *testing.T) {
	for _, noun := range []*reconcileNoun{repos, directories} {
		for _, verb := range []string{"check", "plan", "apply"} {
			t.Run(noun.name+" "+verb, func(t *testing.T) {
				cmd := findSub(t, noun.command(), verb)

				err := cmd.Args(cmd, []string{"precommit", "ci"})
				if err == nil {
					t.Fatal("two die names were accepted")
				}
				if !errors.Is(err, goclikit.ErrUsage) {
					t.Errorf("not a usage error, so it exits 1 rather than 2: %v", err)
				}
				for _, want := range []string{"precommit", "ci", "omit it", "forge dies list"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("message does not carry %q: %v", want, err)
					}
				}
			})
		}
	}
}

func TestUnknownDieNameIsAUsageError(t *testing.T) {
	_, err := selectedDies([]string{"nosuchdie"})
	if !errors.Is(err, goclikit.ErrUsage) {
		t.Errorf("not a usage error, so it exits 1 rather than 2: %v", err)
	}
}

// A plan that measured nothing would otherwise exit 1, which a caller reads as
// changes pending, and check would answer the 1 it never gives.
func TestADeclarationThatWillNotLoadExits3AndSaysWhy(t *testing.T) {
	registry := execRegistry(t, "alpha")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	invalid := filepath.Join(t.TempDir(), "versions.json")
	if err := os.WriteFile(invalid, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfgPath = "" })

	for _, declared := range []string{"", invalid} {
		t.Setenv("FORGE_VERSIONS_FILE", declared)
		for _, verb := range []string{"check", "plan"} {
			var stderr bytes.Buffer
			if code := exitCode(&stderr, runLine(t, "repos", verb, "-c", registry)); code != 3 {
				t.Errorf("repos %s with versions file %q exits %d, want 3", verb, declared, code)
			}
			if !strings.Contains(stderr.String(), "versions_file") {
				t.Errorf("repos %s printed %q, want the reason naming versions_file", verb, stderr.String())
			}
		}
	}
}

// One die name is the ordinary case and stays legal.
func TestOneDieNameIsAccepted(t *testing.T) {
	cmd := findSub(t, repos.command(), "plan")

	if err := cmd.Args(cmd, []string{"precommit"}); err != nil {
		t.Errorf("one die name was rejected: %v", err)
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Errorf("no die name was rejected: %v", err)
	}
}

func findSub(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, sub := range parent.Commands() {
		if strings.Fields(sub.Use)[0] == name {
			return sub
		}
	}
	t.Fatalf("%s has no %q subcommand", parent.Name(), name)
	return nil
}

func subNames(parent *cobra.Command) []string {
	var names []string
	for _, sub := range parent.Commands() {
		names = append(names, strings.Fields(sub.Use)[0])
	}
	sort.Strings(names)
	return names
}

func flagNames(cmd *cobra.Command) []string {
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	return names
}
