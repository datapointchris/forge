package runner

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/datapointchris/forge/internal/config"
)

// ExitSkip is the exit code scripts use to signal "nothing to do."
const ExitSkip = 2

type Result struct {
	Name   string
	Status string // "OK", "SKIP (reason)", "FAIL (exit N)"
}

type Opts struct {
	ScriptFile string   // absolute path to script (empty for inline mode)
	InlineArgs []string // command + args (empty for script mode)
	DryRun     bool
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
		fmt.Printf("[DRY RUN] Would execute in: %s\n", repo.Path)
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
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		exitCode := c.ProcessState.ExitCode()
		if exitCode == ExitSkip {
			return Result{Name: repo.Name, Status: "SKIP (nothing to do)"}
		}
		return Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (exit %d)", exitCode)}
	}

	return Result{Name: repo.Name, Status: "OK"}
}

func PrintResult(r Result) {
	fmt.Printf("  %-30s %s\n", "["+r.Name+"]", r.Status)
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
	fmt.Printf("\nSummary: %d ok, %d skip, %d fail\n", ok, skip, fail)
}
