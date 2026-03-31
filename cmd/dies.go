package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/internal/config"
	"github.com/datapointchris/forge/internal/dies"
	"github.com/datapointchris/forge/internal/runner"
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

func loadForgeConfig() (*config.ForgeConfig, error) {
	return config.LoadForgeConfig(config.DefaultForgeConfigPath)
}

func loadDiesRegistry() (*dies.Registry, error) {
	forgeCfg, err := loadForgeConfig()
	if err != nil {
		return nil, err
	}
	return dies.LoadRegistry(forgeCfg.DiesDir)
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

	for _, cat := range cats {
		fmt.Printf("\n%s\n", cat)
		fmt.Println(strings.Repeat("─", len(cat)))
		for _, name := range grouped[cat] {
			die := reg.Dies[name]
			base := filepath.Base(name)

			desc := die.Description
			if desc == "" {
				desc = "(no description)"
			}

			if s, ok := summaries[name]; ok {
				fmt.Printf("  %-40s %s\n", base, desc)
				fmt.Printf("  %-40s runs: %d | last: %s\n", "", s.RunCount, s.LastRun.Format("2006-01-02 15:04"))
			} else {
				fmt.Printf("  %-40s %s\n", base, desc)
			}
		}
	}
	fmt.Println()

	return nil
}

func runDiesRun(cmd *cobra.Command, args []string) error {
	forgeCfg, err := loadForgeConfig()
	if err != nil {
		return err
	}

	reg, err := dies.LoadRegistry(forgeCfg.DiesDir)
	if err != nil {
		return err
	}

	diePath := args[0]
	absScript, err := reg.Resolve(forgeCfg.DiesDir, diePath)
	if err != nil {
		return err
	}

	syncerCfg, err := config.LoadSyncerConfig(cfgPath)
	if err != nil {
		return err
	}

	repos := runner.FilterRepos(syncerCfg.Repos, diesFilterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(diesFilterNames, ", "))
	}

	opts := runner.Opts{
		ScriptFile: absScript,
		DryRun:     diesDryRun,
	}

	repoResults := make(map[string]string)
	var results []runner.Result
	for _, repo := range repos {
		r := runner.ExecuteInRepo(repo, opts)
		results = append(results, r)
		repoResults[r.Name] = r.Status
		if !diesDryRun {
			runner.PrintResult(r)
		}
	}

	runner.PrintSummary(results)

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

func runDiesShow(cmd *cobra.Command, args []string) error {
	forgeCfg, err := loadForgeConfig()
	if err != nil {
		return err
	}

	reg, err := dies.LoadRegistry(forgeCfg.DiesDir)
	if err != nil {
		return err
	}

	diePath := args[0]
	die, ok := reg.Dies[diePath]
	if !ok {
		return fmt.Errorf("die not found: %s", diePath)
	}

	fmt.Printf("\n%s\n", diePath)
	fmt.Println(strings.Repeat("─", len(diePath)))

	if die.Description != "" {
		fmt.Printf("  Description: %s\n", die.Description)
	}
	if len(die.Tags) > 0 {
		fmt.Printf("  Tags:        %s\n", strings.Join(die.Tags, ", "))
	}
	if !die.Registered {
		fmt.Printf("  Status:      unregistered (add to registry.yml for metadata)\n")
	}

	// Show last run if stats exist
	statsPath, err := config.ExpandTilde(dies.DefaultStatsPath)
	if err == nil {
		records, err := dies.LoadStats(statsPath)
		if err == nil {
			filtered := dies.StatsForDie(records, diePath)
			if len(filtered) > 0 {
				last := filtered[len(filtered)-1]
				fmt.Printf("  Last run:    %s (%d ok, %d skip, %d fail)\n",
					last.Timestamp.Format("2006-01-02 15:04"),
					last.OK, last.Skip, last.Fail)
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

	fmt.Printf("\nResults for %q:\n\n", args[0])
	for _, name := range matches {
		die := reg.Dies[name]
		if die.Description != "" {
			fmt.Printf("  %-45s %s\n", name, die.Description)
		} else {
			fmt.Printf("  %s\n", name)
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

	fmt.Printf("\n%-50s %-20s %4s %4s %4s\n", "Die", "Timestamp", "OK", "Skip", "Fail")
	fmt.Println(strings.Repeat("─", 90))

	for _, r := range records {
		fmt.Printf("%-50s %-20s %4d %4d %4d\n",
			r.Die,
			r.Timestamp.Format("2006-01-02 15:04"),
			r.OK, r.Skip, r.Fail)
	}
	fmt.Println()

	return nil
}
