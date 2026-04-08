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
