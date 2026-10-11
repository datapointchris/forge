package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"

	"github.com/datapointchris/forge/config"
)

// ExitSkip is the exit code scripts use to signal "nothing to do."
const ExitSkip = 2

const lineWidth = 80

// Nerd font icons (2-char display width). They mark the status lines, which go
// to stderr, and never a line of the command's own output.
const (
	IconOK   = "\uf00c" // ✔
	IconWarn = "\uf071" // ⚠
	IconFail = "\uf00d" // ✘
)

var (
	cRed    = color.New(color.FgHiRed)
	cGreen  = color.New(color.FgHiGreen)
	cYellow = color.New(color.FgHiYellow)
	cDim    = color.New(color.Faint)
)

type Result struct {
	Name   string
	Status string // "OK", "SKIP (reason)", "FAIL (exit N)", "FAIL (did not start)"
}

type Opts struct {
	ScriptFile string   // absolute path to script (empty for inline mode)
	InlineArgs []string // command + args (empty for script mode)
}

// ActiveRepos returns the repos being worked on, dropping dormant ones as well
// as retired. Dormant is most of the portfolio and none of it takes another
// release, so a maintenance die swept across it is pure churn: a config every
// repo "should" have is worth nothing in one that will never run the tool.
// Status is matched plainly: the registry suite refuses an entry without one,
// so a reader supplying a meaning for the absent case would be choosing for a
// value the file never holds.
func ActiveRepos(repos []config.Repo) []config.Repo {
	var active []config.Repo
	for _, r := range repos {
		if r.Status == "active" {
			active = append(active, r)
		}
	}
	return active
}

// AddressableRepos returns everything a name can still reach. Retired is the
// one status -F cannot override.
func AddressableRepos(repos []config.Repo) []config.Repo {
	var addressable []config.Repo
	for _, r := range repos {
		if r.Status != "retired" {
			addressable = append(addressable, r)
		}
	}
	return addressable
}

// SelectRepos resolves the repos a command should act on. With no -F names it
// returns the active portfolio, excluding third-party reference clones — no
// implicit operation should ever touch a repo that is not ours. Naming repos
// explicitly overrides that, so a reference clone or a dormant repo is still
// reachable on purpose.
func SelectRepos(repos []config.Repo, names []string) []config.Repo {
	if len(names) > 0 {
		return FilterRepos(AddressableRepos(repos), names)
	}
	return OwnedRepos(ActiveRepos(repos))
}

// OwnedRepos returns repos in the portfolio, dropping third-party reference
// clones — the entries whose owner is not the registry's own.
func OwnedRepos(repos []config.Repo) []config.Repo {
	var owned []config.Repo
	for _, r := range repos {
		if !r.Reference {
			owned = append(owned, r)
		}
	}
	return owned
}

func FilterRepos(repos []config.Repo, names []string) []config.Repo {
	if len(names) == 0 {
		return repos
	}

	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[strings.TrimSpace(n)] = true
	}

	var filtered []config.Repo
	for _, r := range repos {
		if nameSet[r.Name] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// ExecuteInRepo runs the command in one repo. Every line it writes reaches the
// matching stream behind the repo's registry name and a tab, so many repos'
// output in one stream still says which repo each line came from, the way
// `grep -H` names the file.
func ExecuteInRepo(repo config.Repo, opts Opts, stdout, stderr io.Writer) Result {
	info, err := os.Stat(repo.Path)
	if err != nil || !info.IsDir() {
		return Result{Name: repo.Name, Status: "SKIP (not found)"}
	}

	gitDir := repo.Path + "/.git"
	if _, err := os.Stat(gitDir); err != nil {
		return Result{Name: repo.Name, Status: "SKIP (not a git repo)"}
	}

	var c *exec.Cmd
	if opts.ScriptFile != "" {
		c = exec.Command("bash", opts.ScriptFile)
	} else {
		c = exec.Command(opts.InlineArgs[0], opts.InlineArgs[1:]...)
	}
	c.Dir = repo.Path
	// FORGE_REPO_NAME is the registry name, which a script cannot derive from its
	// cwd: basenames are neither unique — a reference clone can share a name with
	// a portfolio repo — nor always equal to the registry name, which can differ
	// from the directory the repo sits in.
	c.Env = append(os.Environ(), "FORGE_REPO_NAME="+repo.Name)

	out := &taggedLines{out: stdout, tag: repo.Name + "\t"}
	errs := &taggedLines{out: stderr, tag: repo.Name + "\t"}
	c.Stdout = out
	c.Stderr = errs
	runErr := c.Run()
	out.finish()

	if runErr != nil && c.ProcessState == nil {
		_, _ = fmt.Fprintf(errs, "%v\n", runErr)
		errs.finish()
		return Result{Name: repo.Name, Status: "FAIL (did not start)"}
	}
	errs.finish()
	if runErr != nil {
		exitCode := c.ProcessState.ExitCode()
		if exitCode == ExitSkip {
			return Result{Name: repo.Name, Status: "SKIP (nothing to do)"}
		}
		return Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (exit %d)", exitCode)}
	}

	return Result{Name: repo.Name, Status: "OK"}
}

// taggedLines writes what it is given with a tag at the start of every line. A
// line split across two writes carries one tag, because the tag goes out only
// where the previous write ended a line.
type taggedLines struct {
	out     io.Writer
	tag     string
	midLine bool
}

func (w *taggedLines) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		if !w.midLine {
			if _, err := io.WriteString(w.out, w.tag); err != nil {
				return 0, err
			}
		}
		end := bytes.IndexByte(p, '\n')
		line := p
		if end >= 0 {
			line = p[:end+1]
		}
		if _, err := w.out.Write(line); err != nil {
			return 0, err
		}
		w.midLine = end < 0
		p = p[len(line):]
	}
	return written, nil
}

// finish ends a last line the command left open, so the next repo's first tag
// starts a line of its own.
func (w *taggedLines) finish() {
	if w.midLine {
		_, _ = io.WriteString(w.out, "\n")
		w.midLine = false
	}
}

func statusLine(icon, name, msg string, c *color.Color) string {
	// icon (2 display chars) + 2 spaces + name + padding + msg
	prefix := icon + "  " + name + " "
	// prefix display width: icon=2 + spaces=2 + name + space=1
	prefixWidth := 2 + 2 + len(name) + 1
	msgWidth := len(msg)
	padWidth := lineWidth - prefixWidth - msgWidth
	if padWidth < 1 {
		padWidth = 1
	}
	padding := strings.Repeat("·", padWidth)
	return c.Sprintf("%s", prefix) + cDim.Sprintf(" %s ", padding) + c.Sprintf("%s", msg)
}

// PrintResult writes one repo's status line. It is about the run rather than the
// command's output, so the caller hands it stderr, and a console row's failed
// write has no one to report to.
func PrintResult(w io.Writer, r Result) {
	switch {
	case r.Status == "OK":
		_, _ = fmt.Fprintln(w, statusLine(IconOK, r.Name, "ok", cGreen))
	case strings.HasPrefix(r.Status, "SKIP"):
		reason := "skipped"
		if i := strings.Index(r.Status, "("); i >= 0 {
			reason = r.Status[i+1 : len(r.Status)-1]
		}
		_, _ = fmt.Fprintln(w, statusLine(IconWarn, r.Name, reason, cYellow))
	case strings.HasPrefix(r.Status, "FAIL"):
		_, _ = fmt.Fprintln(w, statusLine(IconFail, r.Name, r.Status, cRed))
	}
}

func PrintSummary(w io.Writer, results []Result) {
	var ok, skip, fail int
	for _, r := range results {
		switch {
		case r.Status == "OK":
			ok++
		case strings.HasPrefix(r.Status, "SKIP"):
			skip++
		case strings.HasPrefix(r.Status, "FAIL"):
			fail++
		}
	}

	_, _ = fmt.Fprintln(w)
	var parts []string
	if ok > 0 {
		parts = append(parts, cGreen.Sprintf("%s %d ok", IconOK, ok))
	}
	if skip > 0 {
		parts = append(parts, cYellow.Sprintf("%s %d skip", IconWarn, skip))
	}
	if fail > 0 {
		parts = append(parts, cRed.Sprintf("%s %d fail", IconFail, fail))
	}
	sep := cDim.Sprintf("│")
	_, _ = fmt.Fprintf(w, "  %s\n", strings.Join(parts, fmt.Sprintf("  %s  ", sep)))
}
