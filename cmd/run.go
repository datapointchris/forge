package cmd

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/directory"
	"github.com/datapointchris/forge/reconcile"
)

// directoriesOnly is the verb set directories carry that repos do not.
func directoriesOnly(n *reconcileNoun) []*cobra.Command {
	return []*cobra.Command{directoriesRunCmd(n)}
}

// directoriesRunCmd is the verb repos do not have.
//
// A repo's generated config is executed twice without forge: by the git hook on
// every commit, and by validate.yml on every pull request. ci.md § "Don't run
// `pre-commit run` manually before committing" exists because that coverage is
// already there. A maintained directory has neither, so the config forge writes
// for one would never run at all — this is what runs it.
//
// Takes the noun rather than reaching for the package variable, which would be
// an initialization cycle: the value being built is the one this hangs off.
func directoriesRunCmd(n *reconcileNoun) *cobra.Command {
	var rebuild bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Build the index and run the hooks",
		Long: `Execute every pre-commit-stage hook against each maintained directory.

pre-commit needs a git index to know which files exist. A directory git does not
version has none, so forge builds a throwaway one in its cache — a bare
repository outside the tree, with the directory as its work tree. Nothing is
written inside the directory itself, because a .git in a file-synced folder
conflicts on every peer.

Which files are examined is decided the ordinary way, by the directory's
.gitignore.

Hooks that fix rather than report will rewrite files. That is the same contract
a repo's pre-commit hooks have; the difference is only that here it happens when
you ask rather than when you commit.

Exit codes: 0 everything passed, 1 a hook failed or rewrote a file, 3 the run
could not happen.`,
		Example: "  forge directories run\n  forge directories run -F claude\n  forge directories run --rebuild --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDirectories(n, cmd, rebuild)
		},
	}

	cmd.Flags().StringSliceVarP(&n.filterNames, "filter", "F", nil,
		"comma-separated "+n.one+" names to include")
	cmd.Flags().BoolVar(&n.asJSON, "json", false, "output as JSON to stdout")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "discard the index before running")

	return cmd
}

func runDirectories(n *reconcileNoun, cmd *cobra.Command, rebuild bool) error {
	selected, _, err := n.selected(cmd)
	if err != nil {
		return err
	}

	results := make([]directory.Result, 0, len(selected))
	var failed, broke int

	for _, target := range selected {
		index := directory.IndexFor(target)
		if rebuild {
			if err := index.Discard(); err != nil {
				return err
			}
		}

		// The hooks write their own output as they go; stdout stays the summary
		// rows, so a caller piping one gets rows and not hook noise.
		result, err := directory.Run(index, cmd.ErrOrStderr(), cmd.ErrOrStderr())
		if err != nil {
			// One directory that cannot run must not abandon the others, for the
			// same reason reconcile.Assess recovers per die per repo.
			row(cmd.ErrOrStderr(), "%s: %s\n", target.Name, err)
			broke++
			results = append(results, result)
			continue
		}
		if !result.Passed {
			failed++
		}
		results = append(results, result)
	}

	if n.asJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(results); err != nil {
			return err
		}
	} else {
		for _, result := range results {
			if !result.Ran {
				row(cmd.OutOrStdout(), "%-12s did not run\n", result.Name)
				continue
			}
			status := "passed"
			if !result.Passed {
				status = "hooks reported findings"
			}
			row(cmd.OutOrStdout(), "%-12s %s (%s)\n", result.Name, status,
				plural(result.Files, "file", "files"))
		}
	}

	switch {
	case broke > 0:
		return &reconcile.ExitError{Code: reconcile.ExitIssue}
	case failed > 0:
		return &reconcile.ExitError{Code: reconcile.ExitDrift}
	}
	return nil
}
