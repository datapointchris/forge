package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/datapointchris/goselfupdate/cobracmd"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/runner"
	"github.com/datapointchris/forge/suites"
)

var testCmd = &cobra.Command{
	Use:   "test [repo...]",
	Short: "Run each repo's declared components' test suites",
	Long: `Run the tests of every component the registry declares for a repo.

The command per stack is the one ci/blocks/ generates into that repo's own
workflow, so a local run and CI cannot disagree about what "the tests" means.
Vue is the exception — its CI block builds and lints without testing — so there
the component's own package.json says what to run.

Four outcomes, not two. ` + "`no_suite`" + ` is a repo with no tests yet, which is not a
failing repo, and pytest's exit 5 lands here. ` + "`unknown`" + ` is a component that
could not be measured at all — a missing runner, absent node_modules, a timeout.
Nothing is installed to fix that: it is a fact about this machine rather than
about the code, and repairing it is dotfiles' job.

Exit 1 if anything failed. ` + "`unknown`" + ` does not fail the run, for the reason
~/dev/architecture/status-layers.md gives: an unmeasurable item is not drift and
must not move an exit code, or a machine missing one runner reports a screen of
failures.`,
	Example: "  forge test\n  forge test fleet forge\n  forge test --json",
	RunE:    runTest,
}

var (
	testJSON       bool
	testFailedOnly bool
	testJobs       int
)

func init() {
	testCmd.Flags().BoolVar(&testJSON, "json", false, "Output as JSON to stdout")
	testCmd.Flags().BoolVar(&testFailedOnly, "failed", false, "Print captured output for failures only, rather than nothing")
	testCmd.Flags().IntVarP(&testJobs, "jobs", "j", 0, "Repos to test at once; 0 is one per CPU")
	rootCmd.AddCommand(testCmd)
}

func runTest(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadRepos()
	if err != nil {
		return fmt.Errorf("load repo registry: %w", err)
	}
	selected := runner.SelectRepos(runner.ActiveRepos(cfg.Repos), args)
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })

	started := time.Now()
	results := suites.RunRepos(selected, testJobs)
	elapsed := time.Since(started).Round(time.Millisecond).Seconds()

	if testJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if results == nil {
			results = []suites.Result{}
		}
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		writeResults(cmd, results, elapsed)
	}

	return verdict(results)
}

// verdict is the run's answer, reached the same way whichever form it was
// rendered in.
//
// Its own function because the two render paths returning separately is what
// broke: `--json` exited 0 on a failing suite while the text form exited 1, so a
// caller reading the exit code — which cli-design.md § "Machine contract" calls
// the API — got the opposite answer depending on a formatting flag. `fleet test`
// recorded exactly that wrong answer on its first run.
//
// ErrReported rather than a message: the failing rows are already printed, and a
// second line would report the same failure twice.
func verdict(results []suites.Result) error {
	for _, result := range results {
		if result.Outcome == suites.Failed {
			return cobracmd.ErrReported
		}
	}
	return nil
}

func writeResults(cmd *cobra.Command, results []suites.Result, elapsed float64) {
	out := cmd.OutOrStdout()
	counts := map[suites.Outcome]int{}
	var seconds float64

	for _, result := range results {
		counts[result.Outcome]++
		seconds += result.Seconds

		where := result.Repo
		if result.Dir != "" && result.Dir != "." {
			where += "/" + result.Dir
		}
		note := result.Note
		if note != "" {
			note = "  " + note
		}
		_, _ = fmt.Fprintf(out, "%-9s %-28s %-7s %6.1fs%s\n", result.Outcome, where, result.Stack, result.Seconds, note)
	}

	// Both numbers, because they stopped being the same once repos ran together
	// and the sum alone would read as the run having taken far longer than it did.
	_, _ = fmt.Fprintf(out, "\n%d passed  %d failed  %d unknown  %d no suite   %.1fs elapsed, %.1fs of suite time\n",
		counts[suites.Passed], counts[suites.Failed], counts[suites.Unknown], counts[suites.NoSuite], elapsed, seconds)

	if !testFailedOnly {
		return
	}
	for _, result := range results {
		if result.Outcome != suites.Failed || result.Output == "" {
			continue
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n─── %s %s ───\n%s\n", result.Repo, result.Dir, result.Output)
	}
}
