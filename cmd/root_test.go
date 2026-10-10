package cmd

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"
)

// runLine runs one command line through run, the path the shipped binary
// takes, with the version check suppressed. goclikit.Execute resolves the
// command from os.Args, so the line goes there rather than through SetArgs.
func runLine(t *testing.T, args ...string) error {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"forge"}, args...)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	t.Cleanup(func() {
		os.Args = original
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	return run(autoupdate.Config{Suppress: true})
}

// A word one slip from a subcommand is answered with the subcommand, so a
// mistyped verb names the one that was meant.
func TestAnUnknownSubcommandNamesTheNearOnes(t *testing.T) {
	err := runLine(t, "repos", "plam")
	if !errors.Is(err, goclikit.ErrUsage) {
		t.Fatalf("repos plam is not a usage error, so it exits 1 rather than 2: %v", err)
	}
	if !slices.Contains(strings.Fields(err.Error()), "plan") {
		t.Errorf("repos plam answered %v, want it to name plan", err)
	}
}

// Cobra parses a group's flags before its arguments, so an unknown word followed
// by a flag only a leaf declares reported the flag and never the word.
func TestAWordBeforeAFlagIsRefusedOnEveryGroup(t *testing.T) {
	for _, group := range []string{"cli", "config", "dies", "directories", "repos", "stamp", "toolchain"} {
		err := runLine(t, group, "bogus", "--json")
		if !errors.Is(err, goclikit.ErrUsage) {
			t.Errorf("%s bogus --json is not a usage error, so it exits 1 rather than 2: %v", group, err)
			continue
		}
		if want := `unknown command "bogus" for "forge ` + group + `"`; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s bogus --json answered %v, want it to open %q", group, err, want)
		}
	}
}

// Cobra answers --help before it validates arguments, so a mistyped word asking
// for help printed the group's screen and exited 0 as though it had matched.
func TestAWordTypedWithHelpIsRefusedOnEveryGroup(t *testing.T) {
	lines := [][]string{{"repos", "bogus", "-h"}}
	for _, group := range []string{"cli", "config", "dies", "directories", "repos", "stamp", "toolchain"} {
		lines = append(lines, []string{group, "bogus", "--help"})
	}
	for _, args := range lines {
		err := runLine(t, args...)
		if !errors.Is(err, goclikit.ErrUsage) {
			t.Errorf("%v is not a usage error, so it exits 0 or 1 rather than 2: %v", args, err)
			continue
		}
		if want := `unknown command "bogus" for "forge ` + args[0] + `"`; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%v answered %v, want it to open %q", args, err, want)
		}
	}
}
