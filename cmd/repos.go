package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/datapointchris/goselfupdate/cobracmd"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v6/dies"
	"github.com/datapointchris/forge/v6/reconcile"
	"github.com/datapointchris/forge/v6/runner"
	"github.com/datapointchris/forge/v6/toolchain"
)

var (
	reposFilterNames []string
	reposJSON        bool
	reposYes         bool
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
	Use:   "apply [die]",
	Short: "Make the pending changes",
	Long: `Make the changes plan reported.

Naming a die applies that one. Omitting it applies them all, which is the other
half of the terraform loop and is gated by a confirmation rather than by being
untypeable — the plan printed above the prompt is what you are confirming.

Only Automatic repairs are ever performed. A finding check reports is
structurally unreachable from here, whether or not a die was named.`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeDieNames,
	RunE:              runReposApply,
}

func init() {
	for _, cmd := range []*cobra.Command{reposCheckCmd, reposPlanCmd, reposApplyCmd, reposListCmd} {
		cmd.Flags().StringSliceVarP(&reposFilterNames, "filter", "F", nil, "comma-separated repo names to include")
		cmd.Flags().BoolVar(&reposJSON, "json", false, "output as JSON to stdout")
		reposCmd.AddCommand(cmd)
	}
	reposApplyCmd.Flags().BoolVar(&reposYes, "yes", false, "apply without confirming, for the unaliased form")

	rootCmd.AddCommand(reposCmd)
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
		// A usage error, not a runtime one: naming a repo that does not exist is
		// the one failure worth retrying with different arguments, and it is
		// almost always a typo or a shell that did not split the list.
		return nil, cobracmd.UsageError(fmt.Errorf("no repos matched: %s", strings.Join(reposFilterNames, ", ")))
	}

	assets, err := loadAssets()
	if err != nil {
		return nil, err
	}

	selected := make([]reconcile.Target, 0, len(repos))
	for _, repo := range repos {
		selected = append(selected, reconcile.Target{Repo: repo, Assets: assets, Config: cfg})
	}
	return selected, nil
}

// resolvePreCommitFS roots an fs.FS at the pre-commit asset directory, the
// parent of both the blocks and the toolchain manifest.
func resolvePreCommitFS() (fs.FS, error) {
	assetsFS, err := fs.Sub(embeddedPreCommit, "pre-commit")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded assets: %w", err)
	}
	return assetsFS, nil
}

// resolveCIBlocksFS roots an fs.FS at the CI blocks directory.
func resolveCIBlocksFS() (fs.FS, error) {
	blocksFS, err := fs.Sub(embeddedCI, "ci/blocks")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded CI blocks: %w", err)
	}
	return blocksFS, nil
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
		recordReconcileRun(chosen, results)
		return reconcile.ExitFor(results)
	}

	render(cmd, results)
	reconcile.RenderSummary(cmd.OutOrStdout(), results)
	recordReconcileRun(chosen, results)
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

// recordReconcileRun files one record per die, so `forge dies stats` keeps
// answering which operations are used even when a walk covered several at once.
func recordReconcileRun(chosen []reconcile.Die, results []reconcile.Result) {
	for _, die := range chosen {
		var mine []reconcile.Result
		for _, result := range results {
			if result.Die == die.Name() {
				mine = append(mine, result)
			}
		}
		if len(mine) > 0 {
			recordRun(die.Name(), mine)
		}
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

	// The plan is printed in full before anything is written, and what is acted
	// on is what was printed — Perform re-verifies live rather than the walk
	// taking a second look that may have found something different.
	planned := make([]reconcile.Result, 0, len(measurements))
	for _, m := range measurements {
		planned = append(planned, m.Fold(reconcile.LensPlan))
	}
	width := reconcile.LabelWidth(planned)
	if !reposJSON {
		render(cmd, planned)
	}

	if err := confirmApply(cmd, args, planned); err != nil {
		return err
	}

	var results []reconcile.Result
	for i, m := range measurements {
		result := planned[i]
		outcomes := reconcile.Apply(m)
		for _, outcome := range outcomes {
			reconcile.RenderOutcome(cmd.ErrOrStderr(), outcome)
			if !outcome.OK() {
				result.Status = reconcile.Issue
				result.Detail = outcome.Message
			}
		}
		if len(outcomes) > 0 {
			reconcile.RenderResult(cmd.OutOrStdout(), result, width)
		}
		results = append(results, result)
	}

	if reposJSON {
		if err := reconcile.EmitJSON(cmd.OutOrStdout(), results); err != nil {
			return err
		}
	} else {
		reconcile.RenderSummary(cmd.OutOrStdout(), results)
	}
	recordReconcileRun(chosen, results)

	// Drift is not a failure of apply — it is what apply just repaired — so only
	// an Issue reaches the exit code here.
	for _, result := range results {
		if result.Status == reconcile.Issue {
			return &reconcile.ExitError{Code: reconcile.ExitIssue}
		}
	}
	return nil
}

// confirmApply gates the unaliased form, where the blast radius is every
// operation forge knows, everywhere.
//
// Naming a die is its own confirmation — the word is the scope — so that form
// keeps running unprompted. cli-design.md scales friction to blast radius, and
// nothing here is destructive in the sense that rule bites on: no die commits,
// so every file-shaped change lands as an unstaged diff. What earns the prompt
// is size, which is why it prints a count rather than a warning.
func confirmApply(cmd *cobra.Command, args []string, planned []reconcile.Result) error {
	if len(args) > 0 || reposYes {
		return nil
	}

	pending, repos := pendingSummary(planned)
	if pending == 0 {
		return nil
	}
	scale := fmt.Sprintf("%s across %s",
		plural(pending, "change", "changes"), plural(repos, "repo", "repos"))

	// Never block on a closed stdin: a prompt waiting on one deadlocks the
	// caller with no output and no exit code.
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return cobracmd.UsageError(fmt.Errorf("%s pending; pass --yes to apply non-interactively", scale))
	}

	row(cmd.ErrOrStderr(), "\napply %s? [y/N] ", scale)
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && answer == "" {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		// Said out loud rather than exiting silently: a prompt that takes an
		// answer and prints nothing back leaves the operator unsure whether it
		// was read, which is the state a confirmation exists to remove.
		row(cmd.ErrOrStderr(), "canceled — nothing was applied\n")
		return cobracmd.ErrReported
	}
	return nil
}

// pendingSummary counts what apply would do, and across how many repos.
func pendingSummary(planned []reconcile.Result) (changes, repos int) {
	seen := map[string]bool{}
	for _, result := range planned {
		changes += result.Pending
		if result.Pending > 0 {
			seen[result.Repo] = true
		}
	}
	return changes, len(seen)
}

// plural renders a count with its noun. Both forms are named by the caller,
// because English inflection is not derivable from the singular.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

var reposListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the repos a verb would act on",
	Long: `List the selected repos.

This is what --dry-run used to answer on a die run. It is its own question —
"which repos" — and it was never the same one as "what would change", which is
` + "`plan`" + `.`,
	Args: cobra.NoArgs,
	RunE: runReposList,
}

func runReposList(cmd *cobra.Command, _ []string) error {
	cfg, err := loadRepos()
	if err != nil {
		return err
	}

	selected := runner.SelectRepos(cfg.Repos, reposFilterNames)
	if reposJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(selected)
	}

	for _, repo := range selected {
		row(cmd.OutOrStdout(), "%s\n", repo.Name)
	}
	return nil
}
