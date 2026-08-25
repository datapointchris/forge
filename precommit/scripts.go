package precommit

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ScriptCategory is the block category the shebang-selected Python hooks sit
// under. Like sql and git it is gated by something other than a component, so
// Generate seeds it rather than dirsByCategory.
const ScriptCategory = "python-scripts"

// UntaggedPythonScript reports whether a file runs Python that pre-commit's
// tagging cannot see.
//
// pre-commit selects Python files through identify, which derives the `python`
// tag from the extension or from the interpreter the shebang names. An app with
// no extension whose shebang reads `#!/usr/bin/env -S uv run --script` names
// `uv`, so it is tagged executable/file/text and every Python hook skips it in
// silence — ruff and mypy both report a clean repo while the file is unread.
//
// Interpreter resolution follows identify's: `/usr/bin/env` forwards to its
// first non-option argument, so env and its flags are dropped before the
// basename is taken. A shebang landing on a python interpreter is left alone,
// because identify already tags that file and a second hook would only lint it
// twice.
//
// Splitting on whitespace rather than shell-quoting rules is deliberate and is
// the one place this parts company with identify. A quoted `env -S "uv run"`
// would resolve differently, and nothing in the portfolio writes one.
func UntaggedPythonScript(path, firstLine string) bool {
	if filepath.Ext(path) != "" {
		return false
	}
	if !strings.HasPrefix(firstLine, "#!") {
		return false
	}

	fields := strings.Fields(strings.TrimPrefix(firstLine, "#!"))
	if len(fields) > 0 && filepath.Base(fields[0]) == "env" {
		fields = fields[1:]
		for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
			fields = fields[1:]
		}
	}
	if len(fields) == 0 {
		return false
	}

	interpreter := filepath.Base(fields[0])
	if strings.HasPrefix(interpreter, "python") {
		return false
	}
	return interpreter == "uv"
}

// ScriptsPattern renders paths as the anchored alternation a hook's files: key
// takes, single-quote-escaped for the YAML it is written into.
//
// An empty list yields "", which is the signal Generate uses to leave the block
// out entirely. A pattern matching nothing is not the same thing: pre-commit
// would still install the hooks, and a repo carrying hooks that can never fire
// reports converged while saying nothing about anything.
func ScriptsPattern(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, regexp.QuoteMeta(path))
	}
	return strings.ReplaceAll("^("+strings.Join(quoted, "|")+")$", "'", "''")
}
