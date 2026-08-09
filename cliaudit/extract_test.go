package cliaudit

import (
	"strings"
	"testing"
)

// fakeRunner answers from a map keyed by the joined arguments, so the parsers
// are tested against captured help output rather than whatever happens to be
// installed on the machine running the tests.
func fakeRunner(responses map[string]string) runner {
	return func(_ string, args ...string) string {
		return responses[strings.Join(args, " ")]
	}
}

const cobraRoot = `A test tool.

Usage:
  demo [command]

Available Commands:
  books       Work with books
`

func TestCobraTreeComesFromCompleteNotHelp(t *testing.T) {
	run := fakeRunner(map[string]string{
		"__complete ":            "books\tWork with books\n:4\nCompletion ended with directive: ShellCompDirectiveNoFileComp",
		"__complete books ":      "list\tList books\nshow\tShow one book\n:4\nCompletion ended with directive: ShellCompDirectiveNoFileComp",
		"__complete books list ": ":4\nCompletion ended with directive: ShellCompDirectiveNoFileComp",
		"__complete books show ": ":4\nCompletion ended with directive: ShellCompDirectiveNoFileComp",
		"--help":                 cobraRoot,
		"books --help":           "Work with books\n\nUsage:\n  demo books [command]\n\nAvailable Commands:\n  list        List books\n  show        Show one book\n",
		"books list --help":      "List books\n\nUsage:\n  demo books list [flags]\n\nFlags:\n      --json   as JSON\n",
		"books show --help":      "Show one book\n\nUsage:\n  demo books show <id>\n",
	})

	tool := extractWith("demo", "", run)
	if tool.Framework != FrameworkCobra {
		t.Fatalf("framework = %q, want cobra", tool.Framework)
	}

	var paths []string
	tool.Walk(func(n *Node) { paths = append(paths, strings.Join(n.Path, " ")) })
	want := []string{"books", "books list", "books show"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestCobraLeafCarriesItsFlags(t *testing.T) {
	run := fakeRunner(map[string]string{
		"__complete ":      "list\tList\n:4\nCompletion ended with directive: x",
		"__complete list ": ":4\nCompletion ended with directive: x",
		"--help":           "A test tool.\n\nUsage:\n  demo [command]\n\nAvailable Commands:\n  list        List\n",
		"list --help":      "Usage:\n  demo list [flags]\n\nFlags:\n      --json    as JSON\n      --limit int\n",
	})
	tool := extractWith("demo", "", run)
	var flags []string
	tool.Walk(func(n *Node) { flags = n.Flags })
	if !contains(flags, "--json") || !contains(flags, "--limit") {
		t.Errorf("flags = %v, want --json and --limit", flags)
	}
}

// __complete answers with a leaf's ValidArgs when it has no subcommands, and
// says nothing about which kind of name it returned. `forge dies list` offered
// its three categories and they parsed as three subcommands.
func TestArgumentCompletionsAreNotSubcommands(t *testing.T) {
	run := fakeRunner(map[string]string{
		"__complete ":      "list\tList them\n:4\nCompletion ended with directive: x",
		"__complete list ": "checks\nmaintenance\nonetime\n:4\nCompletion ended with directive: x",
		"--help":           "Usage:\n  demo [command]\n\nAvailable Commands:\n  list        List them\n",
		// The help of a leaf that takes an argument names no commands at all.
		"list --help": "Usage:\n  demo list [category] [flags]\n\nFlags:\n  -h, --help   help for list\n",
	})
	tool := extractWith("demo", "", run)

	var paths []string
	tool.Walk(func(n *Node) { paths = append(paths, strings.Join(n.Path, " ")) })
	if len(paths) != 1 || paths[0] != "list" {
		t.Errorf("paths = %v, want [list] — argument values were read as commands", paths)
	}
}

// A Rich "Arguments" panel is shaped exactly like a "Commands" panel. Reading
// one as subcommands recursed until a 12-command tool reported 1992 nodes.
func TestRichArgumentsPanelIsNotACommandList(t *testing.T) {
	root := "Usage: demo [OPTIONS] COMMAND\n" +
		"╭─ Commands ─────────╮\n" +
		"│ search   Search it │\n" +
		"╰────────────────────╯\n"
	search := "Usage: demo search [OPTIONS] QUERY\n" +
		"╭─ Arguments ────────╮\n" +
		"│ query    The query │\n" +
		"╰────────────────────╯\n" +
		"╭─ Options ──────────╮\n" +
		"│ --json   As JSON   │\n" +
		"╰────────────────────╯\n"

	run := fakeRunner(map[string]string{
		"--help":              root,
		"search --help":       search,
		"search query --help": search,
	})
	tool := extractWith("demo", "", run)
	if tool.Framework != FrameworkRich {
		t.Fatalf("framework = %q, want rich", tool.Framework)
	}
	var n int
	tool.Walk(func(*Node) { n++ })
	if n != 1 {
		t.Errorf("walked %d nodes, want 1 — an Arguments panel was read as commands", n)
	}
}

const sectionHelp = `
 demo

Commands
───────────────────────
  backup [--tag <name>]  Copy the paths into today's snapshot
  snapshots              List the snapshots
  sync push              Force push
  sync status            Show sync status
  install                Install everything
  install --check        Show what is missing

Options
──────────────────────
  -c, --config <name>  Config to use
`

func TestSectionRowsKeepArgsOutAndNestMultiWord(t *testing.T) {
	run := fakeRunner(map[string]string{"--help": sectionHelp})
	tool := extractWith("demo", "", run)
	if tool.Framework != FrameworkSection {
		t.Fatalf("framework = %q, want section", tool.Framework)
	}

	got := map[string]string{}
	tool.Walk(func(n *Node) { got[strings.Join(n.Path, " ")] = n.Short })

	if _, ok := got["backup"]; !ok {
		t.Error("backup missing — an argument spec after the name swallowed the row")
	}
	if _, ok := got["sync push"]; !ok {
		t.Errorf("sync push missing; got %v", keys(got))
	}
	if got["install"] != "Install everything" {
		t.Errorf("install short = %q, want the first row's text, not the --check row's", got["install"])
	}
	for path := range got {
		if strings.Contains(path, "--") {
			t.Errorf("%q parsed as a command; a flag row is not a subcommand", path)
		}
	}
}

// A tool with no per-command help answers `tool sub --help` with the root
// screen, which made every command list its siblings at every level.
func TestIdenticalChildHelpStopsTheWalk(t *testing.T) {
	root := "Usage: demo [command]\n\nAvailable Commands:\n  alpha   A\n  beta    B\n"
	run := fakeRunner(map[string]string{
		"__complete ":            "alpha\tA\nbeta\tB\n:4\nCompletion ended with directive: x",
		"__complete alpha ":      "alpha\tA\nbeta\tB\n:4\nCompletion ended with directive: x",
		"__complete beta ":       "alpha\tA\nbeta\tB\n:4\nCompletion ended with directive: x",
		"__complete alpha beta ": "alpha\tA\nbeta\tB\n:4\nCompletion ended with directive: x",
		"__complete beta alpha ": "alpha\tA\nbeta\tB\n:4\nCompletion ended with directive: x",
		"--help":                 root,
		"alpha --help":           root,
		"beta --help":            root,
	})
	tool := extractWith("demo", "", run)
	var n int
	tool.Walk(func(*Node) { n++ })
	if n != 2 {
		t.Errorf("walked %d nodes, want 2 — root help echoed as each child's help", n)
	}
}

func TestFlatToolHasNoChildren(t *testing.T) {
	run := fakeRunner(map[string]string{"--help": "Usage: demo [OPTIONS]\n\nOptions:\n  --clean  Clean\n"})
	tool := extractWith("demo", "", run)
	if tool.Framework != FrameworkFlat {
		t.Errorf("framework = %q, want flat", tool.Framework)
	}
	var n int
	tool.Walk(func(*Node) { n++ })
	if n != 0 {
		t.Errorf("walked %d nodes, want 0", n)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
