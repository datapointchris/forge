// Package cliaudit reads the command surface of installed fleet CLIs and
// reports where their grammar varies.
//
// Everything here works from the outside: it runs `--help` and, for cobra
// tools, the hidden `__complete` callback. It never runs a bare subcommand,
// because the thing most worth finding is a noun that performs a read when
// invoked with no verb, and detecting it by running it would fire real reads
// against APIs and databases.
package cliaudit

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Framework is how a tool's tree has to be read. The distinction is not
// cosmetic: cobra ships a machine-readable command list, and everything else
// has to be scraped out of rendered help.
type Framework string

const (
	FrameworkCobra   Framework = "cobra"
	FrameworkRich    Framework = "rich"    // typer/click, box-drawn panels
	FrameworkSection Framework = "section" // pytermstyle, an underlined "Commands" heading
	FrameworkFlat    Framework = "flat"    // no discoverable subcommands
)

// maxDepth bounds the walk. Nothing on the fleet nests four deep, and a parser
// that mistakes an argument list for a command list would otherwise recurse
// until the process dies — which is exactly what a Rich "Arguments" panel did.
const maxDepth = 4

const helpTimeout = 20 * time.Second

// Node is one command in a tool's tree.
type Node struct {
	Path     []string `json:"path"`
	Name     string   `json:"name"`
	Short    string   `json:"short"`
	Usage    string   `json:"usage,omitempty"`
	Flags    []string `json:"flags,omitempty"`
	Children []*Node  `json:"children,omitempty"`

	// childIndex is scratch used while assembling a section-format tree, where
	// a parent is implied by its rows rather than listed on its own.
	childIndex map[string]*Node
}

// Leaf reports whether the node runs something rather than holding verbs.
func (n *Node) Leaf() bool { return len(n.Children) == 0 }

// Depth is how many words follow the binary name.
func (n *Node) Depth() int { return len(n.Path) }

// Tool is one binary's whole surface.
type Tool struct {
	Binary    string    `json:"binary"`
	Repo      string    `json:"repo,omitempty"`
	Framework Framework `json:"framework"`
	Root      *Node     `json:"root"`
}

// Walk calls fn for every node below the root, parents before children.
func (t *Tool) Walk(fn func(*Node)) {
	if t.Root == nil {
		return
	}
	var rec func(*Node)
	rec = func(n *Node) {
		if n.Depth() > 0 {
			fn(n)
		}
		for _, c := range n.Children {
			rec(c)
		}
	}
	rec(t.Root)
}

var (
	usageRe     = regexp.MustCompile(`(?m)^\s*Usage:\s+(.*)$`)
	flagRe      = regexp.MustCompile(`(--[a-z][a-z0-9-]*)`)
	panelOpenRe = regexp.MustCompile(`^╭─+\s*(.*?)\s*─+╮`)
	panelRowRe  = regexp.MustCompile(`^[│|]\s+([a-z][a-z0-9:_-]*)\s{2,}(.*?)\s*[│|]\s*$`)
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

// skip are the commands every framework generates, which say nothing about the
// tool's own grammar.
var skip = map[string]bool{"help": true, "completion": true, "__complete": true}

// notCommandPanel are Rich panel titles that hold something other than
// subcommands. Reading an "Arguments" panel as a command list is what turned a
// 12-command tool into a 1992-node walk.
var notCommandPanel = []string{"option", "argument", "usage", "flag"}

type runner func(bin string, args ...string) string

func execRunner(bin string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), helpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(cmd.Environ(), "COLUMNS=200", "NO_COLOR=1", "TERM=dumb")
	out, _ := cmd.CombinedOutput() // a non-zero exit still prints usable help
	return ansiRe.ReplaceAllString(string(out), "")
}

// Extract reads one binary's whole command tree.
func Extract(binary, repo string) *Tool {
	return extractWith(binary, repo, execRunner)
}

func extractWith(binary, repo string, run runner) *Tool {
	fw := detectFramework(binary, run)
	t := &Tool{Binary: binary, Repo: repo, Framework: fw}
	t.Root = build(binary, nil, "", fw, run, 0, "")
	return t
}

func detectFramework(binary string, run runner) Framework {
	if cobraChildren(binary, nil, run) != nil {
		return FrameworkCobra
	}
	help := run(binary, "--help")
	if strings.Contains(help, "╭─") {
		return FrameworkRich
	}
	if len(sectionChildren(binary, help)) > 0 {
		return FrameworkSection
	}
	return FrameworkFlat
}

func build(binary string, path []string, short string, fw Framework, run runner, depth int, parentHelp string) *Node {
	args := append(append([]string{}, path...), "--help")
	help := run(binary, args...)

	n := &Node{Path: append([]string{}, path...), Short: short, Flags: uniqueFlags(help)}
	if len(path) > 0 {
		n.Name = path[len(path)-1]
	} else {
		n.Name = binary
	}
	if m := usageRe.FindStringSubmatch(help); m != nil {
		n.Usage = strings.TrimSpace(m[1])
	}
	// A tool with no per-command help answers `tool sub --help` with the root
	// screen. Reading that as sub's own children makes every command list its
	// siblings, at every level, forever — safekeep does exactly this.
	if depth >= maxDepth || (parentHelp != "" && help == parentHelp) {
		return n
	}

	// The section format prints its whole tree on the root screen and has no
	// per-command help, so it is assembled in one pass instead of walked.
	if fw == FrameworkSection {
		if len(path) == 0 {
			n.Children = sectionTree(binary, help)
		}
		return n
	}

	var kids []child
	switch fw {
	case FrameworkCobra:
		kids = cobraChildren(binary, path, run)
	case FrameworkRich:
		kids = richChildren(help)
	case FrameworkSection, FrameworkFlat:
		kids = nil
	}
	for _, k := range kids {
		if k.name == n.Name {
			continue // a row echoing its own parent is a parse artifact, not a command
		}
		n.Children = append(n.Children, build(binary, append(append([]string{}, path...), k.name), k.desc, fw, run, depth+1, help))
	}
	return n
}

type child struct{ name, desc string }

// cobraChildren asks cobra's own completion callback. Returns nil when the tool
// is not cobra, which is also how the framework is detected.
func cobraChildren(binary string, path []string, run runner) []child {
	args := append([]string{"__complete"}, path...)
	args = append(args, "")
	out := run(binary, args...)
	if !strings.Contains(out, ":") || !strings.Contains(out, "Completion ended") {
		return nil
	}
	var kids []child
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "Completion") {
			continue
		}
		name, desc, _ := strings.Cut(line, "\t")
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "-") || skip[name] || !isCommandName(name) {
			continue
		}
		kids = append(kids, child{name, strings.TrimSpace(desc)})
	}
	if kids == nil {
		return []child{} // cobra answered, this node simply has no subcommands
	}
	return kids
}

func richChildren(help string) []child {
	var kids []child
	inPanel := false
	for _, line := range strings.Split(help, "\n") {
		if m := panelOpenRe.FindStringSubmatch(line); m != nil {
			inPanel = !titleIsNotCommands(m[1])
			continue
		}
		if strings.HasPrefix(line, "╰") {
			inPanel = false
			continue
		}
		if !inPanel {
			continue
		}
		if m := panelRowRe.FindStringSubmatch(line); len(m) == 3 && !skip[m[1]] {
			kids = append(kids, child{m[1], strings.TrimSpace(m[2])})
		}
	}
	return kids
}

// sectionTree assembles the whole tree from the root screen's rows. A row may
// name more than one word — font lists `sync setup`, `sync status`, `sync push`
// — so the leading command words become a path and the shared prefix becomes a
// parent node that the help never lists on its own.
func sectionTree(binary, help string) []*Node {
	var roots []*Node
	byName := map[string]*Node{}

	for _, c := range sectionChildren(binary, help) {
		words := strings.Fields(c.name)
		parent := &roots
		var path []string
		lookup := byName
		for i, w := range words {
			path = append(path, w)
			node, ok := lookup[w]
			if !ok {
				node = &Node{Path: append([]string{}, path...), Name: w}
				lookup[w] = node
				*parent = append(*parent, node)
			}
			if i == len(words)-1 {
				// First row wins. font lists `install` and `install --check`
				// as separate rows; both reduce to the same command, and the
				// second would otherwise overwrite the real description with
				// the flag's.
				if node.Short == "" {
					node.Short = c.desc
				}
			} else {
				if node.childIndex == nil {
					node.childIndex = map[string]*Node{}
				}
				lookup = node.childIndex
				parent = &node.Children
			}
		}
	}
	return roots
}

// sectionChildren reads the pytermstyle shape: a "Commands" heading, an
// underline rule, then indented rows. safekeep prefixes each row with the tool
// name where theme and font do not, so the binary name is stripped when present.
func sectionChildren(binary, help string) []child {
	var kids []child
	inSection := false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Commands" {
			inSection = true
			continue
		}
		if inSection && trimmed != "" && isRule(trimmed) {
			continue
		}
		if inSection && trimmed == "" {
			continue
		}
		if inSection && !strings.HasPrefix(line, " ") {
			inSection = false // a new unindented heading ends the block
			continue
		}
		if !inSection {
			continue
		}
		// Split on the two-space gutter rather than pattern-matching the row:
		// the left side is "name [args]" in every variant of this format, and
		// the arg spellings vary too much to enumerate ([--tag <name>],
		// <verb>, --to <path>).
		row := strings.TrimPrefix(strings.TrimLeft(line, " "), binary+" ")
		left, desc, found := strings.Cut(row, "  ")
		if !found {
			continue
		}
		// Keep every leading command word and stop at the first argument
		// placeholder, so `sync push` stays two words while
		// `backup [--tag <name>]` stays one.
		var words []string
		for _, w := range strings.Fields(left) {
			if !isCommandName(w) {
				break
			}
			words = append(words, w)
		}
		if len(words) == 0 || skip[words[0]] {
			continue
		}
		kids = append(kids, child{strings.Join(words, " "), strings.TrimSpace(desc)})
	}
	return kids
}

func isRule(s string) bool {
	for _, r := range s {
		if r != '─' && r != '-' && r != '━' && r != '=' {
			return false
		}
	}
	return len(s) > 0
}

func titleIsNotCommands(title string) bool {
	t := strings.ToLower(title)
	for _, w := range notCommandPanel {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

func isCommandName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false // a leading dash is a flag, not a command
	}
	for _, r := range s {
		lower := r >= 'a' && r <= 'z'
		digit := r >= '0' && r <= '9'
		punct := r == '-' || r == '_' || r == ':'
		if !lower && !digit && !punct {
			return false
		}
	}
	return true
}

func uniqueFlags(help string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range flagRe.FindAllStringSubmatch(help, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
