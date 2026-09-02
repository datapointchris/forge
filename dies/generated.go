package dies

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// toolchainStamp marks a file as generated. Its absence on an existing file is
// what makes that file hand-written by definition — overwriting one would
// discard work with no way back.
const toolchainStamp = "# forge-toolchain:"

// managedMark marks a file forge deploys whole from a static template.
//
// Separate from toolchainStamp because the two answer different questions. A
// generated file's content is derived from the manifest, so its marker carries
// the version and a staged rollout reads it. A tool config is a template copied
// verbatim, so ownership is the only fact its marker has to carry — a version
// there would make every manifest bump report eight files per repo as drift
// that no content change had touched.
const managedMark = "# forge-managed"

// forgeMarks are the first lines that identify a file as forge's own.
var forgeMarks = []string{toolchainStamp, managedMark}

// generatedFile is one file the standard owns whole, as opposed to the
// append-only .gitignore the gitignore die asserts entries in.
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

// managed reports whether the die has anything to say about this path at all. A
// zero generatedFile names no path, which is how a die says the file is not its
// business in this repo.
func (f generatedFile) managed() bool { return f.rel != "" }

// wanted reports whether the standard says content belongs at this path.
//
// Separate from managed because a die has to distinguish three states, not two:
// the path is none of my business, the standard wants this content there, and
// the standard has withdrawn a file it once wrote. Collapsing the first and
// third lets a repo keep a generated file after it stops qualifying for one,
// with every verb reporting converged because nothing looks.
func (f generatedFile) wanted() bool { return f.rel != "" && f.want != "" }

// retracting reports whether a file the standard no longer wants is still on
// disk. The caller decides whether forge may remove it — a file without the
// stamp is not forge's to delete.
func (f generatedFile) retracting() bool { return f.rel != "" && f.want == "" && f.exists }

// change renders the file's state as the Change a plan shows.
func (f generatedFile) change(reason string) (reconcile.Change, bool) {
	if !f.managed() || f.matches() {
		return reconcile.Change{}, false
	}

	// A retraction is Stale rather than Undeclared: forge wrote this file, and
	// Undeclared is reserved for what it did not put there and may not touch.
	if f.retracting() {
		return reconcile.Change{
			Item:    f.rel,
			Verdict: reconcile.Stale,
			Repair:  reconcile.Automatic,
			Detail:  reason,
			Patch:   unifiedDiff(f.rel, f.have, ""),
		}, true
	}
	if !f.wanted() {
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

// removeGenerated deletes a file the standard has withdrawn.
//
// The caller has already established that the file carries the stamp, which is
// the record proving forge wrote it. Without that record this would be forge
// deleting a repo's own file, which no verdict authorizes.
func removeGenerated(root string, file generatedFile) error {
	if err := os.Remove(filepath.Join(root, file.rel)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// handWritten reports whether a file exists carrying none of forge's markers,
// which is the one state the generators must never overwrite.
//
// An absent file is not hand-written. First deployment writes it, and a repo
// forge has never touched is reached only by a declaration.
func handWritten(root, rel string) bool {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}
	for _, mark := range forgeMarks {
		if strings.HasPrefix(string(data), mark) {
			return false
		}
	}
	return true
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
