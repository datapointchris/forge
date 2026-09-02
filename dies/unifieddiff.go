package dies

import (
	"fmt"
	"strings"
)

// diffContext is how many identical lines surround each change, matching
// `diff -u`'s default so a patch read here reads the same as one read anywhere.
const diffContext = 3

// unifiedDiff renders the change to a file the way `diff -u` would.
//
// Hand-rolled rather than shelled out to diff: the content is already in
// memory, and writing two temp files per file per repo to read a diff back is
// a lot of syscalls for a plan that visits fifty repos.
//
// Changes far enough apart come out as separate hunks. A generated config is
// mostly identical between versions, and the toolchain stamp on line 1 moves
// on every bump, so anything that spans the first change to the last is the
// whole file on every rollout — which is what makes a plan unreadable.
func unifiedDiff(name, have, want string) string {
	if have == want {
		return ""
	}

	ops := lineOps(splitKeepingEmpty(have), splitKeepingEmpty(want))
	hunks := groupHunks(ops)
	if len(hunks) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", name, name)
	for _, h := range hunks {
		h.render(&out, ops)
	}
	return out.String()
}

func splitKeepingEmpty(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// opKind is what one line of an edit script does to the file.
type opKind byte

const (
	opEqual  opKind = ' '
	opDelete opKind = '-'
	opInsert opKind = '+'
)

// lineOp is one line of the edit script turning have into want.
type lineOp struct {
	kind opKind
	text string
}

func (o lineOp) inFrom() bool { return o.kind != opInsert }
func (o lineOp) inTo() bool   { return o.kind != opDelete }

// lineOps is the edit script turning from into to, in file order.
//
// The identical head and tail are matched off before the alignment below sees
// them. That is a shortcut and not the answer — everything between them still
// goes through the table — and it is what keeps a one-line edit in a five
// hundred line workflow off the quadratic path entirely.
func lineOps(from, to []string) []lineOp {
	head := 0
	for head < len(from) && head < len(to) && from[head] == to[head] {
		head++
	}

	tail := 0
	for tail < len(from)-head && tail < len(to)-head &&
		from[len(from)-1-tail] == to[len(to)-1-tail] {
		tail++
	}

	ops := make([]lineOp, 0, len(from)+len(to))
	for _, line := range from[:head] {
		ops = append(ops, lineOp{opEqual, line})
	}
	ops = append(ops, align(from[head:len(from)-tail], to[head:len(to)-tail])...)
	for _, line := range from[len(from)-tail:] {
		ops = append(ops, lineOp{opEqual, line})
	}
	return ops
}

// align pairs two line runs on their longest common subsequence.
//
// One table cell per pair of lines, which the files this renders keep cheap:
// the largest generated config in the portfolio is under six hundred lines,
// and the caller has already taken the matching head and tail off both sides.
//
// Ties go to the deletion, which is what puts a replacement's old block ahead
// of its new one rather than interleaving the two.
func align(from, to []string) []lineOp {
	// common[i][j] is the length of the longest common subsequence of from[i:]
	// and to[j:]. Indexed from the end so the walk below can start at [0][0]
	// and always step the way that keeps the most lines paired.
	common := make([][]int, len(from)+1)
	for i := range common {
		common[i] = make([]int, len(to)+1)
	}
	for i := len(from) - 1; i >= 0; i-- {
		for j := len(to) - 1; j >= 0; j-- {
			if from[i] == to[j] {
				common[i][j] = common[i+1][j+1] + 1
				continue
			}
			common[i][j] = max(common[i+1][j], common[i][j+1])
		}
	}

	ops := make([]lineOp, 0, len(from)+len(to))
	i, j := 0, 0
	for i < len(from) && j < len(to) {
		switch {
		case from[i] == to[j]:
			ops = append(ops, lineOp{opEqual, from[i]})
			i, j = i+1, j+1
		case common[i+1][j] >= common[i][j+1]:
			ops = append(ops, lineOp{opDelete, from[i]})
			i++
		default:
			ops = append(ops, lineOp{opInsert, to[j]})
			j++
		}
	}
	for ; i < len(from); i++ {
		ops = append(ops, lineOp{opDelete, from[i]})
	}
	for ; j < len(to); j++ {
		ops = append(ops, lineOp{opInsert, to[j]})
	}
	return ops
}

// hunk is the span of ops printed under one @@ header, with the line each side
// starts at and how many of that side's lines it covers.
type hunk struct {
	start, end int
	fromStart  int
	fromCount  int
	toStart    int
	toCount    int
}

// groupHunks gathers each change with its context, joining two changes whose
// context runs together.
//
// The join threshold is 2*diffContext because that is where splitting stops
// paying: the two hunks would print the same identical lines between them and
// buy a second header with it.
func groupHunks(ops []lineOp) []hunk {
	fromBefore := make([]int, len(ops)+1)
	toBefore := make([]int, len(ops)+1)
	for k, op := range ops {
		fromBefore[k+1], toBefore[k+1] = fromBefore[k], toBefore[k]
		if op.inFrom() {
			fromBefore[k+1]++
		}
		if op.inTo() {
			toBefore[k+1]++
		}
	}

	var hunks []hunk
	for k := 0; k < len(ops); k++ {
		if ops[k].kind == opEqual {
			continue
		}

		start := max(k-diffContext, 0)
		end := k + 1
		for {
			next := end
			for next < len(ops) && next-end < 2*diffContext && ops[next].kind == opEqual {
				next++
			}
			if next >= len(ops) || ops[next].kind == opEqual {
				break
			}
			end = next + 1
		}
		end = min(end+diffContext, len(ops))

		hunks = append(hunks, hunk{
			start:     start,
			end:       end,
			fromStart: hunkStart(fromBefore[start], fromBefore[end]-fromBefore[start]),
			fromCount: fromBefore[end] - fromBefore[start],
			toStart:   hunkStart(toBefore[start], toBefore[end]-toBefore[start]),
			toCount:   toBefore[end] - toBefore[start],
		})
		k = end - 1
	}
	return hunks
}

// hunkStart is the 1-based line one side of a hunk begins at, or the line it
// sits after when that side contributes nothing — which is how `diff -u`
// spells a created file's removed side as `-0,0`.
func hunkStart(before, count int) int {
	if count == 0 {
		return before
	}
	return before + 1
}

func (h hunk) render(out *strings.Builder, ops []lineOp) {
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", h.fromStart, h.fromCount, h.toStart, h.toCount)

	for k := h.start; k < h.end; {
		if ops[k].kind == opEqual {
			fmt.Fprintf(out, " %s\n", ops[k].text)
			k++
			continue
		}

		// A run of changed lines prints every removal before any addition, so
		// a replaced block reads as the old text then the new text.
		run := k
		for run < h.end && ops[run].kind != opEqual {
			run++
		}
		for _, op := range ops[k:run] {
			if op.kind == opDelete {
				fmt.Fprintf(out, "-%s\n", op.text)
			}
		}
		for _, op := range ops[k:run] {
			if op.kind == opInsert {
				fmt.Fprintf(out, "+%s\n", op.text)
			}
		}
		k = run
	}
}
