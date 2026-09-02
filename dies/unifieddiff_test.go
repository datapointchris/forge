package dies

import (
	"fmt"
	"strings"
	"testing"
)

// hunkHeaders is every @@ line in a patch, in order.
func hunkHeaders(patch string) []string {
	var headers []string
	for _, line := range patchBody(patch) {
		if strings.HasPrefix(line, "@@") {
			headers = append(headers, line)
		}
	}
	return headers
}

// patchBody is a patch's lines with the two file headers dropped, so a test
// reading line prefixes cannot mistake `--- a/x` for a removal.
func patchBody(patch string) []string {
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	if len(lines) < 2 {
		return nil
	}
	return lines[2:]
}

// numbered is n distinct lines, none of them a substring of another, so a test
// asserting a line was not printed cannot be satisfied by a near miss.
func numbered(prefix string, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s%03d", prefix, i)
	}
	return lines
}

func joined(lines ...[]string) string {
	var out strings.Builder
	for _, group := range lines {
		for _, line := range group {
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

func TestUnifiedDiffShowsOnlyTheChangedHunk(t *testing.T) {
	have := strings.Repeat("same\n", 50) + "old\n" + strings.Repeat("same\n", 50)
	want := strings.Repeat("same\n", 50) + "new\n" + strings.Repeat("same\n", 50)

	patch := unifiedDiff(".pre-commit-config.yaml", have, want)

	if !strings.Contains(patch, "-old") || !strings.Contains(patch, "+new") {
		t.Errorf("the change is missing from the diff:\n%s", patch)
	}
	if lineCount := strings.Count(patch, "\n"); lineCount > 12 {
		t.Errorf("the diff printed %d lines for a one-line change:\n%s", lineCount, patch)
	}
}

func TestUnifiedDiffKeepsOneContiguousChangeInOneHunk(t *testing.T) {
	middle := numbered("m", 30)
	have := joined(middle[:10], []string{"old-a", "old-b", "old-c"}, middle[10:])
	want := joined(middle[:10], []string{"new-a", "new-b", "new-c"}, middle[10:])

	patch := unifiedDiff("validate.yml", have, want)

	if headers := hunkHeaders(patch); len(headers) != 1 {
		t.Errorf("hunks = %v, want one for a contiguous change:\n%s", headers, patch)
	}
	for _, line := range []string{"-old-a", "-old-b", "-old-c", "+new-a", "+new-b", "+new-c"} {
		if !strings.Contains(patch, line) {
			t.Errorf("%q is missing from the diff:\n%s", line, patch)
		}
	}
}

func TestUnifiedDiffSplitsSeparatedChangesIntoTheirOwnHunks(t *testing.T) {
	middle := numbered("m", 50)
	have := joined([]string{"head-old"}, middle, []string{"foot-old"})
	want := joined([]string{"head-new"}, middle, []string{"foot-new"})

	patch := unifiedDiff("validate.yml", have, want)

	if headers := hunkHeaders(patch); len(headers) != 2 {
		t.Errorf("hunks = %v, want two for two separated changes:\n%s", headers, patch)
	}
	for _, line := range middle[10:40] {
		if strings.Contains(patch, line) {
			t.Errorf("unchanged line %q was printed:\n%s", line, patch)
		}
	}
}

func TestUnifiedDiffSeparatesAFirstLineChangeFromALastLineChange(t *testing.T) {
	// The live shape: the toolchain stamp is line 1 of every generated file, so
	// any bump that also changes something else spans the whole file.
	middle := numbered("m", 20)
	have := joined([]string{"# forge-toolchain: 19"}, middle, []string{"      - run: uv sync"})
	want := joined([]string{"# forge-toolchain: 20"}, middle, []string{"      - run: uv sync --dev"})

	patch := unifiedDiff("validate.yml", have, want)

	headers := hunkHeaders(patch)
	if len(headers) != 2 {
		t.Fatalf("hunks = %v, want two:\n%s", headers, patch)
	}
	if headers[0] != "@@ -1,4 +1,4 @@" {
		t.Errorf("first header = %q, want @@ -1,4 +1,4 @@", headers[0])
	}
	if headers[1] != "@@ -19,4 +19,4 @@" {
		t.Errorf("second header = %q, want @@ -19,4 +19,4 @@", headers[1])
	}
}

func TestUnifiedDiffHeadersCountFromTheRealLineNumbers(t *testing.T) {
	have := "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\ngolf\nhotel\nindia\njuliet\n"
	want := "alpha\nbravo\ncharlie\ndelta\nECHO\nfoxtrot\ngolf\nhotel\nindia\njuliet\n"

	patch := unifiedDiff("validate.yml", have, want)

	headers := hunkHeaders(patch)
	if len(headers) != 1 || headers[0] != "@@ -2,7 +2,7 @@" {
		t.Errorf("headers = %v, want one reading @@ -2,7 +2,7 @@:\n%s", headers, patch)
	}
}

func TestUnifiedDiffIsEmptyWhenTheFileMatches(t *testing.T) {
	same := joined(numbered("m", 40))

	if patch := unifiedDiff("validate.yml", same, same); patch != "" {
		t.Errorf("patch = %q, want empty for identical content", patch)
	}
}

func TestUnifiedDiffRendersACreatedFileAsOneAddition(t *testing.T) {
	want := "alpha\nbravo\ncharlie\n"

	patch := unifiedDiff("validate.yml", "", want)

	headers := hunkHeaders(patch)
	if len(headers) != 1 || headers[0] != "@@ -0,0 +1,3 @@" {
		t.Fatalf("headers = %v, want one reading @@ -0,0 +1,3 @@:\n%s", headers, patch)
	}
	for _, line := range patchBody(patch) {
		if strings.HasPrefix(line, "-") {
			t.Errorf("a created file printed a removal:\n%s", patch)
		}
	}
}

func TestUnifiedDiffRendersARetractedFileAsOneDeletion(t *testing.T) {
	have := "alpha\nbravo\ncharlie\n"

	patch := unifiedDiff("validate.yml", have, "")

	headers := hunkHeaders(patch)
	if len(headers) != 1 || headers[0] != "@@ -1,3 +0,0 @@" {
		t.Fatalf("headers = %v, want one reading @@ -1,3 +0,0 @@:\n%s", headers, patch)
	}
	for _, line := range patchBody(patch) {
		if strings.HasPrefix(line, "+") {
			t.Errorf("a retracted file printed an addition:\n%s", patch)
		}
	}
}

func TestUnifiedDiffPrintsRemovalsBeforeAdditionsInAChange(t *testing.T) {
	middle := numbered("m", 20)
	have := joined(middle[:10], []string{"old-a", "old-b"}, middle[10:])
	want := joined(middle[:10], []string{"new-a", "new-b"}, middle[10:])

	patch := unifiedDiff("validate.yml", have, want)

	changed, added := 0, false
	for _, line := range patchBody(patch) {
		switch {
		case strings.HasPrefix(line, "+"):
			added = true
			changed++
		case strings.HasPrefix(line, "-"):
			if added {
				t.Fatalf("a removal printed after an addition:\n%s", patch)
			}
			changed++
		}
	}
	if changed != 4 {
		t.Errorf("changed lines = %d, want 4:\n%s", changed, patch)
	}
}
