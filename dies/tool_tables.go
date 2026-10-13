package dies

import (
	"fmt"
	"regexp"
	"strings"
)

// tableHeader matches a single-bracket TOML table header, with an optional
// trailing comment.
var tableHeader = regexp.MustCompile(`^\[([^\[\]]+)\]\s*(#.*)?$`)

// toolTables lifts the [tool.<name>] tables out of a pyproject.toml as the
// standalone config file that tool reads: the bare table's keys at the top
// level, and each subtable with the tool.<name>. prefix dropped from its
// header.
//
// Text rather than a parse, because the comments are the reasoning behind each
// setting and a TOML round-trip drops them. A comment block directly above a
// header travels with that header, so the explanation of a ruff table comes
// along and the one above the next tool's table does not.
//
// The bare table has to come first. Its keys land at the top level, and a key
// written after a subtable header belongs to that subtable, so a standard that
// put [tool.ruff] after [tool.ruff.format] would move line-length into format.
func toolTables(pyproject, name string) (string, error) {
	prefix := "tool." + name
	var out, comments []string
	inside, seenSubtable := false, false

	for _, line := range strings.Split(pyproject, "\n") {
		trimmed := strings.TrimSpace(line)
		if match := tableHeader.FindStringSubmatch(trimmed); match != nil {
			table := strings.TrimSpace(match[1])
			inside = table == prefix || strings.HasPrefix(table, prefix+".")
			if inside {
				out = append(out, comments...)
				if table == prefix {
					if seenSubtable {
						return "", fmt.Errorf("[%s] follows one of its subtables, so its keys cannot be lifted to the top level", prefix)
					}
				} else {
					seenSubtable = true
					out = append(out, "["+strings.TrimPrefix(table, prefix+".")+"]")
				}
			}
			comments = nil
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			comments = append(comments, line)
			continue
		}
		if inside {
			out = append(out, comments...)
			out = append(out, line)
		}
		comments = nil
	}

	body := strings.Trim(strings.Join(out, "\n"), "\n")
	if body == "" {
		return "", fmt.Errorf("the standard has no [%s] tables", prefix)
	}
	return body + "\n", nil
}
