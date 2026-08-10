package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v6/config"
	"github.com/datapointchris/forge/v6/runner"
)

var (
	statusFilterNames []string
	statusShowAll     bool
	statusJSON        bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Cross-project status view from planning directories",
	Long: `Show project descriptions, planning status, and design docs across all active repos.

By default, only repos with planning content (status.md or design docs) are shown.
Use --all to include all active repos with descriptions. status.md is printed
verbatim — the docs are kept short at the source (a current-state snapshot, not a
changelog), so there is nothing to truncate.`,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().StringSliceVarP(&statusFilterNames, "filter", "F", nil, "comma-separated repo names to include")
	statusCmd.Flags().BoolVarP(&statusShowAll, "all", "a", false, "include repos with only a description")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "output as JSON to stdout")
	rootCmd.AddCommand(statusCmd)
}

// syncBase returns the path to ~/dev/repos/ where planning content lives so
// Syncthing can carry it between machines.
func syncBase() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "dev", "repos")
}

// isSynced reports whether a repo's .planning resolves into syncBase, which is
// what puts the docs in Syncthing's path. A real directory, or a symlink aimed
// anywhere else, keeps them on this machine only.
func isSynced(planningDir string) bool {
	dest, err := filepath.EvalSymlinks(planningDir)
	if err != nil {
		return false
	}
	base, err := filepath.EvalSymlinks(syncBase())
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, dest)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type repoStatus struct {
	repo       config.Repo
	statusMD   string   // content of status.md (empty if none)
	designDocs []string // filenames of design docs
	unsynced   bool     // .planning is a real dir, not a symlink into syncBase
}

func collectRepoStatus(repo config.Repo) repoStatus {
	rs := repoStatus{repo: repo}
	// Resolve planning through the repo's own .planning symlink rather than
	// syncBase()/<name>/planning: repo names are not unique (a portfolio repo and
	// a reference clone can share a basename), so name-keying attributes one
	// repo's planning docs to another. The symlink is the authoritative mapping.
	planningDir := filepath.Join(repo.Path, ".planning")

	if _, err := os.Lstat(planningDir); err != nil {
		return rs
	}
	rs.unsynced = !isSynced(planningDir)

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

// statusEntry is the machine-readable shape of a repo's planning status, shared
// by the --json output here and consumed by `forge brief`.
type statusEntry struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Description string   `json:"description,omitempty"`
	StatusMD    string   `json:"status_md,omitempty"`
	DesignDocs  []string `json:"design_docs,omitempty"`
	Unsynced    bool     `json:"unsynced,omitempty"`
}

// collectStatusEntries walks the filtered repos and returns those with content
// worth showing (planning content, or a description when showAll is set).
func collectStatusEntries(repos []config.Repo, showAll bool) []statusEntry {
	var entries []statusEntry
	for _, repo := range repos {
		rs := collectRepoStatus(repo)
		if !showAll && !rs.hasPlanningContent() {
			continue
		}
		if repo.Description == "" && !rs.hasPlanningContent() {
			continue
		}
		entries = append(entries, statusEntry{
			Name:        repo.Name,
			Path:        repo.Path,
			Description: repo.Description,
			StatusMD:    rs.statusMD,
			DesignDocs:  rs.designDocs,
			Unsynced:    rs.unsynced,
		})
	}
	return entries
}

func runStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := loadRepos()
	if err != nil {
		return err
	}

	repos := runner.SelectRepos(cfg.Repos, statusFilterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(statusFilterNames, ", "))
	}

	entries := collectStatusEntries(repos, statusShowAll)

	if statusJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	if len(entries) == 0 {
		fmt.Println("No repos with planning content found. Use --all to include description-only repos.")
		return nil
	}

	bold := color.New(color.Bold)
	dim := color.New(color.Faint)
	cyan := color.New(color.FgHiCyan)

	for _, e := range entries {
		bold.Printf("## %s", e.Name)
		dim.Printf(" (%s)\n", e.Path)

		if e.Description != "" {
			fmt.Printf("  %s\n", e.Description)
		}
		if e.StatusMD != "" {
			fmt.Println(e.StatusMD)
		}
		if len(e.DesignDocs) > 0 {
			fmt.Println()
			cyan.Printf("Design docs (%d):\n", len(e.DesignDocs))
			for _, doc := range e.DesignDocs {
				fmt.Printf("  - %s\n", doc)
			}
		}
		fmt.Println()
	}

	dim.Printf("(%d repos shown)\n", len(entries))
	return nil
}
