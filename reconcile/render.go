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

	verdictColors = map[Verdict]*color.Color{
		Matched:    color.New(color.FgHiGreen),
		Missing:    color.New(color.FgHiYellow),
		Stale:      color.New(color.FgHiYellow),
		Undeclared: color.New(color.FgHiBlue),
		Unknown:    color.New(color.FgHiMagenta),
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

// RenderChange writes one item's row.
//
// These go to stderr: below a repo's summary they are the evidence for the row
// that follows, not the answer a caller parses — which is --json.
func RenderChange(w io.Writer, c Change) {
	paint := verdictColors[c.Verdict]
	if paint == nil {
		paint = color.New(color.FgWhite)
	}

	observed := ""
	if c.Observed != "" {
		observed = fmt.Sprintf(" (is %q)", c.Observed)
	}

	writeLine(w, "  %s %-32s %s%s\n", paint.Sprintf("%-11s", c.Verdict), c.Item, c.Detail, observed)

	if c.Patch != "" {
		renderPatch(w, c.Patch)
	}
}

// renderPatch writes a unified diff indented under its change.
//
// Printed in full rather than summarized as a line count. A template edit fans
// out to every Python repo at once, and reading that change across the
// portfolio before applying it is what the plan verb is for.
func renderPatch(w io.Writer, patch string) {
	for _, line := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			writeLine(w, "      %s\n", added.Sprint(line))
		case strings.HasPrefix(line, "-"):
			writeLine(w, "      %s\n", removed.Sprint(line))
		default:
			writeLine(w, "      %s\n", line)
		}
	}
}

// RenderResult writes one repo's row for one die.
func RenderResult(w io.Writer, r Result) {
	paint := statusColors[r.Status]
	if paint == nil {
		paint = color.New(color.FgWhite)
	}
	writeLine(w, "%s %s %s\n",
		paint.Sprintf("%-9s", r.Status), bold.Sprintf("%-24s", r.Repo+"/"+r.Die), r.Detail)
}

// RenderOutcome writes what apply did with one change.
func RenderOutcome(w io.Writer, o Outcome) {
	paint := color.New(color.FgHiGreen)
	switch o.Status {
	case Failed:
		paint = color.New(color.FgHiRed)
	case Refused, Skipped:
		paint = color.New(color.FgHiYellow)
	case Done:
	}

	message := o.Message
	if message == "" {
		message = o.Change.Detail
	}
	writeLine(w, "  %s %-32s %s\n", paint.Sprintf("%-11s", o.Status), o.Change.Item, message)
}

// RenderSummary writes the count of each status across every result.
func RenderSummary(w io.Writer, results []Result) {
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
	writeLine(w, "\n  %s\n", strings.Join(parts, "  │  "))
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
