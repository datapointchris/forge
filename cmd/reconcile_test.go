package cmd

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/forge/v6/config"
	"github.com/datapointchris/forge/v6/runner"
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
		{Name: "forge", Path: "~/tools/forge", Status: "active"},
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
