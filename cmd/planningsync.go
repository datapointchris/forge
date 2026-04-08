package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/runner"
)

var planningSyncDryRun bool

// extraDirs defines project-specific directories to sync beyond .planning/.
// Each entry maps repo name → (subdir in repo, synced dirname under ~/dev/repos/{name}/).
type extraDir struct {
	repoName  string
	subDir    string
	syncedDir string
}

var extraDirs = []extraDir{
	{"ichrisbirch", "stats/data", "stats"},
}

var planningSyncCmd = &cobra.Command{
	Use:   "planning-sync",
	Short: "Sync .planning/ directories to ~/dev/repos/ via symlinks",
	Long: `Create symlinks from each repo's .planning/ directory to ~/dev/repos/{name}/planning/
so Syncthing can sync gitignored planning files across machines.

Safe to re-run (idempotent). Skips repos where symlinks already exist.`,
	RunE: runPlanningSync,
}

func init() {
	planningSyncCmd.Flags().BoolVarP(&planningSyncDryRun, "dry-run", "n", false, "show what would happen without making changes")
	rootCmd.AddCommand(planningSyncCmd)
}

func syncPlanningDir(repo config.Repo, dryRun bool) runner.Result {
	base := syncBase()
	repoTarget := filepath.Join(repo.Path, ".planning")
	syncedPath := filepath.Join(base, repo.Name, "planning")

	// Check if symlink already exists and points to the right place
	linkDest, err := os.Readlink(repoTarget)
	if err == nil {
		// It's a symlink — check if it's correct
		if linkDest == syncedPath {
			// Ensure the target directory exists
			if _, err := os.Stat(syncedPath); os.IsNotExist(err) {
				if !dryRun {
					if err := os.MkdirAll(syncedPath, 0o755); err != nil {
						return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (mkdir: %v)", err)}
					}
				}
				return runner.Result{Name: repo.Name, Status: "OK", Output: "created missing target dir"}
			}
			return runner.Result{Name: repo.Name, Status: "SKIP (already symlinked)"}
		}
		return runner.Result{
			Name:   repo.Name,
			Status: fmt.Sprintf("SKIP (symlink points to %s, expected %s)", linkDest, syncedPath),
		}
	}

	// Check if it's a real directory with content (needs migration)
	info, err := os.Stat(repoTarget)
	if err == nil && info.IsDir() {
		entries, _ := os.ReadDir(repoTarget)
		if len(entries) > 0 {
			if dryRun {
				return runner.Result{Name: repo.Name, Status: "OK", Output: fmt.Sprintf("would migrate %d files", len(entries))}
			}
			// Migrate: create target, move files, replace with symlink
			if err := os.MkdirAll(syncedPath, 0o755); err != nil {
				return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (mkdir: %v)", err)}
			}
			for _, entry := range entries {
				src := filepath.Join(repoTarget, entry.Name())
				dst := filepath.Join(syncedPath, entry.Name())
				if err := os.Rename(src, dst); err != nil {
					return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (move %s: %v)", entry.Name(), err)}
				}
			}
			if err := os.Remove(repoTarget); err != nil {
				return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (rmdir: %v)", err)}
			}
			if err := os.Symlink(syncedPath, repoTarget); err != nil {
				return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (symlink: %v)", err)}
			}
			return runner.Result{Name: repo.Name, Status: "OK", Output: fmt.Sprintf("migrated %d files and created symlink", len(entries))}
		}
		// Empty directory — remove and replace with symlink
		if !dryRun {
			if err := os.Remove(repoTarget); err != nil {
				return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (rmdir: %v)", err)}
			}
		}
	}

	// Create symlink
	if dryRun {
		return runner.Result{Name: repo.Name, Status: "OK", Output: "would create symlink"}
	}

	if err := os.MkdirAll(syncedPath, 0o755); err != nil {
		return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (mkdir: %v)", err)}
	}
	if err := os.Symlink(syncedPath, repoTarget); err != nil {
		return runner.Result{Name: repo.Name, Status: fmt.Sprintf("FAIL (symlink: %v)", err)}
	}
	return runner.Result{Name: repo.Name, Status: "OK", Output: "created symlink"}
}

// syncExtraDir syncs a project-specific directory (same logic as planning dirs).
func syncExtraDir(repo config.Repo, subDir, syncedDir string, dryRun bool) runner.Result {
	base := syncBase()
	repoTarget := filepath.Join(repo.Path, subDir)
	syncedPath := filepath.Join(base, repo.Name, syncedDir)
	label := repo.Name + "/" + subDir

	linkDest, err := os.Readlink(repoTarget)
	if err == nil {
		if linkDest == syncedPath {
			if _, err := os.Stat(syncedPath); os.IsNotExist(err) {
				if !dryRun {
					if err := os.MkdirAll(syncedPath, 0o755); err != nil {
						return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (mkdir: %v)", err)}
					}
				}
				return runner.Result{Name: label, Status: "OK", Output: "created missing target dir"}
			}
			return runner.Result{Name: label, Status: "SKIP (already symlinked)"}
		}
		return runner.Result{
			Name:   label,
			Status: fmt.Sprintf("SKIP (symlink points to %s, expected %s)", linkDest, syncedPath),
		}
	}

	info, statErr := os.Stat(repoTarget)
	if statErr == nil && info.IsDir() {
		entries, _ := os.ReadDir(repoTarget)
		if len(entries) > 0 {
			if dryRun {
				return runner.Result{Name: label, Status: "OK", Output: fmt.Sprintf("would migrate %d files", len(entries))}
			}
			if err := os.MkdirAll(syncedPath, 0o755); err != nil {
				return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (mkdir: %v)", err)}
			}
			for _, entry := range entries {
				src := filepath.Join(repoTarget, entry.Name())
				dst := filepath.Join(syncedPath, entry.Name())
				if err := os.Rename(src, dst); err != nil {
					return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (move %s: %v)", entry.Name(), err)}
				}
			}
			if err := os.Remove(repoTarget); err != nil {
				return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (rmdir: %v)", err)}
			}
			if err := os.Symlink(syncedPath, repoTarget); err != nil {
				return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (symlink: %v)", err)}
			}
			return runner.Result{Name: label, Status: "OK", Output: fmt.Sprintf("migrated %d files", len(entries))}
		}
		if !dryRun {
			_ = os.Remove(repoTarget)
		}
	}

	if dryRun {
		return runner.Result{Name: label, Status: "OK", Output: "would create symlink"}
	}
	if err := os.MkdirAll(syncedPath, 0o755); err != nil {
		return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (mkdir: %v)", err)}
	}
	if err := os.Symlink(syncedPath, repoTarget); err != nil {
		return runner.Result{Name: label, Status: fmt.Sprintf("FAIL (symlink: %v)", err)}
	}
	return runner.Result{Name: label, Status: "OK", Output: "created symlink"}
}

func runPlanningSync(cmd *cobra.Command, args []string) error {
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

	repos := runner.ActiveRepos(cfg.Repos)
	repoMap := make(map[string]config.Repo, len(repos))
	for _, r := range repos {
		repoMap[r.Name] = r
	}

	if planningSyncDryRun {
		runner.PrintDryRunHeader()
	}

	dim := color.New(color.Faint)

	var results []runner.Result

	// Standard .planning/ dirs
	for _, repo := range repos {
		if _, err := os.Stat(repo.Path); os.IsNotExist(err) {
			continue
		}
		r := syncPlanningDir(repo, planningSyncDryRun)
		results = append(results, r)
		runner.PrintResult(r)
	}

	// Extra project-specific dirs
	for _, extra := range extraDirs {
		repo, ok := repoMap[extra.repoName]
		if !ok {
			continue
		}
		r := syncExtraDir(repo, extra.subDir, extra.syncedDir, planningSyncDryRun)
		results = append(results, r)
		runner.PrintResult(r)
	}

	runner.PrintSummary(results)

	if planningSyncDryRun {
		runner.PrintDryRunFooter()
	}

	dim.Printf("\nSymlinks: repo/.planning/ → ~/dev/repos/{name}/planning/\n")
	return nil
}
