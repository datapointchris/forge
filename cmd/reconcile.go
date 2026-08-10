package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/datapointchris/goselfupdate/cobracmd"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v6/config"
	"github.com/datapointchris/forge/v6/dies"
	"github.com/datapointchris/forge/v6/reconcile"
)

// reconcileNoun is one kind of target and the four verbs over it.
//
// Two nouns, one implementation. cli-design.md § "Two front doors on one dataset
// spell everything identically" is the rule, and a factory is how it stays true
// without anyone checking: repos and directories cannot drift in flag names,
// exit codes or output shape, because there is one of each.
//
// Flags live on the value rather than in package globals. The four commands
// under one noun previously shared a single &reposFilterNames, which works while
// there is one noun and silently crosses over the moment there are two.
type reconcileNoun struct {
	// name is the word typed: `forge repos`, `forge directories`.
	name string
	// one and many name what a row counts, for the confirmation's scale line.
	one, many string
	short     string
	long      string
	// resolve answers which targets this noun addresses, and the registry the
	// dies read fleet-level facts from.
	resolve func(names []string) ([]config.Repo, *config.SyncerConfig, error)
	// only are verbs this noun has and the other does not. The asymmetry is
	// deliberate and small: exec sweeps repos, run executes a directory's hooks
	// because nothing else will. Declared here so both stay visible in one place
	// rather than being attached from whichever file happens to define them.
	//
	// Built from the noun rather than referencing it: run needs the same
	// selection and flags these four use, and a verb reaching back for the
	// package variable is an initialization cycle the compiler rejects.
	only func(*reconcileNoun) []*cobra.Command

	filterNames []string
	asJSON      bool
	yes         bool
}

// command builds the noun's whole subtree.
func (n *reconcileNoun) command() *cobra.Command {
	root := &cobra.Command{
		Use:   n.name,
		Short: n.short,
		Long:  n.long,
		RunE:  requireSubcommand,
	}

	check := &cobra.Command{
		Use:               "check [die]",
		Short:             "Report what is wrong, which apply cannot fix",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeDieNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return n.runReconcile(cmd, args, reconcile.LensCheck)
		},
	}

	plan := &cobra.Command{
		Use:               "plan [die]",
		Short:             "Report what apply would change, writing nothing",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeDieNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return n.runReconcile(cmd, args, reconcile.LensPlan)
		},
	}

	apply := &cobra.Command{
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
		RunE:              n.runApply,
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the " + n.many + " a verb would act on",
		Long: `List the selected ` + n.many + `.

This is what --dry-run used to answer on a die run. It is its own question —
"which ` + n.many + `" — and it was never the same one as "what would change", which is
` + "`plan`" + `.`,
		Args: cobra.NoArgs,
		RunE: n.runList,
	}

	for _, cmd := range []*cobra.Command{check, plan, apply, list} {
		cmd.Flags().StringSliceVarP(&n.filterNames, "filter", "F", nil,
			"comma-separated "+n.one+" names to include")
		cmd.Flags().BoolVar(&n.asJSON, "json", false, "output as JSON to stdout")
		root.AddCommand(cmd)
	}
	apply.Flags().BoolVar(&n.yes, "yes", false, "apply without confirming, for the unaliased form")

	if n.only != nil {
		for _, cmd := range n.only(n) {
			root.AddCommand(cmd)
		}
	}

	return root
}

// selected answers which of this noun's members the filter names, and is where
// an empty selection becomes an error. Separate from targets because the verbs
// that need no die — `run` — must answer a typo the same way the ones that do
// already answer it, and they would otherwise pay for assets they never read.
func (n *reconcileNoun) selected() ([]config.Repo, *config.SyncerConfig, error) {
	selected, cfg, err := n.resolve(n.filterNames)
	if err != nil {
		return nil, nil, err
	}
	if len(selected) == 0 {
		// A usage error, not a runtime one: naming something that does not exist
		// is the one failure worth retrying with different arguments, and it is
		// almost always a typo or a shell that did not split the list.
		if len(n.filterNames) > 0 {
			return nil, nil, cobracmd.UsageError(fmt.Errorf("no %s matched: %s",
				n.many, strings.Join(n.filterNames, ", ")))
		}
		return nil, nil, cobracmd.UsageError(fmt.Errorf("no %s are declared", n.many))
	}
	return selected, cfg, nil
}

// targets resolves what to walk, with the assets every die may need.
func (n *reconcileNoun) targets() ([]reconcile.Target, error) {
	selected, cfg, err := n.selected()
	if err != nil {
		return nil, err
	}

	assets, err := loadAssets()
	if err != nil {
		return nil, err
	}

	walked := make([]reconcile.Target, 0, len(selected))
	for _, target := range selected {
		walked = append(walked, reconcile.Target{Repo: target, Assets: assets, Config: cfg})
	}
	return walked, nil
}

func (n *reconcileNoun) runReconcile(cmd *cobra.Command, args []string, lens reconcile.Lens) error {
	chosen, err := selectedDies(args)
	if err != nil {
		return err
	}
	walked, err := n.targets()
	if err != nil {
		return err
	}

	measurements := reconcile.AssessAll(walked, chosen)

	results := make([]reconcile.Result, 0, len(measurements))
	for _, m := range measurements {
		results = append(results, m.Fold(lens))
	}

	if n.asJSON {
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

func (n *reconcileNoun) runApply(cmd *cobra.Command, args []string) error {
	chosen, err := selectedDies(args)
	if err != nil {
		return err
	}
	walked, err := n.targets()
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
	if !n.asJSON {
		render(cmd, planned)
	}

	if err := n.confirmApply(cmd, args, planned); err != nil {
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

	if n.asJSON {
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
func (n *reconcileNoun) confirmApply(cmd *cobra.Command, args []string, planned []reconcile.Result) error {
	if len(args) > 0 || n.yes {
		return nil
	}

	pending, targets := pendingSummary(planned)
	if pending == 0 {
		return nil
	}
	scale := fmt.Sprintf("%s across %s",
		plural(pending, "change", "changes"), plural(targets, n.one, n.many))

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

func (n *reconcileNoun) runList(cmd *cobra.Command, _ []string) error {
	selected, _, err := n.resolve(n.filterNames)
	if err != nil {
		return err
	}

	if n.asJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(selected)
	}

	for _, target := range selected {
		row(cmd.OutOrStdout(), "%s\n", target.Name)
	}
	return nil
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

// render writes the rows: a target's summary to stdout, the items it is a
// summary of to stderr. Below a composite verb the items are the evidence for
// the row that follows, not the answer a caller parses — which is --json.
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

// pendingSummary counts what apply would do, and across how many targets.
func pendingSummary(planned []reconcile.Result) (changes, targets int) {
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
