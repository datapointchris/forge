package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/runner"
)

var (
	execDryRun bool
	scriptFile string
)

// newExecCmd is the escape hatch, and what makes dies affordable as Go only: a
// one-off sweep is one command here, with nothing to register.
//
// Built fresh for each repos subtree, because AddCommand reparents a command
// and a shared one belongs to whichever subtree was built last. Takes the noun
// so -F resolves through the same selection the reconcile verbs use.
func newExecCmd(n *reconcileNoun) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [-- command...]",
		Short: "Run an arbitrary command in each repo",
		Long: `Run a command in each selected repo.

Inline:
  forge repos exec -- git status --short

Script file:
  forge repos exec -f ./one-off.sh

Every line the command prints is tagged with the repo's registry name and a
tab, on the stream it was written to. Each repo's status and the closing tally
go to stderr, so stdout is the commands' output alone:
  forge repos exec -- git log --oneline --since=14.days | cut -f1 | sort | uniq -c

For anything reusable, write a die instead — it gets plan, check and apply.`,
		Example: `  # Which repos have uncommitted work
  forge repos exec -- git status --short

  # Which repos a command would reach, running nothing
  forge repos exec -n -- git pull --ff-only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(n, cmd, args)
		},
	}
	cmd.Flags().StringSliceVarP(&n.filterNames, "filter", "F", nil, "comma-separated "+n.one+" names to include")
	cmd.Flags().BoolVarP(&execDryRun, "dry-run", "n", false, "show which repos would be affected without executing")
	cmd.Flags().StringVarP(&scriptFile, "file", "f", "", "path to script file to execute in each repo")
	return cmd
}

func runExec(n *reconcileNoun, cmd *cobra.Command, args []string) error {
	if scriptFile == "" && len(args) == 0 {
		return goclikit.UsageError(fmt.Errorf("provide a command after -- or use -f to specify a script file"))
	}
	if scriptFile != "" && len(args) > 0 {
		return goclikit.UsageError(fmt.Errorf("cannot use both -f and inline command"))
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

	repos, _, err := n.selected(cmd)
	if err != nil {
		return err
	}

	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if execDryRun {
		for _, repo := range repos {
			if _, err := fmt.Fprintln(stdout, repo.Name); err != nil {
				return err
			}
		}
		_, _ = fmt.Fprintf(stderr, "%d repo(s) would run it; nothing ran.\n", len(repos))
		return nil
	}

	opts := runner.Opts{
		ScriptFile: scriptFile,
		InlineArgs: args,
	}

	var results []runner.Result
	for _, repo := range repos {
		r := runner.ExecuteInRepo(repo, opts, stdout, stderr)
		results = append(results, r)
		runner.PrintResult(stderr, r)
	}

	runner.PrintSummary(stderr, results)
	// The summary already names each failed repo, so the error adds only the exit code.
	if slices.ContainsFunc(results, func(r runner.Result) bool { return strings.HasPrefix(r.Status, "FAIL") }) {
		return goclikit.ErrReported
	}
	return nil
}
