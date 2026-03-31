package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"

	"github.com/datapointchris/forge/internal/config"
)

// ExitSkip is the exit code scripts use to signal "nothing to do."
const ExitSkip = 2

const lineWidth = 80

// Nerd font icons (2-char display width)
const (
	IconOK   = "\uf00c" // ✔
	IconWarn = "\uf071" // ⚠
	IconFail = "\uf00d" // ✘
	IconRun  = "\uf013" // ⚙
)

// Reusable color printers.
var (
	cRed     = color.New(color.FgHiRed)
	cGreen   = color.New(color.FgHiGreen)
	cYellow  = color.New(color.FgHiYellow)
	cCyan    = color.New(color.FgHiCyan)
	cDim     = color.New(color.Faint)
	cBoldRed = color.New(color.Bold, color.FgHiRed)
)

type Result struct {
	Name   string
	Status string // "OK", "SKIP (reason)", "FAIL (exit N)"
	Output string // captured output (only when CaptureOutput is set)
}

type Opts struct {
	ScriptFile    string   // absolute path to script (empty for inline mode)
	InlineArgs    []string // command + args (empty for script mode)
	DryRun        bool
	CaptureOutput bool // tee stdout/stderr to buffer for failure replay
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

func ExecuteInRepo(repo config.Repo, opts Opts) Result {
	if opts.DryRun {
		cCyan.Printf("  %s  %s\n", IconRun, repo.Name)
		return Result{Name: repo.Name, Status: "OK"}
	}

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

	var buf bytes.Buffer
	if opts.CaptureOutput {
		c.Stdout = io.MultiWriter(os.Stdout, &buf)
		c.Stderr = io.MultiWriter(os.Stderr, &buf)
	} else {
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	}

	if err := c.Run(); err != nil {
		exitCode := c.ProcessState.ExitCode()
		output := buf.String()
		if exitCode == ExitSkip {
			return Result{Name: repo.Name, Status: "SKIP (nothing to do)", Output: output}
		}
		return Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (exit %d)", exitCode), Output: output}
	}

	return Result{Name: repo.Name, Status: "OK", Output: buf.String()}
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

func PrintResult(r Result) {
	switch {
	case r.Status == "OK":
		msg := "ok"
		if line := lastLine(r.Output); line != "" {
			msg = line
		}
		fmt.Println(statusLine(IconOK, r.Name, msg, cGreen))
	case strings.HasPrefix(r.Status, "SKIP"):
		reason := "skipped"
		if line := lastLine(r.Output); line != "" {
			reason = line
		} else if i := strings.Index(r.Status, "("); i >= 0 {
			reason = r.Status[i+1 : len(r.Status)-1]
		}
		fmt.Println(statusLine(IconWarn, r.Name, reason, cYellow))
	case strings.HasPrefix(r.Status, "FAIL"):
		msg := r.Status
		if line := lastLine(r.Output); line != "" {
			msg = line
		}
		fmt.Println(statusLine(IconFail, r.Name, msg, cRed))
	}
}

func PrintDryRunHeader() {
	bar := strings.Repeat("═", 28)
	cYellow.Printf("\n%s┤  DRY RUN  ├%s\n\n", bar, bar)
}

func PrintDryRunFooter() {
	bar := strings.Repeat("═", 26)
	cYellow.Printf("\n%s┤  END DRY RUN  ├%s\n", bar, bar)
}

func PrintSummary(results []Result) {
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

	fmt.Println()
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
	fmt.Printf("  %s\n", strings.Join(parts, fmt.Sprintf("  %s  ", sep)))
}

func PrintFailures(results []Result) {
	var failures []Result
	for _, r := range results {
		if strings.HasPrefix(r.Status, "FAIL") {
			failures = append(failures, r)
		}
	}
	if len(failures) == 0 {
		return
	}

	line := strings.Repeat("─", lineWidth)
	cRed.Printf("\n%s\n", line)
	cBoldRed.Printf("%sFailures (%d):\n", IconFail, len(failures))
	cRed.Printf("%s\n", line)

	for _, r := range failures {
		fmt.Printf("\n%s — %s\n", cRed.Sprintf("%s  %s", IconFail, r.Name), r.Status)
		if r.Output != "" {
			// Indent failure output for readability
			for _, line := range strings.Split(strings.TrimRight(r.Output, "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
