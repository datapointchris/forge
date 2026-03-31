package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/idp/internal/config"
)

type result struct {
	Name   string
	Status string // "OK", "SKIP (reason)", "FAIL (exit N)"
}

var (
	filterNames []string
	dryRun      bool
	scriptFile  string
)

var execCmd = &cobra.Command{
	Use:   "exec [-- command...]",
	Short: "Execute a command in each repo",
	Long: `Execute a command in each repo tracked by syncer.

Inline mode:
  idp exec -- git status --short

Script mode:
  idp exec -f ./my-script.sh`,
	RunE: runExec,
}

func init() {
	execCmd.Flags().StringSliceVarP(&filterNames, "filter", "F", nil, "comma-separated repo names to include")
	execCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show which repos would be affected without executing")
	execCmd.Flags().StringVarP(&scriptFile, "file", "f", "", "path to script file to execute in each repo")
	rootCmd.AddCommand(execCmd)
}

func runExec(cmd *cobra.Command, args []string) error {
	if scriptFile == "" && len(args) == 0 {
		return fmt.Errorf("provide a command after -- or use -f to specify a script file")
	}
	if scriptFile != "" && len(args) > 0 {
		return fmt.Errorf("cannot use both -f and inline command")
	}

	if scriptFile != "" {
		info, err := os.Stat(scriptFile)
		if err != nil {
			return fmt.Errorf("script file %s: %w", scriptFile, err)
		}
		if info.IsDir() {
			return fmt.Errorf("script file %s is a directory", scriptFile)
		}
	}

	cfg, err := config.LoadSyncerConfig(cfgPath)
	if err != nil {
		return err
	}

	repos := filterRepos(cfg.Repos, filterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(filterNames, ", "))
	}

	var results []result
	for _, repo := range repos {
		r := executeInRepo(repo, args)
		results = append(results, r)
		printResult(r)
	}

	printSummary(results)
	return nil
}

func filterRepos(repos []config.Repo, names []string) []config.Repo {
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

func executeInRepo(repo config.Repo, inlineArgs []string) result {
	if dryRun {
		fmt.Printf("[DRY RUN] Would execute in: %s\n", repo.Path)
		return result{Name: repo.Name, Status: "OK"}
	}

	info, err := os.Stat(repo.Path)
	if err != nil || !info.IsDir() {
		return result{Name: repo.Name, Status: "SKIP (not found)"}
	}

	gitDir := repo.Path + "/.git"
	if _, err := os.Stat(gitDir); err != nil {
		return result{Name: repo.Name, Status: "SKIP (not a git repo)"}
	}

	var c *exec.Cmd
	if scriptFile != "" {
		c = exec.Command("bash", scriptFile)
	} else {
		c = exec.Command(inlineArgs[0], inlineArgs[1:]...)
	}
	c.Dir = repo.Path
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		exitCode := c.ProcessState.ExitCode()
		return result{Name: repo.Name, Status: fmt.Sprintf("FAIL (exit %d)", exitCode)}
	}

	return result{Name: repo.Name, Status: "OK"}
}

func printResult(r result) {
	if dryRun {
		return
	}
	fmt.Printf("  %-30s %s\n", "["+r.Name+"]", r.Status)
}

func printSummary(results []result) {
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
