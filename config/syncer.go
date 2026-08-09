package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultReposPath is forge's own XDG data directory. A generic tool must not
// carry a fleet-specific path, so it asks for the registry where its own data
// lives and knows nothing about where the file is actually maintained — the
// fleet points this at a shared registry with a symlink.
func DefaultReposPath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "forge", "repos.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "forge", "repos.json")
}

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
	// Binary is the command this repo installs, when that name cannot be read
	// out of the repo. Declared for the same reason Toolchain is: a Go CLI has
	// no standard place to state its binary name, so ichrisbirch's `icb` lives
	// only in a cobra Use: string and a `go build -o` path, and nothing that
	// reads the registry can find it. Omit it wherever the repo name, a
	// pyproject [project.scripts] key, or a goreleaser binary: already says so.
	Binary string `json:"binary,omitempty"`
}

// Toolchain is a repo's declared build surface.
type Toolchain struct {
	Components []Component `json:"components,omitempty"`
	// SQLDialect is the sqlfluff dialect for the repo's .sql files.
	// Empty means the repo has no SQL worth linting.
	SQLDialect string `json:"sql_dialect,omitempty"`
	// ShellcheckDisable are rules this repo turns off in its deployed
	// .shellcheckrc, each with the reason it does not apply.
	//
	// The shared config carries no disables and should not: they hid six real
	// SC2155s per repo. But "never disable anything" is not a standard either —
	// homelab's deploy scripts interpolate local variables into remote ssh
	// commands 53 times, which is what those scripts are for, and SC2029 flags
	// every one. The choice was between 53 inline suppressions and a lie, so
	// this is the third option: declared once, in the registry, with a reason
	// attached, visible to anyone reading what the repo asks for.
	ShellcheckDisable []ShellcheckException `json:"shellcheck_disable,omitempty"`
	// Exclude is a regex emitted as pre-commit's top-level exclude, for paths
	// whose content is invalid on purpose. logsift keeps a tree of deliberately
	// broken files to generate real hook output for its pattern tests; every
	// file-shaped hook fails on them, and fail_fast only hid that behind the
	// first one. Excluding per hook would mean naming the same path in a dozen
	// generated blocks, and the repo is the only thing that knows the tree is
	// fixtures rather than source.
	Exclude string `json:"exclude,omitempty"`
}

// ShellcheckException is one disabled rule and why it does not apply here.
// Reason is not optional: an undocumented disable is indistinguishable from
// one nobody has re-examined since, which is how the previous configs drifted.
type ShellcheckException struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
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

// LoadRepos reads the registry from DefaultReposPath. Callers wanting a
// different one pass --config, which is the only override: a config file naming
// the registry's location was a second place for the path to live, and a tool
// that resolves its own data directory does not need one.
func LoadRepos() (*SyncerConfig, error) {
	return LoadSyncerConfig(DefaultReposPath())
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
