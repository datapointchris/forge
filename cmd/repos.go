package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v5/dies"
	"github.com/datapointchris/forge/v5/reconcile"
	"github.com/datapointchris/forge/v5/runner"
	"github.com/datapointchris/forge/v5/toolchain"
)

var (
	reposFilterNames []string
	reposJSON        bool
)

var reposCmd = &cobra.Command{
	Use:   "repos",
	Short: "Reconcile the repos against the standards",
	Long: `Three verbs over one measurement, Terraform-shaped.

  check   what is wrong: findings apply cannot fix
  plan    what apply would change
  apply   make it so

plan is apply minus its last step — the same walk, stopping before the write —
so there is no --dry-run for apply to be the opposite of. check is a different
question: a repo missing a standard .gitignore entry is drift, which is what
apply is for, while a hand-written pipeline or an unmarked custom hook needs a
person. One verb answering both means one exit code carrying both.

Naming a die selects it; omitting one selects them all. -F narrows the repos.

Exit codes: 0 converged, 1 changes pending (plan only), 3 something is wrong.`,
	RunE: requireSubcommand,
}

var reposCheckCmd = &cobra.Command{
	Use:               "check [die]",
	Short:             "Report what is wrong, which apply cannot fix",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeDieNames,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReconcile(cmd, args, reconcile.LensCheck)
	},
}

var reposPlanCmd = &cobra.Command{
	Use:               "plan [die]",
	Short:             "Report what apply would change, writing nothing",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeDieNames,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReconcile(cmd, args, reconcile.LensPlan)
	},
}

var reposApplyCmd = &cobra.Command{
	Use:   "apply <die>",
	Short: "Make the pending changes",
	// A die is required. An unaliased apply over every die and every repo is
	// not something anyone should be able to type by accident, and the read
	// verbs are where the whole-portfolio question belongs.
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDieNames,
	RunE:              runReposApply,
}

func init() {
	for _, cmd := range []*cobra.Command{reposCheckCmd, reposPlanCmd, reposApplyCmd} {
		cmd.Flags().StringSliceVarP(&reposFilterNames, "filter", "F", nil, "comma-separated repo names to include")
		cmd.Flags().BoolVar(&reposJSON, "json", false, "output as JSON to stdout")
		reposCmd.AddCommand(cmd)
	}
	rootCmd.AddCommand(reposCmd)
}

func completeDieNames(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return dies.BuiltinNames(), cobra.ShellCompDirectiveNoFileComp
}

// selectedDies resolves the die argument, or every die when none was named.
func selectedDies(args []string) ([]reconcile.Die, error) {
	if len(args) == 0 {
		return dies.Builtin(), nil
	}
	die, err := dies.Named(args[0])
	if err != nil {
		return nil, err
	}
	return []reconcile.Die{die}, nil
}

// targets resolves the repos to walk, with the assets every die may need.
func targets() ([]reconcile.Target, error) {
	cfg, err := loadRepos()
	if err != nil {
		return nil, err
	}

	repos := runner.SelectRepos(cfg.Repos, reposFilterNames)
	if len(repos) == 0 {
		return nil, fmt.Errorf("no repos matched filter")
	}

	assets, err := loadAssets()
	if err != nil {
		return nil, err
	}

	selected := make([]reconcile.Target, 0, len(repos))
	for _, repo := range repos {
		selected = append(selected, reconcile.Target{Repo: repo, Assets: assets})
	}
	return selected, nil
}

// loadAssets gathers the embedded trees and the version manifest once, so every
// die in a walk reads the same manifest rather than each loading its own.
func loadAssets() (reconcile.Assets, error) {
	preCommitFS, err := resolvePreCommitFS()
	if err != nil {
		return reconcile.Assets{}, err
	}

	manifest, err := toolchain.Load(preCommitFS)
	if err != nil {
		return reconcile.Assets{}, err
	}

	ciFS, err := resolveCIBlocksFS()
	if err != nil {
		return reconcile.Assets{}, err
	}

	return reconcile.Assets{PreCommit: preCommitFS, CI: ciFS, Manifest: manifest}, nil
}

func runReconcile(cmd *cobra.Command, args []string, lens reconcile.Lens) error {
	chosen, err := selectedDies(args)
	if err != nil {
		return err
	}
	walked, err := targets()
	if err != nil {
		return err
	}

	measurements := reconcile.AssessAll(walked, chosen)

	results := make([]reconcile.Result, 0, len(measurements))
	for _, m := range measurements {
		results = append(results, m.Fold(lens))
	}

	if reposJSON {
		if err := reconcile.EmitJSON(cmd.OutOrStdout(), results); err != nil {
			return err
		}
		return reconcile.ExitFor(results)
	}

	render(cmd, results)
	reconcile.RenderSummary(cmd.OutOrStdout(), results)
	return reconcile.ExitFor(results)
}

// render writes the rows: a repo's summary to stdout, the items it is a summary
// of to stderr. Below a composite verb the items are the evidence for the row
// that follows, not the answer a caller parses — which is --json.
func render(cmd *cobra.Command, results []reconcile.Result) {
	width := reconcile.LabelWidth(results)
	for _, result := range results {
		for _, change := range result.Changes {
			reconcile.RenderChange(cmd.ErrOrStderr(), change)
		}
		reconcile.RenderResult(cmd.OutOrStdout(), result, width)
	}
}

func runReposApply(cmd *cobra.Command, args []string) error {
	chosen, err := selectedDies(args)
	if err != nil {
		return err
	}
	walked, err := targets()
	if err != nil {
		return err
	}

	measurements := reconcile.AssessAll(walked, chosen)

	// The width comes from every row the run will print, so the column does not
	// widen halfway down as apply reaches a longer name.
	planned := make([]reconcile.Result, 0, len(measurements))
	for _, m := range measurements {
		planned = append(planned, m.Fold(reconcile.LensPlan))
	}
	width := reconcile.LabelWidth(planned)

	var results []reconcile.Result
	for i, m := range measurements {
		// The plan is printed before it is acted on, and what is acted on is
		// what was printed — Perform re-verifies live rather than the walk
		// taking a second look that may find something different.
		result := planned[i]
		for _, change := range result.Changes {
			reconcile.RenderChange(cmd.ErrOrStderr(), change)
		}

		for _, outcome := range reconcile.Apply(m) {
			reconcile.RenderOutcome(cmd.ErrOrStderr(), outcome)
			if !outcome.OK() {
				result.Status = reconcile.Issue
				result.Detail = outcome.Message
			}
		}

		reconcile.RenderResult(cmd.OutOrStdout(), result, width)
		results = append(results, result)
	}

	reconcile.RenderSummary(cmd.OutOrStdout(), results)

	// Drift is not a failure of apply — it is what apply just repaired — so
	// only an Issue reaches the exit code here.
	for _, result := range results {
		if result.Status == reconcile.Issue {
			return &reconcile.ExitError{Code: reconcile.ExitIssue}
		}
	}
	return nil
}
