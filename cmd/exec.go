package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/runner"
)

var (
	execFilterNames []string
	execDryRun      bool
	scriptFile      string
)

var execCmd = &cobra.Command{
	Use:   "exec [-- command...]",
	Short: "Execute a command in each repo",
	Long: `Execute a command in each repo tracked by syncer.

Inline mode:
  forge exec -- git status --short

Script mode:
  forge exec -f ./dies/maintenance/my-script.sh`,
	RunE: runExec,
}

func init() {
	execCmd.Flags().StringSliceVarP(&execFilterNames, "filter", "F", nil, "comma-separated repo names to include")
	execCmd.Flags().BoolVarP(&execDryRun, "dry-run", "n", false, "show which repos would be affected without executing")
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
		scriptFile, err = filepath.Abs(scriptFile)
		if err != nil {
			return fmt.Errorf("resolving script path: %w", err)
		}
	}

	var (
		cfg *config.SyncerConfig
		err error
	)
	if cfgPath != "" {
		cfg, err = config.LoadSyncerConfig(cfgPath)
	} else {
		cfg, err = config.LoadReposFromForgeConfig()
	}
	if err != nil {
		return err
	}

	repos := runner.FilterRepos(runner.ActiveRepos(cfg.Repos), execFilterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(execFilterNames, ", "))
	}

	opts := runner.Opts{
		ScriptFile: scriptFile,
		InlineArgs: args,
		DryRun:     execDryRun,
	}

	var results []runner.Result
	for _, repo := range repos {
		r := runner.ExecuteInRepo(repo, opts)
		results = append(results, r)
		if !execDryRun {
			runner.PrintResult(r)
		}
	}

	runner.PrintSummary(results)
	return nil
}
