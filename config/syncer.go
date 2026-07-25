package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const defaultReposFileFallback = "~/.config/syncer/datapointchris.json"

type Repo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	// Owner marks a third-party reference clone (the upstream GitHub owner).
	// Empty for repos in the portfolio.
	Owner string `json:"owner,omitempty"`
	// Toolchain declares what forge's generators need to know about the repo.
	// Declared rather than detected: five different conventions for "where the
	// Go service lives" exist across the portfolio, and facts like the SQL
	// dialect are not derivable from the layout at all.
	Toolchain *Toolchain `json:"toolchain,omitempty"`
}

// Toolchain is a repo's declared build surface.
type Toolchain struct {
	Components []Component `json:"components,omitempty"`
	// SQLDialect is the sqlfluff dialect for the repo's .sql files.
	// Empty means the repo has no SQL worth linting.
	SQLDialect string `json:"sql_dialect,omitempty"`
}

// Component is one buildable unit: a stack and the directory it lives in.
// A repo can hold several of the same stack — nomad's api/ and cli/ are both
// Go modules, deliberately isolated from each other.
type Component struct {
	Stack string `json:"stack"`
	Dir   string `json:"dir"`
}

// Stacks returns the distinct stacks across a repo's components.
func (t *Toolchain) Stacks() []string {
	if t == nil {
		return nil
	}
	seen := make(map[string]bool)
	var stacks []string
	for _, component := range t.Components {
		if !seen[component.Stack] {
			seen[component.Stack] = true
			stacks = append(stacks, component.Stack)
		}
	}
	return stacks
}

type SyncerConfig struct {
	Owner       string   `json:"owner"`
	Host        string   `json:"host"`
	SearchPaths []string `json:"search_paths"`
	Repos       []Repo   `json:"repos"`
}

func LoadSyncerConfig(path string) (*SyncerConfig, error) {
	expanded, err := ExpandTilde(path)
	if err != nil {
		return nil, fmt.Errorf("expanding config path: %w", err)
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", expanded, err)
	}

	var cfg SyncerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	for i := range cfg.Repos {
		cfg.Repos[i].Path, err = ExpandTilde(cfg.Repos[i].Path)
		if err != nil {
			return nil, fmt.Errorf("expanding path for repo %s: %w", cfg.Repos[i].Name, err)
		}
	}

	slices.SortFunc(cfg.Repos, func(a, b Repo) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &cfg, nil
}

func ExpandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}

	if path == "~" {
		return home, nil
	}

	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}

	// ~otheruser/... is not supported
	return "", fmt.Errorf("expanding ~user paths is not supported: %s", path)
}

// LoadReposFromForgeConfig loads repos using the path from ForgeConfig.ReposFile,
// falling back to the default syncer config path if not configured.
func LoadReposFromForgeConfig() (*SyncerConfig, error) {
	// Env override, matching FORGE_DIES_DIR: a die shells out to forge, so a
	// caller pointing at a different registry has no other way to say so.
	if reposFile := os.Getenv("FORGE_REPOS_FILE"); reposFile != "" {
		return LoadSyncerConfig(reposFile)
	}

	forgeCfg, err := LoadForgeConfig(DefaultForgeConfigPath)
	if err != nil {
		// Config file missing or unreadable — use fallback
		return LoadSyncerConfig(defaultReposFileFallback)
	}

	if forgeCfg.ReposFile != "" {
		return LoadSyncerConfig(forgeCfg.ReposFile)
	}

	return LoadSyncerConfig(defaultReposFileFallback)
}

// FindRepoByPath returns the repo whose path contains dir, preferring the
// longest match so a repo nested inside another resolves to the inner one.
func FindRepoByPath(repos []Repo, dir string) *Repo {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	abs = resolveSymlinks(abs)

	var best *Repo
	for i := range repos {
		expanded, err := ExpandTilde(repos[i].Path)
		if err != nil {
			continue
		}
		expanded = resolveSymlinks(expanded)
		if abs != expanded && !strings.HasPrefix(abs, expanded+string(filepath.Separator)) {
			continue
		}
		if best == nil || len(expanded) > len(best.Path) {
			best = &repos[i]
		}
	}
	return best
}

// resolveSymlinks canonicalizes a path so a repo reached through a link still
// matches its registry entry — on macOS /var is itself a link to /private/var,
// which is enough to make a working directory unrecognizable. Unresolvable
// paths pass through: a registry entry may name a repo not present here.
func resolveSymlinks(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
