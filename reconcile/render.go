package reconcile

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

var (
	statusColors = map[Status]*color.Color{
		Converged: color.New(color.FgHiGreen),
		Drift:     color.New(color.FgHiYellow),
		Issue:     color.New(color.FgHiRed),
	}

	added   = color.New(color.FgHiGreen)
	removed = color.New(color.FgHiRed)
	bold    = color.New(color.Bold)
)

// writeLine renders one row, discarding the write error.
//
// Stated once here rather than at each call site: these are console rows, and
// there is nothing a caller can do about a failed write to a terminal that has
// already gone away. The one write whose error is returned is EmitJSON's, where
// a truncated document is a caller's problem.
func writeLine(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

// statusWidth is the column a row's status is padded to. Outcome and patch
// lines indent by one more, so they start under the row's label.
const statusWidth = 9

// outcomeWidth fits the longest outcome status, "refused".
const outcomeWidth = 7

var itemIndent = strings.Repeat(" ", statusWidth+1)

// itemWidth pads items so their detail starts in the row's detail column, given
// the label width the rows use. An item too long for that pushes its own lines.
func itemWidth(labelWidth int, items []string) int {
	width := labelWidth - outcomeWidth - 1
	for _, item := range items {
		width = max(width, len(item))
	}
	return width
}

// RenderChanges writes one row's findings beneath it to out, each starting in
// the row's text column, unless the row's text is already its one finding.
// Each finding's fix follows it.
// Patches go to errw: they are the evidence for a change, and --json is what a
// caller parses.
func RenderChanges(out, errw io.Writer, r Result, labelWidth int) {
	indent := strings.Repeat(" ", statusWidth+1+labelWidth+1)
	beneath := !rowIsItsChange(r.Changes)
	for _, c := range r.Changes {
		if beneath && c.Detail != "" {
			writeLine(out, "%s%s\n", indent, c.Detail)
		}
		if c.Advice != "" {
			writeLine(out, "%sfix: %s\n", indent, c.Advice)
		}
		if c.Patch != "" {
			renderPatch(errw, c.Patch)
		}
	}
}

// renderPatch writes a unified diff indented under its change.
//
// Printed in full rather than summarized as a line count. A template edit fans
// out to every Python repo at once, and reading that change across the
// portfolio before applying it is what the plan verb is for.
func renderPatch(w io.Writer, patch string) {
	indent := itemIndent + "  "
	for _, line := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			writeLine(w, "%s%s\n", indent, added.Sprint(line))
		case strings.HasPrefix(line, "-"):
			writeLine(w, "%s%s\n", indent, removed.Sprint(line))
		default:
			writeLine(w, "%s%s\n", indent, line)
		}
	}
}

// Label is the handle a row is addressed by: the repo and the die that looked
// at it, which is also what a reader retypes to narrow to that one row.
func (r Result) Label() string { return r.Repo + "/" + r.Die }

// LabelWidth is the column the labels of a walk over these targets and dies fit
// in.
//
// Measured rather than fixed, from the rows the walk will render. Registry
// names vary by more than an order of magnitude in length, so any constant wide
// enough for the longest is mostly padding, and any narrower one breaks the
// alignment exactly on the rows that are hardest to read. Taken from the names
// rather than the results, so the first row can print before the last is
// measured.
func LabelWidth(targets []Target, dies []Die) int {
	repo, die := 0, 0
	for _, t := range targets {
		repo = max(repo, len(t.Repo.Name))
	}
	for _, d := range dies {
		die = max(die, len(d.Name()))
	}
	return repo + len("/") + die
}

// RenderResult writes one repo's row for one die.
func RenderResult(w io.Writer, r Result, width int) {
	paint := statusColors[r.Status]
	if paint == nil {
		paint = color.New(color.FgWhite)
	}
	writeLine(w, "%s %s %s\n",
		paint.Sprintf("%-*s", statusWidth, r.Status), bold.Sprintf("%-*s", width, r.Label()), r.Detail)
}

// RenderOutcomes writes what apply did with one row's changes, beneath it, at
// the row's label width.
func RenderOutcomes(w io.Writer, outcomes []Outcome, labelWidth int) {
	items := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		items = append(items, o.Change.Item)
	}
	width := itemWidth(labelWidth, items)
	for _, o := range outcomes {
		paint := color.New(color.FgHiGreen)
		switch o.Status {
		case Failed:
			paint = color.New(color.FgHiRed)
		case Refused, Skipped:
			paint = color.New(color.FgHiYellow)
		case Done:
		}

		// A Detail names its own item, so it takes the item's column blank.
		item, message := o.Change.Item, o.Message
		if message == "" {
			item, message = "", o.Change.Detail
		}
		writeLine(w, "%s%s %-*s %s\n", itemIndent, paint.Sprintf("%-*s", outcomeWidth, o.Status), width, item, message)
	}
}

// RenderSummary writes the count of each status across every result, after the
// coverage the caller names: how many dies on how many targets.
func RenderSummary(w io.Writer, results []Result, coverage string) {
	counts := map[Status]int{}
	for _, r := range results {
		counts[r.Status]++
	}

	var parts []string
	for _, status := range []Status{Converged, Drift, Issue} {
		if counts[status] > 0 {
			parts = append(parts, statusColors[status].Sprintf("%d %s", counts[status], status))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing measured")
	}
	writeLine(w, "\n  %s:  %s\n", coverage, strings.Join(parts, "  │  "))
}

// EmitJSON writes the machine-readable document, and nothing else goes to that
// stream — one stray diagnostic on stdout turns a caller's parse into a syntax
// error rather than the warning it actually was.
func EmitJSON(w io.Writer, results []Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if results == nil {
		results = []Result{}
	}
	return encoder.Encode(results)
}
