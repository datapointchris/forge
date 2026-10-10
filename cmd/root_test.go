package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"

	"github.com/datapointchris/forge/dies"
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

// backticked is a command quoted inside prose.
var backticked = regexp.MustCompile("`(forge [^`]*)`")

// shownCommands is every forge command line the root help prints: an indented
// line opening with one, which is how the lists and examples are laid out, and
// any quoted in a sentence. Unindented prose that opens with "forge" describes
// the tool rather than showing a command, and a column of two or more spaces
// separates a listed command from what it is for.
func shownCommands(help string) []string {
	var shown []string
	for _, line := range strings.Split(help, "\n") {
		if trimmed := strings.TrimLeft(line, " "); trimmed != line && strings.HasPrefix(trimmed, "forge ") {
			command, _, _ := strings.Cut(trimmed, "  ")
			shown = append(shown, command)
		}
		for _, match := range backticked.FindAllStringSubmatch(line, -1) {
			shown = append(shown, match[1])
		}
	}
	return shown
}

// resolves answers whether a command line shown in help runs as written, up to
// its first flag or placeholder: every word names a command, and a word left
// over is one the command's own completion offers when it has one, so a die
// name is checked against the dies.
func resolves(line string) error {
	var words []string
	for _, word := range strings.Fields(line)[1:] {
		if strings.HasPrefix(word, "-") || strings.HasPrefix(word, "[") || strings.HasPrefix(word, "<") {
			break
		}
		words = append(words, word)
	}
	found, rest, err := rootCmd.Find(words)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return nil
	}
	if found.HasSubCommands() {
		return fmt.Errorf("%q is not a subcommand of %s", rest[0], found.CommandPath())
	}
	if found.ValidArgsFunction != nil {
		offered, _ := found.ValidArgsFunction(found, nil, "")
		if !slices.Contains(offered, rest[0]) {
			return fmt.Errorf("%s does not take %q; it offers %v", found.CommandPath(), rest[0], offered)
		}
	}
	return nil
}

// The root help is what a session reads before guessing, so every command it
// shows has to run. Each of these was typed after reading a help screen that
// listed the dies apart from the verbs that take them.
func TestResolvesRefusesTheWordsForgeDoesNotHave(t *testing.T) {
	for _, line := range []string{
		"forge precommit",
		"forge ci",
		"forge brief",
		"forge stacks",
		"forge dies release",
		"forge repos plan nope",
	} {
		if resolves(line) == nil {
			t.Errorf("%q resolved, want it refused", line)
		}
	}
}

func TestEveryCommandTheRootHelpShowsExists(t *testing.T) {
	shown := shownCommands(rootCmd.Long + "\n" + rootCmd.Example)
	if len(shown) < 8 {
		t.Fatalf("found %d commands in the root help, want the verbs and examples: %v", len(shown), shown)
	}
	for _, line := range shown {
		if err := resolves(line); err != nil {
			t.Errorf("the root help shows %q: %v", line, err)
		}
	}
}

// A die is reached through a verb, so the root help is where its name has to
// be found by anyone who would otherwise type it as a command.
func TestTheRootHelpNamesEveryDie(t *testing.T) {
	listed := strings.FieldsFunc(rootCmd.Long, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	for _, name := range dies.BuiltinNames() {
		if !slices.Contains(listed, name) {
			t.Errorf("the root help does not name the %s die", name)
		}
	}
}

// The screen as printed, for reading in -v output. Every line stays inside a
// standard terminal.
func TestTheRootHelpFitsATerminal(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(nil)
	if err := rootCmd.Help(); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + out.String())
	for _, line := range strings.Split(rootCmd.Long+"\n"+rootCmd.Example, "\n") {
		if len(line) > 80 {
			t.Errorf("%d columns: %q", len(line), line)
		}
	}
}

func TestWrapListBreaksBeforeTheWidth(t *testing.T) {
	got := wrapList([]string{"alpha", "beta", "gamma"}, "  ", 14)
	want := "  alpha, beta,\n  gamma"
	if got != want {
		t.Errorf("wrapList = %q, want %q", got, want)
	}
}
