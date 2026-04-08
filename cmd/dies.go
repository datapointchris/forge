package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/assets"
	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/dies"
	"github.com/datapointchris/forge/runner"
)

var (
	diesFilterNames []string
	diesDryRun      bool
)

var diesCmd = &cobra.Command{
	Use:   "dies",
	Short: "Manage and run dies (reusable scripts for repos)",
	Long: `Browse, search, run, and track dies — reusable scripts executed across repos.

A die is a script in the dies directory, organized by category (subdirectory).
Use 'forge dies list' to see available dies, or 'forge dies run' to execute one.`,
}

var diesListCmd = &cobra.Command{
	Use:   "list [category]",
	Short: "List available dies",
	Args:  cobra.MaximumNArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := loadDiesRegistry()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return reg.Categories(), cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runDiesList,
}

var diesRunCmd = &cobra.Command{
	Use:   "run <die-path>",
	Short: "Run a die across repos",
	Long: `Execute a die script across all (or filtered) repos.

Example:
  forge dies run maintenance/add-planning-to-gitignore.sh
  forge dies run maintenance/add-planning-to-gitignore.sh -F dotfiles,homelab
  forge dies run maintenance/add-planning-to-gitignore.sh -n`,
	Args: cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := loadDiesRegistry()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return reg.AllDiePaths(), cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runDiesRun,
}

var diesShowCmd = &cobra.Command{
	Use:   "show <die-path>",
	Short: "Show details about a die",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := loadDiesRegistry()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return reg.AllDiePaths(), cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runDiesShow,
}

var diesSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search dies by name, description, or tags",
	Args:  cobra.ExactArgs(1),
	RunE:  runDiesSearch,
}

var diesStatsCmd = &cobra.Command{
	Use:   "stats [die-path]",
	Short: "Show execution history",
	Args:  cobra.MaximumNArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := loadDiesRegistry()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return reg.AllDiePaths(), cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runDiesStats,
}

func init() {
	diesRunCmd.Flags().StringSliceVarP(&diesFilterNames, "filter", "F", nil, "comma-separated repo names to include")
	diesRunCmd.Flags().BoolVarP(&diesDryRun, "dry-run", "n", false, "show which repos would be affected without executing")

	diesCmd.AddCommand(diesListCmd)
	diesCmd.AddCommand(diesRunCmd)
	diesCmd.AddCommand(diesShowCmd)
	diesCmd.AddCommand(diesSearchCmd)
	diesCmd.AddCommand(diesStatsCmd)
	rootCmd.AddCommand(diesCmd)
}

func loadDiesRegistry() (*dies.Registry, error) {
	if diesDir := os.Getenv("FORGE_DIES_DIR"); diesDir != "" {
		return dies.LoadRegistry(os.DirFS(diesDir))
	}
	diesFS, err := fs.Sub(embeddedDies, "dies")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded dies: %w", err)
	}
	return dies.LoadRegistry(diesFS)
}

func runDiesList(cmd *cobra.Command, args []string) error {
	reg, err := loadDiesRegistry()
	if err != nil {
		return err
	}

	var categoryFilter string
	if len(args) == 1 {
		categoryFilter = args[0]
	}

	grouped := reg.ByCategory(categoryFilter)
	if len(grouped) == 0 {
		if categoryFilter != "" {
			return fmt.Errorf("no dies found in category: %s", categoryFilter)
		}
		fmt.Println("No dies found.")
		return nil
	}

	var summaries map[string]dies.DieSummary
	statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
	if err == nil {
		records, err := dies.LoadStats(statsPath)
		if err == nil && len(records) > 0 {
			summaries = dies.SummaryByDie(records)
		}
	}

	cats := make([]string, 0, len(grouped))
	for c := range grouped {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	boldMagenta := color.New(color.FgHiMagenta, color.Bold)
	magenta := color.New(color.FgHiMagenta)
	cyan := color.New(color.FgHiCyan)
	dim := color.New(color.Faint)

	for _, cat := range cats {
		boldMagenta.Printf("\n%s\n", cat)
		magenta.Printf("%s\n", strings.Repeat("─", len(cat)))
		for _, name := range grouped[cat] {
			die := reg.Dies[name]
			base := filepath.Base(name)

			desc := die.Description
			if desc == "" {
				desc = dim.Sprint("(no description)")
			}

			fmt.Printf("  %s %s\n", cyan.Sprintf("%-40s", base), desc)

			if s, ok := summaries[name]; ok {
				fmt.Printf("  %-40s %s\n", "", dim.Sprintf("runs: %d │ last: %s", s.RunCount, s.LastRun.Format("2006-01-02 15:04")))
			}
		}
	}
	fmt.Println()

	return nil
}

func runDiesRun(cmd *cobra.Command, args []string) error {
	diePath := args[0]

	// Resolve die source: filesystem (dies_dir configured) or embedded
	scriptPath, env, cleanup, err := resolveDie(diePath)
	if err != nil {
		return err
	}
	defer cleanup()

	var syncerCfg *config.SyncerConfig
	if cfgPath != "" {
		syncerCfg, err = config.LoadSyncerConfig(cfgPath)
	} else {
		syncerCfg, err = config.LoadReposFromForgeConfig()
	}
	if err != nil {
		return err
	}

	repos := runner.FilterRepos(runner.ActiveRepos(syncerCfg.Repos), diesFilterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(diesFilterNames, ", "))
	}

	opts := runner.Opts{
		ScriptFile:    scriptPath,
		Env:           env,
		DryRun:        diesDryRun,
		CaptureOutput: true,
	}

	if diesDryRun {
		runner.PrintDryRunHeader()
	}

	repoResults := make(map[string]string)
	var results []runner.Result
	for _, repo := range repos {
		r := runner.ExecuteInRepo(repo, opts)
		results = append(results, r)
		repoResults[r.Name] = r.Status
	}

	if diesDryRun {
		runner.PrintDryRunFooter()
	}

	if !diesDryRun {
		printGroupedResults(results)
	}

	runner.PrintSummary(results)
	runner.PrintFailures(results)

	if !diesDryRun {
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

		statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
		if err != nil {
			return fmt.Errorf("expanding stats path: %w", err)
		}

		record := dies.RunRecord{
			Die:       diePath,
			Timestamp: time.Now().UTC(),
			Results:   repoResults,
			OK:        ok,
			Skip:      skip,
			Fail:      fail,
		}
		if err := dies.RecordRun(statsPath, record); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to record stats: %v\n", err)
		}
	}

	return nil
}

// resolveDie returns the script path, env vars, and cleanup function for a die.
// When dies_dir is configured, it uses the filesystem directly.
// Otherwise, it extracts from embedded assets.
func resolveDie(diePath string) (scriptPath string, env []string, cleanup func(), err error) {
	noop := func() {}

	if diesDir := os.Getenv("FORGE_DIES_DIR"); diesDir != "" {
		// Filesystem mode: use FORGE_DIES_DIR env var
		reg, err := dies.LoadRegistry(os.DirFS(diesDir))
		if err != nil {
			return "", nil, noop, err
		}
		absScript, err := reg.Resolve(diesDir, diePath)
		if err != nil {
			return "", nil, noop, err
		}
		return absScript, nil, noop, nil
	}

	// Embedded mode: extract to temp files
	diesFS, err := fs.Sub(embeddedDies, "dies")
	if err != nil {
		return "", nil, noop, fmt.Errorf("accessing embedded dies: %w", err)
	}

	reg, err := dies.LoadRegistry(diesFS)
	if err != nil {
		return "", nil, noop, err
	}
	if _, ok := reg.Dies[diePath]; !ok {
		return "", nil, noop, fmt.Errorf("die not found: %s", diePath)
	}

	mgr := assets.NewManager(embeddedDies, embeddedPreCommit)

	script, err := mgr.ExtractScript(diePath)
	if err != nil {
		mgr.Cleanup()
		return "", nil, noop, err
	}

	dataDir, err := mgr.DataDir()
	if err != nil {
		mgr.Cleanup()
		return "", nil, noop, err
	}

	return script, []string{"FORGE_DATA_DIR=" + dataDir}, mgr.Cleanup, nil
}

func runDiesShow(cmd *cobra.Command, args []string) error {
	reg, err := loadDiesRegistry()
	if err != nil {
		return err
	}

	diePath := args[0]
	die, ok := reg.Dies[diePath]
	if !ok {
		return fmt.Errorf("die not found: %s", diePath)
	}

	boldCyan := color.New(color.FgHiCyan, color.Bold)
	cyan := color.New(color.FgHiCyan)
	yellow := color.New(color.FgHiYellow)
	dim := color.New(color.Faint)
	green := color.New(color.FgHiGreen)
	red := color.New(color.FgHiRed)

	boldCyan.Printf("\n%s\n", diePath)
	cyan.Printf("%s\n", strings.Repeat("─", len(diePath)))

	if die.Description != "" {
		fmt.Printf("  %s %s\n", yellow.Sprint("Description:"), die.Description)
	}
	if len(die.Tags) > 0 {
		fmt.Printf("  %s        %s\n", yellow.Sprint("Tags:"), strings.Join(die.Tags, ", "))
	}
	if !die.Registered {
		fmt.Printf("  %s      %s\n", yellow.Sprint("Status:"), dim.Sprint("unregistered (add to registry.yml for metadata)"))
	}

	statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
	if err == nil {
		records, err := dies.LoadStats(statsPath)
		if err == nil {
			filtered := dies.StatsForDie(records, diePath)
			if len(filtered) > 0 {
				last := filtered[len(filtered)-1]
				fmt.Printf("  %s    %s (%s, %s, %s)\n",
					yellow.Sprint("Last run:"),
					last.Timestamp.Format("2006-01-02 15:04"),
					green.Sprintf("%d ok", last.OK),
					yellow.Sprintf("%d skip", last.Skip),
					red.Sprintf("%d fail", last.Fail))
				fmt.Printf("  %s  %d\n", yellow.Sprint("Total runs:"), len(filtered))
			}
		}
	}

	fmt.Println()
	return nil
}

func runDiesSearch(cmd *cobra.Command, args []string) error {
	reg, err := loadDiesRegistry()
	if err != nil {
		return err
	}

	matches := reg.Search(args[0])
	if len(matches) == 0 {
		fmt.Printf("No dies matching %q\n", args[0])
		return nil
	}

	bold := color.New(color.Bold)
	cyan := color.New(color.FgHiCyan)

	fmt.Printf("\nResults for %s:\n\n", bold.Sprintf("%q", args[0]))
	for _, name := range matches {
		die := reg.Dies[name]
		if die.Description != "" {
			fmt.Printf("  %s %s\n", cyan.Sprintf("%-45s", name), die.Description)
		} else {
			cyan.Printf("  %s\n", name)
		}
	}
	fmt.Println()

	return nil
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

	if len(records) == 0 {
		fmt.Println("No execution history found.")
		return nil
	}

	if len(args) == 1 {
		records = dies.StatsForDie(records, args[0])
		if len(records) == 0 {
			fmt.Printf("No execution history for %s\n", args[0])
			return nil
		}
	}

	bold := color.New(color.Bold)
	cyan := color.New(color.FgHiCyan)
	dim := color.New(color.Faint)
	green := color.New(color.FgHiGreen)
	yellow := color.New(color.FgHiYellow)
	red := color.New(color.FgHiRed)

	bold.Printf("\n%-50s %-20s %4s %4s %4s\n", "Die", "Timestamp", "OK", "Skip", "Fail")
	dim.Printf("%s\n", strings.Repeat("─", 90))

	for _, r := range records {
		fmt.Printf("%s %s %s %s %s\n",
			cyan.Sprintf("%-50s", r.Die),
			dim.Sprintf("%-20s", r.Timestamp.Format("2006-01-02 15:04")),
			green.Sprintf("%4d", r.OK),
			yellow.Sprintf("%4d", r.Skip),
			red.Sprintf("%4d", r.Fail))
	}
	fmt.Println()

	return nil
}

func printGroupedResults(results []runner.Result) {
	var oks, skips []runner.Result
	for _, r := range results {
		switch {
		case r.Status == "OK":
			oks = append(oks, r)
		case strings.HasPrefix(r.Status, "SKIP"):
			skips = append(skips, r)
		}
	}

	yellow := color.New(color.FgHiYellow)
	green := color.New(color.FgHiGreen)
	dim := color.New(color.Faint)

	if len(skips) > 0 {
		yellow.Printf("\n  %s  %d repos skipped\n", runner.IconWarn, len(skips))
	}

	detailed := false
	for _, r := range oks {
		if strings.Contains(strings.TrimRight(r.Output, "\n"), "\n") {
			detailed = true
			break
		}
	}

	for _, r := range oks {
		if detailed {
			fmt.Println()
			green.Printf("  %s  %s\n", runner.IconOK, r.Name)
			output := strings.TrimRight(r.Output, "\n")
			if output != "" {
				for _, line := range strings.Split(output, "\n") {
					dim.Printf("      %s\n", line)
				}
			}
		} else {
			runner.PrintResult(r)
		}
	}
}
