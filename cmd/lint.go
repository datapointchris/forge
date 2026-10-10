package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/lint"
)

var lintCmd = &cobra.Command{
	Use:   "lint [repo...]",
	Short: "Run each repo's standard pre-commit hooks over every file",
	Long: `Run every hook a standard block put in each repo's committed
.pre-commit-config.yaml, over every file the repo tracks.

The hooks are the ones the repo's generated CI runs, read the same way. CI
runs them over what a push changed, and this runs them over every file, so it
can fail where CI passes. A hook in a custom section is not run: some need a
workstation, such as one driving an editor or a local stack.

Each repo is linted in a throwaway clone of its HEAD under forge's cache,
because several hooks rewrite what they check. The checkout is never written,
so an uncommitted change is not what gets linted.

Four outcomes, not two. ` + "`no_hooks`" + ` is a repo with no committed config, or none
of forge's hooks in it. ` + "`unknown`" + ` is a hook whose tool is not on this machine,
a hook environment pre-commit could not set up, or a repo that ran out of time.
Nothing is installed to fix that: it is a fact about this machine rather than
about the code.

Exit 1 if any hook failed. ` + "`unknown`" + ` does not fail the run, because an
unmeasurable item is not drift.`,
	Example: "  forge lint\n  forge lint alpha beta\n  forge lint --failed",
	RunE:    runLint,
}

var (
	lintJSON       bool
	lintFailedOnly bool
	lintJobs       int
)

func init() {
	lintCmd.Flags().BoolVar(&lintJSON, "json", false, "Output as JSON to stdout")
	lintCmd.Flags().BoolVar(&lintFailedOnly, "failed", false, "Print each failing hook's output, rather than nothing")
	lintCmd.Flags().IntVarP(&lintJobs, "jobs", "j", 0, "Repos to lint at once; 0 is half the CPUs")
	addRegistryFlag(lintCmd)
	rootCmd.AddCommand(lintCmd)
}

func runLint(cmd *cobra.Command, args []string) error {
	cfg, err := loadRepos(cmd)
	if err != nil {
		return fmt.Errorf("load repo registry: %w", err)
	}
	selected, err := selectRepos(cfg.Repos, args)
	if err != nil {
		return err
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })

	// Hooks lead their own process groups, so only this stops them on a Ctrl-C.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	results := lint.RunRepos(ctx, selected, lintJobs)
	elapsed := time.Since(started).Round(time.Millisecond).Seconds()

	if lintJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if results == nil {
			results = []lint.Result{}
		}
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		writeLintResults(cmd, results, elapsed)
	}

	if ctx.Err() != nil {
		return errors.New("interrupted: the rows above are partial")
	}
	return lintVerdict(results)
}

// lintVerdict is the run's answer whichever form it was rendered in, for the
// reason verdict gives.
func lintVerdict(results []lint.Result) error {
	for _, result := range results {
		if result.Outcome == lint.Failed {
			return goclikit.ErrReported
		}
	}
	return nil
}

func writeLintResults(cmd *cobra.Command, results []lint.Result, elapsed float64) {
	out := cmd.OutOrStdout()
	counts := map[lint.Outcome]int{}
	var seconds float64

	for _, result := range results {
		counts[result.Outcome]++
		seconds += result.Seconds
		note := result.Note
		if note != "" {
			note = "  " + note
		}
		_, _ = fmt.Fprintf(out, "%-9s %-28s %6.1fs%s\n", result.Outcome, result.Repo, result.Seconds, note)
	}

	_, _ = fmt.Fprintf(out, "\n%d passed  %d failed  %d unknown  %d no hooks   %.1fs elapsed, %.1fs of lint time\n",
		counts[lint.Passed], counts[lint.Failed], counts[lint.Unknown], counts[lint.NoHooks], elapsed, seconds)

	if !lintFailedOnly {
		return
	}
	for _, result := range results {
		for _, hook := range result.Hooks {
			if hook.Outcome != lint.Failed || hook.Output == "" {
				continue
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n─── %s %s ───\n%s\n", result.Repo, hook.ID, hook.Output)
		}
	}
}
