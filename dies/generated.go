package dies

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/v5/reconcile"
)

// toolchainStamp marks a file as generated. Its absence on an existing file is
// what makes that file hand-written by definition — overwriting one would
// discard work with no way back.
const toolchainStamp = "# forge-toolchain:"

// generatedFile is one file the standard owns whole, as opposed to the
// append-only files gitignore and markdownlintignore assert entries in.
type generatedFile struct {
	// rel is the repo-relative path.
	rel string
	// want is what the standard says it should contain.
	want string
	// have is what is there now, empty when the file does not exist.
	have string
	// exists distinguishes an absent file from an empty one.
	exists bool
}

func (f generatedFile) matches() bool { return f.exists && f.have == f.want }

// change renders the file's state as the Change a plan shows.
func (f generatedFile) change(reason string) (reconcile.Change, bool) {
	if f.matches() {
		return reconcile.Change{}, false
	}

	verdict := reconcile.Stale
	if !f.exists {
		verdict = reconcile.Missing
	}

	return reconcile.Change{
		Item:    f.rel,
		Verdict: verdict,
		Repair:  reconcile.Automatic,
		Detail:  reason,
		Patch:   unifiedDiff(f.rel, f.have, f.want),
	}, true
}

func readGenerated(root, rel, want string) generatedFile {
	file := generatedFile{rel: rel, want: want}
	if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
		file.exists = true
		file.have = string(data)
	}
	return file
}

// writeGenerated writes a generated file, creating its directory.
func writeGenerated(root string, file generatedFile) error {
	path := filepath.Join(root, file.rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(file.want), 0o644)
}

// unifiedDiff renders the change to a file the way `diff -u` would.
//
// Hand-rolled rather than shelled out to diff: the content is already in
// memory, and writing two temp files per file per repo to read a diff back is
// a lot of syscalls for a plan that visits fifty repos.
func unifiedDiff(name, have, want string) string {
	if have == want {
		return ""
	}

	from := splitKeepingEmpty(have)
	to := splitKeepingEmpty(want)

	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", name, name)

	// Trim the common head and tail so the hunk is the change and not the file.
	// A generated config is mostly identical between versions, and printing all
	// two hundred lines for a one-line rev bump is what makes a plan unreadable.
	head := 0
	for head < len(from) && head < len(to) && from[head] == to[head] {
		head++
	}
	tail := 0
	for tail < len(from)-head && tail < len(to)-head &&
		from[len(from)-1-tail] == to[len(to)-1-tail] {
		tail++
	}

	const context = 2
	start := max(head-context, 0)
	fromEnd := min(len(from)-tail+context, len(from))
	toEnd := min(len(to)-tail+context, len(to))

	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", start+1, fromEnd-start, start+1, toEnd-start)
	for _, line := range from[start:max(len(from)-tail, start)] {
		fmt.Fprintf(&out, "-%s\n", line)
	}
	for _, line := range to[start:max(len(to)-tail, start)] {
		fmt.Fprintf(&out, "+%s\n", line)
	}
	return out.String()
}

func splitKeepingEmpty(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// handWritten reports whether a file exists without the generated stamp, which
// is the one state the generators must never overwrite.
func handWritten(root, rel string) bool {
	data, err := os.ReadFile(filepath.Join(root, rel))
	return err == nil && !strings.HasPrefix(string(data), toolchainStamp)
}

// blocker is a finding that stops a sync landing and that apply cannot clear.
//
// The whole of can-generate, which existed only because the generators had no
// way to run across the portfolio. They do now, so the gate is the read verb:
// `forge repos check` before a manifest bump is what that die was for.
func blocker(item, detail string) reconcile.Change {
	return reconcile.Change{
		Item:    item,
		Verdict: reconcile.Undeclared,
		Repair:  reconcile.ByHand,
		Detail:  detail,
	}
}

// unexpandedPlaceholder finds a generator placeholder that survived rendering.
//
// `${{ ... }}` is ordinary GitHub Actions syntax and must not read as a failed
// substitution, so only a `{{` not preceded by `$` counts.
func unexpandedPlaceholder(text string) bool {
	for i := range len(text) - 1 {
		if text[i] == '{' && text[i+1] == '{' && (i == 0 || text[i-1] != '$') {
			return true
		}
	}
	return false
}

// runValidator runs a tool inside a directory and returns its first line of
// complaint, or "" when the tool is absent or content.
//
// Absent means no finding rather than a failure: actionlint and pre-commit are
// not required to be installed to reconcile a repo, and reporting a missing
// linter as a problem with the repo would be a lie.
//
// The working directory is set rather than passed as a flag. actionlint has no
// -chdir, which the first real run found: every repo reported "flag provided
// but not defined" as a problem with its own workflow.
func runValidator(dir, tool string, args ...string) string {
	if _, err := exec.LookPath(tool); err != nil {
		return ""
	}
	cmd := exec.Command(tool, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	if line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// subFS narrows an embedded tree, passing a root of "." straight through.
func subFS(fsys fs.FS, dir string) (fs.FS, error) {
	if dir == "." || dir == "" {
		return fsys, nil
	}
	return fs.Sub(fsys, dir)
}
