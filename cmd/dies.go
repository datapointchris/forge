package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v5/config"
	"github.com/datapointchris/forge/v5/dies"
	"github.com/datapointchris/forge/v5/reconcile"
)

var (
	diesStatsSince string
	diesStatsJSON  bool
)

// diesCmd is the library, and it executes nothing.
//
// Scope is structural: `forge dies` answers what exists, `forge repos` acts on
// repos. `dies run` was the one verb crossing that line, and it is
// `forge repos apply` now — a die is what gets applied, not what does the
// applying.
var diesCmd = &cobra.Command{
	Use:   "dies",
	Short: "Browse the reusable repo operations",
	Long: `Browse the dies — the reusable operations forge applies to repos.

This is the library. To run one, use a reconcile verb, which takes a die name:

  forge repos plan <die>     what apply would change
  forge repos check <die>    what is wrong that apply cannot fix
  forge repos apply <die>    make it so`,
	RunE: requireSubcommand,
}

var diesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the dies",
	Args:  cobra.NoArgs,
	RunE:  runDiesList,
}

var diesShowCmd = &cobra.Command{
	Use:               "show <die>",
	Short:             "Show one die in full",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeDieNames,
	RunE:              runDiesShow,
}

var diesSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search dies by name, description, or tags",
	Args:  cobra.ExactArgs(1),
	RunE:  runDiesSearch,
}

var diesStatsCmd = &cobra.Command{
	Use:               "stats [die]",
	Short:             "Show execution history",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeDieNames,
	RunE:              runDiesStats,
}

func init() {
	diesStatsCmd.Flags().StringVar(&diesStatsSince, "since", "", "only runs at or after this point: \"2 weeks\", \"30 days\", 72h, or 2026-07-01")
	diesStatsCmd.Flags().BoolVar(&diesStatsJSON, "json", false, "output as JSON to stdout")

	diesCmd.AddCommand(diesListCmd, diesShowCmd, diesSearchCmd, diesStatsCmd)
	rootCmd.AddCommand(diesCmd)
}

func completeDieNames(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return dies.BuiltinNames(), cobra.ShellCompDirectiveNoFileComp
}

func runDiesList(cmd *cobra.Command, _ []string) error {
	summaries := dieSummaries()
	cyan := color.New(color.FgHiCyan)
	dim := color.New(color.Faint)

	out := cmd.OutOrStdout()
	row(out, "\n")
	for _, die := range dies.Builtin() {
		row(out, "  %s %s\n", cyan.Sprintf("%-20s", die.Name()), die.Description())
		if s, ok := summaries[die.Name()]; ok {
			row(out, "  %-20s %s\n", "", dim.Sprintf("runs: %d │ last: %s", s.RunCount, s.LastRun.Format("2006-01-02 15:04")))
		}
	}
	row(out, "\n")
	return nil
}

func runDiesShow(cmd *cobra.Command, args []string) error {
	die, err := dies.Named(args[0])
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	boldCyan := color.New(color.FgHiCyan, color.Bold)
	yellow := color.New(color.FgHiYellow)

	row(out, "\n%s\n", boldCyan.Sprint(die.Name()))
	row(out, "%s\n", color.New(color.FgHiCyan).Sprint(strings.Repeat("─", len(die.Name()))))
	row(out, "  %s %s\n", yellow.Sprint("Description:"), die.Description())
	row(out, "  %s        %s\n", yellow.Sprint("Tags:"), strings.Join(die.Tags(), ", "))

	if s, ok := dieSummaries()[die.Name()]; ok {
		row(out, "  %s        %d (last %s)\n", yellow.Sprint("Runs:"), s.RunCount, s.LastRun.Format("2006-01-02 15:04"))
	}
	row(out, "\n")
	return nil
}

func runDiesSearch(cmd *cobra.Command, args []string) error {
	matches := dies.SearchBuiltin(args[0])
	out := cmd.OutOrStdout()
	if len(matches) == 0 {
		row(out, "no dies match %q\n", args[0])
		return nil
	}

	cyan := color.New(color.FgHiCyan)
	for _, die := range matches {
		row(out, "  %s %s\n", cyan.Sprintf("%-20s", die.Name()), die.Description())
	}
	return nil
}

// row writes one console line, discarding the write error: there is nothing a
// caller can do about a failed write to a terminal that has already gone away.
func row(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func dieSummaries() map[string]dies.DieSummary {
	statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
	if err != nil {
		return nil
	}
	records, err := dies.LoadStats(statsPath)
	if err != nil || len(records) == 0 {
		return nil
	}
	return dies.SummaryByDie(records)
}

// recordRun appends what a reconcile run did, so `dies stats` answers which
// operations are actually used and when each last ran.
//
// Verdicts rather than the old OK/SKIP/FAIL counts, which came from exit codes
// no die produces any more. The field names stay so records written before this
// still load.
func recordRun(dieName string, results []reconcile.Result) {
	statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
	if err != nil {
		return
	}

	record := dies.RunRecord{Die: dieName, Timestamp: time.Now().UTC(), Results: map[string]string{}}
	for _, result := range results {
		record.Results[result.Repo] = string(result.Status)
		switch result.Status {
		case reconcile.Converged:
			record.OK++
		case reconcile.Drift:
			record.Skip++
		case reconcile.Issue:
			record.Fail++
		}
	}

	_ = dies.RecordRun(statsPath, record)
}

func runDiesStats(cmd *cobra.Command, args []string) error {
	statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
	if err != nil {
		return fmt.Errorf("expanding stats path: %w", err)
	}

	records, err := dies.LoadStats(statsPath)
	if err != nil {
		return err
	}

	window := ""
	if diesStatsSince != "" {
		cutoff, err := dies.ParseSince(diesStatsSince, time.Now())
		if err != nil {
			return err
		}
		records = dies.Since(records, cutoff)
		window = fmt.Sprintf(" since %s", cutoff.Format("2006-01-02"))
	}

	if len(args) == 1 {
		records = dies.StatsForDie(records, args[0])
	}

	if len(records) == 0 {
		if diesStatsJSON {
			return emitDiesStatsJSON(cmd, args, nil, nil)
		}
		switch {
		case len(args) == 1:
			fmt.Printf("No execution history for %s%s\n", args[0], window)
		default:
			fmt.Printf("No execution history found%s.\n", window)
		}
		return nil
	}

	dies.SortNewestFirst(records)

	if len(args) == 1 {
		if diesStatsJSON {
			return emitDiesStatsJSON(cmd, args, records, nil)
		}
		printDieRunHistory(args[0], window, records)
		return nil
	}

	aggregated := dies.AggregateByDie(records)
	if diesStatsJSON {
		return emitDiesStatsJSON(cmd, args, nil, aggregated)
	}
	printDieStatsSummary(window, aggregated)
	return nil
}

// emitDiesStatsJSON keeps one schema per view: the per-die argument returns the
// runs themselves, the default returns the aggregate the table shows.
func emitDiesStatsJSON(cmd *cobra.Command, args []string, records []dies.RunRecord, aggregated []dies.DieStats) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if len(args) == 1 {
		if records == nil {
			records = []dies.RunRecord{}
		}
		return enc.Encode(records)
	}
	if aggregated == nil {
		aggregated = []dies.DieStats{}
	}
	return enc.Encode(aggregated)
}

func printDieStatsSummary(window string, aggregated []dies.DieStats) {
	bold := color.New(color.Bold)
	cyan := color.New(color.FgHiCyan)
	dim := color.New(color.Faint)
	green := color.New(color.FgHiGreen)
	yellow := color.New(color.FgHiYellow)
	red := color.New(color.FgHiRed)

	if window != "" {
		fmt.Printf("\nRuns%s\n", window)
	}
	bold.Printf("\n%-50s %5s %-16s %5s %5s %5s\n", "Die", "Runs", "Last run", "OK", "Skip", "Fail")
	dim.Printf("%s\n", strings.Repeat("─", 92))

	for _, s := range aggregated {
		fmt.Printf("%s %s %s %s %s %s\n",
			cyan.Sprintf("%-50s", s.Die),
			bold.Sprintf("%5d", s.Runs),
			s.LastRun.Format("2006-01-02 15:04"),
			green.Sprintf("%5d", s.OK),
			yellow.Sprintf("%5d", s.Skip),
			red.Sprintf("%5d", s.Fail))
	}

	dim.Printf("\nPer-run history: forge dies stats <die-path>\n\n")
}

func printDieRunHistory(diePath, window string, records []dies.RunRecord) {
	bold := color.New(color.Bold)
	cyan := color.New(color.FgHiCyan)
	dim := color.New(color.Faint)
	green := color.New(color.FgHiGreen)
	yellow := color.New(color.FgHiYellow)
	red := color.New(color.FgHiRed)

	cyan.Printf("\n%s%s\n", diePath, window)
	bold.Printf("%-16s %5s %5s %5s\n", "Run", "OK", "Skip", "Fail")
	dim.Printf("%s\n", strings.Repeat("─", 34))

	for _, r := range records {
		fmt.Printf("%s %s %s %s\n",
			r.Timestamp.Format("2006-01-02 15:04"),
			green.Sprintf("%5d", r.OK),
			yellow.Sprintf("%5d", r.Skip),
			red.Sprintf("%5d", r.Fail))
	}
	fmt.Println()
}
