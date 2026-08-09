package cliaudit

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Job is one thing a CLI does, with the fleet default first and the other
// spellings seen alongside it. The point of the report is the distribution:
// one tool differing from ten is drift, a near-even split means the fleet found
// a distinction ~/dev/standards/cli-design.md has not named yet.
type Job struct {
	Name     string
	Default  string
	Variants []string
}

// jobs mirrors cli-design.md § Grammar. `add`, `remove` and `done` are listed
// as variants rather than as wrong: the standard keeps all three as distinct
// jobs, so what matters here is which tools carry which, not a verdict.
var jobs = []Job{
	{"show one", "show", []string{"view", "info", "cat", "read"}},
	{"list set", "list", []string{"ls", "dir"}},
	{"create", "create", []string{"add", "new", "define"}},
	{"delete", "delete", []string{"rm", "remove", "drop"}},
	{"mark done", "done", []string{"complete", "finish", "close"}},
	{"edit", "edit", []string{"update", "set", "modify"}},
}

// queryNames are reads named for the question they answer rather than for a
// resource, so they are not bare-noun findings. `status` and `stats` are the
// established spelling for a read everywhere, not nouns standing in for verbs.
var queryNames = map[string]bool{
	"status": true, "stats": true, "next": true, "now": true, "due": true,
	"todo": true, "overview": true, "brief": true, "report": true, "search": true,
	"dashboard": true, "blocked": true, "current": true, "random": true, "pick": true,
	"doctor": true, "config": true, "version": true, "update": true, "auth": true,
	"login": true, "logout": true, "token": true, "reference": true, "demo": true,
	"ui": true, "browse": true, "init": true, "run": true, "exec": true, "check": true,
	"apply": true, "sync": true, "undo": true, "read": true, "play": true, "export": true,
	"import": true, "scan": true, "index": true, "shell-init": true, "path": true,
	"example": true, "validate": true, "generate": true, "mkdir": true,
}

// kindNote says once what each group means, so the explanation is not repeated
// on every row of a thirty-row list.
var kindNote = map[string]string{
	"bare-noun":       "a noun that acts. Namespace it, or record why it will never grow a second verb",
	"hyphen-compound": "a namespace wearing a hyphen; the arrow is the shape it wants",
	"unreadable":      "the tree is not reachable from the root help screen",
}

// VerbUse is one tool's spellings for one job.
type VerbUse struct {
	Tool     string   `json:"tool"`
	Job      string   `json:"job"`
	Spelling []string `json:"spelling"`
}

// Finding is a variation worth a look. It is never a failure — the caller
// decides whether the context earns it.
type Finding struct {
	Kind    string `json:"kind"`
	Tool    string `json:"tool"`
	Command string `json:"command"`
	Detail  string `json:"detail"`
}

// Report is the whole analysis.
type Report struct {
	Tools      []string             `json:"tools"`
	Unresolved []Unresolved         `json:"unresolved,omitempty"`
	NodeCount  int                  `json:"node_count"`
	Verbs      map[string][]VerbUse `json:"verbs"`
	Findings   []Finding            `json:"findings"`
}

// Analyze applies the cli-design.md grammar sections to the extracted trees.
func Analyze(tools []*Tool, unresolved []Unresolved) *Report {
	rep := &Report{Verbs: map[string][]VerbUse{}, Unresolved: unresolved}
	seen := map[string]map[string]map[string]bool{} // job -> tool -> spelling

	for _, t := range tools {
		rep.Tools = append(rep.Tools, t.Binary)
		t.Walk(func(n *Node) {
			rep.NodeCount++
			recordVerb(seen, t.Binary, n)
			rep.Findings = append(rep.Findings, inspect(t, n)...)
		})
		if t.Root != nil && t.Root.Leaf() && t.Framework != FrameworkFlat {
			rep.Findings = append(rep.Findings, Finding{
				Kind: "unreadable", Tool: t.Binary,
				Detail: "help lists no commands, so the tree cannot be reached from the root screen",
			})
		}
	}

	for job, byTool := range seen {
		for tool, spellings := range byTool {
			var s []string
			for sp := range spellings {
				s = append(s, sp)
			}
			sort.Strings(s)
			rep.Verbs[job] = append(rep.Verbs[job], VerbUse{Tool: tool, Job: job, Spelling: s})
		}
		sort.Slice(rep.Verbs[job], func(i, j int) bool { return rep.Verbs[job][i].Tool < rep.Verbs[job][j].Tool })
	}
	sort.Slice(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].Kind != rep.Findings[j].Kind {
			return rep.Findings[i].Kind < rep.Findings[j].Kind
		}
		if rep.Findings[i].Tool != rep.Findings[j].Tool {
			return rep.Findings[i].Tool < rep.Findings[j].Tool
		}
		return rep.Findings[i].Command < rep.Findings[j].Command
	})
	return rep
}

func recordVerb(seen map[string]map[string]map[string]bool, tool string, n *Node) {
	for _, j := range jobs {
		match := n.Name == j.Default
		for _, v := range j.Variants {
			if n.Name == v {
				match = true
			}
		}
		if !match {
			continue
		}
		// A root-level `update` is self-update, a different job from editing a
		// record, and counting it as an edit spelling makes every tool look
		// like it carries two words for one thing.
		if n.Name == "update" && n.Depth() == 1 {
			continue
		}
		if seen[j.Name] == nil {
			seen[j.Name] = map[string]map[string]bool{}
		}
		if seen[j.Name][tool] == nil {
			seen[j.Name][tool] = map[string]bool{}
		}
		seen[j.Name][tool][n.Name] = true
	}
}

// inspect applies the shape rules to one node.
func inspect(t *Tool, n *Node) []Finding {
	var out []Finding
	cmd := strings.Join(n.Path, " ")

	// cli-design.md § "The write verbs come in pairs" — a hyphenated verb-noun
	// is a namespace wearing a hyphen. Checked first: a compound is already
	// reported here, and reporting it again as a bare noun says the same thing
	// twice with a worse fix attached.
	if v, rest, ok := splitCompound(n.Name); ok {
		parent := strings.Join(n.Path[:len(n.Path)-1], " ")
		out = append(out, Finding{
			Kind: "hyphen-compound", Tool: t.Binary, Command: cmd,
			Detail: strings.TrimSpace(fmt.Sprintf("%s %s %s", parent, rest, v)),
		})
		return out
	}

	// cli-design.md § "A resource that could ever grow a second command is a
	// namespace today" — a bare noun that acts.
	if n.Leaf() && looksLikeNoun(n.Name) && !queryNames[n.Name] {
		out = append(out, Finding{Kind: "bare-noun", Tool: t.Binary, Command: cmd})
	}
	return out
}

var verbWords = map[string]bool{
	"add": true, "remove": true, "create": true, "delete": true, "update": true,
	"edit": true, "set": true, "list": true, "show": true, "reorder": true,
	"move": true, "search": true, "complete": true, "clear": true, "get": true,
	"put": true, "copy": true, "rm": true, "view": true, "swap": true, "promote": true,
}

func isVerb(s string) bool { return verbWords[s] }

// splitCompound reports a `verb-noun` name, which the standard reads as a
// namespace that was never created.
func splitCompound(name string) (verb, noun string, ok bool) {
	v, rest, found := strings.Cut(name, "-")
	if !found || !verbWords[v] || rest == "" {
		return "", "", false
	}
	return v, rest, true
}

// looksLikeNoun is deliberately crude — a plural, or a known set name. It only
// generates candidates; the report says "worth a look", never "wrong".
func looksLikeNoun(s string) bool {
	if isVerb(s) {
		return false
	}
	return strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss")
}

// WriteSpec renders the extracted trees for a terminal.
func WriteSpec(out io.Writer, tools []*Tool) error {
	var b strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&b, "%s (%s)\n", t.Binary, t.Framework)
		t.Walk(func(n *Node) {
			fmt.Fprintf(&b, "  %s%-26s %s\n", strings.Repeat("  ", n.Depth()-1), n.Name, n.Short)
		})
		b.WriteString("\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// WriteText renders the report for a terminal.
//
// It builds the whole page in memory first. A strings.Builder cannot fail, so
// the formatting reads without an error check on every line and there is one
// real write to check at the end.
func WriteText(out io.Writer, rep *Report) error {
	var b strings.Builder
	w := &b
	fmt.Fprintf(w, "%d tools, %d commands\n\n", len(rep.Tools), rep.NodeCount)

	fmt.Fprintln(w, "VERB SPELLINGS")
	for _, j := range jobs {
		uses := rep.Verbs[j.Name]
		if len(uses) == 0 {
			continue
		}
		counts := map[string][]string{}
		for _, u := range uses {
			for _, sp := range u.Spelling {
				counts[sp] = append(counts[sp], u.Tool)
			}
		}
		var spellings []string
		for sp := range counts {
			spellings = append(spellings, sp)
		}
		sort.Slice(spellings, func(a, b int) bool {
			if len(counts[spellings[a]]) != len(counts[spellings[b]]) {
				return len(counts[spellings[a]]) > len(counts[spellings[b]])
			}
			return spellings[a] < spellings[b]
		})
		var parts []string
		for _, sp := range spellings {
			tools := counts[sp]
			if len(tools) <= 2 {
				parts = append(parts, fmt.Sprintf("%s x%d %s", sp, len(tools), strings.Join(tools, ",")))
			} else {
				parts = append(parts, fmt.Sprintf("%s x%d", sp, len(tools)))
			}
		}
		fmt.Fprintf(w, "  %-11s %s\n", j.Name, strings.Join(parts, " | "))
	}

	byKind := map[string][]Finding{}
	for _, f := range rep.Findings {
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}
	var kinds []string
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(w, "\n%s (%d) — %s\n", strings.ToUpper(k), len(byKind[k]), kindNote[k])
		for _, f := range byKind[k] {
			if f.Detail == "" {
				fmt.Fprintf(w, "  %-9s %s\n", f.Tool, f.Command)
				continue
			}
			fmt.Fprintf(w, "  %-9s %-34s -> %s\n", f.Tool, f.Command, f.Detail)
		}
	}

	if len(rep.Unresolved) > 0 {
		fmt.Fprintf(w, "\nNO BINARY ON PATH (%d)\n", len(rep.Unresolved))
		for _, u := range rep.Unresolved {
			fmt.Fprintf(w, "  %-14s tried: %s\n", u.Repo, strings.Join(u.Tried, ", "))
		}
	}

	_, err := io.WriteString(out, b.String())
	return err
}
