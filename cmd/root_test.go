package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"
	"github.com/spf13/cobra"

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

// Left to cobra, which parses a group's flags before its arguments, an unknown
// word followed by a flag only a leaf declares reports the flag and never the word.
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

// Left to cobra, which answers --help before it validates arguments, a mistyped
// word asking for help prints the group's screen and exits 0 as though it matched.
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
// shows has to run. Each of these reads as a command on a help screen that
// lists the dies apart from the verbs that take them.
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

// Every command forge prints for its reader to run has to exist, and every
// command's help carries an example. A hint is read by someone already stuck,
// and it names a sibling, so it breaks when the sibling is renamed rather than
// when its own code changes. The walk reads each command's help in the
// assembled tree, where the noun factory's examples exist only once built, and
// every string literal in the module's source, where the error hints live.
func TestEveryPrintedCommandExistsAndEveryCommandHasAnExample(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		// cobra's own help and completion commands carry cobra's help text.
		if cmd.Hidden || cmd.Name() == "help" || cmd.Name() == "completion" {
			return
		}
		if strings.TrimSpace(cmd.Example) == "" {
			t.Errorf("%s has no example in its help", cmd.CommandPath())
		}
		for _, line := range shownCommands(cmd.Long + "\n" + cmd.Example) {
			if err := resolves(line); err != nil {
				t.Errorf("%s's help shows %q: %v", cmd.CommandPath(), line, err)
			}
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	shown := 0
	for _, lit := range sourceStrings(t, "..") {
		for _, line := range shownCommands(lit.value) {
			shown++
			if err := resolves(line); err != nil {
				t.Errorf("%s prints %q: %v", lit.at, line, err)
			}
		}
	}
	// A reader that found no source would pass with every hint broken.
	if shown < 20 {
		t.Fatalf("found %d commands printed in the source, want the hints and examples", shown)
	}
}

// sourceString is one string literal in the module's source and where it sits.
type sourceString struct{ at, value string }

// sourceStrings is every string literal in the module's non-test Go source
// under root. Test files are left out, because they print commands that must
// not resolve in order to prove the walk refuses them.
func sourceStrings(t *testing.T, root string) []sourceString {
	t.Helper()
	fset := token.NewFileSet()
	var found []sourceString
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if value, err := strconv.Unquote(lit.Value); err == nil {
					found = append(found, sourceString{at: fset.Position(lit.Pos()).String(), value: value})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
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
