package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/runner"
)

var (
	statusFilterNames []string
	statusShowAll     bool
	statusVerbose     bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Cross-project status view from planning directories",
	Long: `Show project descriptions, planning status, and design docs across all active repos.

By default, only repos with planning content (status.md or design docs) are shown.
Use --all to include all active repos with descriptions.`,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().StringSliceVarP(&statusFilterNames, "filter", "F", nil, "comma-separated repo names to include")
	statusCmd.Flags().BoolVarP(&statusShowAll, "all", "a", false, "include repos with only a description")
	statusCmd.Flags().BoolVarP(&statusVerbose, "verbose", "v", false, "show full status.md content")
	rootCmd.AddCommand(statusCmd)
}

// syncBase returns the path to ~/dev/repos/ where planning symlinks live.
func syncBase() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "dev", "repos")
}

type repoStatus struct {
	repo       config.Repo
	statusMD   string   // content of status.md (empty if none)
	designDocs []string // filenames of design docs
}

func collectRepoStatus(repo config.Repo) repoStatus {
	rs := repoStatus{repo: repo}
	planningDir := filepath.Join(syncBase(), repo.Name, "planning")

	info, err := os.Stat(planningDir)
	if err != nil || !info.IsDir() {
		return rs
	}

	entries, err := os.ReadDir(planningDir)
	if err != nil {
		return rs
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "status.md" {
			data, err := os.ReadFile(filepath.Join(planningDir, "status.md"))
			if err == nil {
				rs.statusMD = strings.TrimSpace(string(data))
			}
		} else {
			rs.designDocs = append(rs.designDocs, e.Name())
		}
	}
	sort.Strings(rs.designDocs)
	return rs
}

func (rs repoStatus) hasPlanningContent() bool {
	return rs.statusMD != "" || len(rs.designDocs) > 0
}

// filterStatusContent returns the current-state portion of status.md,
// skipping completed records, implementation phases, and other historical sections.
func filterStatusContent(content string, verbose bool) string {
	if verbose {
		return content
	}

	lines := strings.Split(content, "\n")
	var filtered []string
	skipSection := false

	skipKeywords := []string{
		"completed", "previously", "phase", "implementation",
		"design principle", "non-goal", "learning goal",
		"v1 feature", "explicitly not",
	}

	for _, line := range lines {
		isHeading := strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### Previously")
		if isHeading {
			lower := strings.ToLower(line)
			skipSection = false
			for _, kw := range skipKeywords {
				if strings.Contains(lower, kw) {
					skipSection = true
					break
				}
			}
		}
		if !skipSection {
			filtered = append(filtered, line)
		}
	}

	// If still too long, truncate to first major section
	if len(filtered) > 50 {
		var truncated []string
		for _, line := range filtered {
			if len(truncated) > 30 && strings.HasPrefix(line, "## ") {
				break
			}
			truncated = append(truncated, line)
		}
		truncated = append(truncated,
			fmt.Sprintf("\n  (status.md is %d lines — run with --verbose or read directly)", len(lines)))
		filtered = truncated
	}

	// Trim trailing blank lines
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}

	return strings.Join(filtered, "\n")
}

func runStatus(cmd *cobra.Command, args []string) error {
	var (
		cfg *config.SyncerConfig
		err error
	)
	if cfgPath != "" {
		cfg, err = config.LoadSyncerConfig(cfgPath)
	} else {
		cfg, err = config.LoadReposFromForgeConfig()
	}
	if err != nil {
		return err
	}

	repos := runner.FilterRepos(runner.ActiveRepos(cfg.Repos), statusFilterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(statusFilterNames, ", "))
	}

	bold := color.New(color.Bold)
	dim := color.New(color.Faint)
	cyan := color.New(color.FgHiCyan)

	shown := 0
	for _, repo := range repos {
		rs := collectRepoStatus(repo)

		if !statusShowAll && !rs.hasPlanningContent() {
			continue
		}
		// Skip repos with no description and no planning content
		if repo.Description == "" && !rs.hasPlanningContent() {
			continue
		}

		shown++
		bold.Printf("## %s", repo.Name)
		dim.Printf(" (%s)\n", repo.Path)

		if repo.Description != "" {
			fmt.Printf("  %s\n", repo.Description)
		}

		if rs.statusMD != "" {
			content := filterStatusContent(rs.statusMD, statusVerbose)
			fmt.Println(content)
		}

		if len(rs.designDocs) > 0 {
			fmt.Println()
			cyan.Printf("Design docs (%d):\n", len(rs.designDocs))
			for _, doc := range rs.designDocs {
				fmt.Printf("  - %s\n", doc)
			}
		}

		fmt.Println()
	}

	if shown == 0 {
		fmt.Println("No repos with planning content found. Use --all to include description-only repos.")
		return nil
	}

	dim.Printf("(%d repos shown)\n", shown)
	return nil
}
