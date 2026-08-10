package dies

import (
	"os"
	"strconv"
	"strings"
)

// lineFile is a text file read as the lines it declares.
//
// Shared by the append-only dies, which is a real shared mechanism rather than
// incidental similarity: .gitignore and .markdownlintignore are both files
// whose contents are legitimately per-repo, so neither can be deployed whole
// and both assert that a baseline is PRESENT and leave everything else alone.
type lineFile struct {
	exists bool
	lines  []string
}

func readLineFile(path string) (lineFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return lineFile{}, nil
	}
	if err != nil {
		return lineFile{}, err
	}

	return lineFile{
		exists: true,
		lines:  strings.Split(strings.TrimRight(string(data), "\n"), "\n"),
	}, nil
}

// count reports how many times an entry appears as a whole line.
//
// Whole-line and literal, matching `grep -qxF`: without it `dist/` would match
// a repo's existing `somedist/thing`, and the `*` in `*.egg-info/` would be
// read as a pattern rather than the character it is.
func (f lineFile) count(entry string) int {
	var n int
	for _, line := range f.lines {
		if strings.TrimRight(line, "\r") == entry {
			n++
		}
	}
	return n
}

// appendBlock adds lines to a file, creating it when absent. A separated block
// gets a blank line above it, which is what makes a commented entry readable.
//
// The newline guard is not cosmetic: without it the first entry lands glued to
// whatever the last line was, silently changing that line's meaning. Separation
// is skipped for a file that did not exist, or the created file opens with a
// blank line.
func appendBlock(path string, block []string, separate bool) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var out strings.Builder
	if len(existing) > 0 {
		if !strings.HasSuffix(string(existing), "\n") {
			out.WriteString("\n")
		}
		if separate {
			out.WriteString("\n")
		}
	}
	for _, line := range block {
		out.WriteString(line + "\n")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = f.WriteString(out.String())
	return err
}

// plural renders a count with its noun, so a summary never reads "1 lines".
//
// Both forms are named by the caller. Appending "s" mangled "directory" in the
// first real run, and every rule that fixes that case breaks another — English
// inflection is not derivable from the singular.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
