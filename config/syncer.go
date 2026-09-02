package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultReposPath is forge's own XDG data directory: where the registry lives
// on a machine that says nothing about it.
//
// A generic tool must not carry a fleet-specific path, which is why this one
// resolves its own. Where the registry is actually maintained is the machine's
// to state, in the config's repos_registry — see ReposPath.
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

// Repo is one target: a name, where it lives, and what forge needs to know to
// generate for it.
//
// Every field carries both tags. The registry is JSON and forge's own config is
// YAML, and yaml.v3's default of lowercasing the Go name silently disagrees with
// the JSON spelling on any multi-word field — sql_dialect arrives as nil rather
// than failing. Tagging all of them makes the two formats checkable instead of
// coincidental.
type Repo struct {
	Name        string `json:"name"        yaml:"name"`
	Path        string `json:"path"        yaml:"path"`
	Status      string `json:"status"      yaml:"status"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Owner is the GitHub owner, required on every entry because a bare name
	// does not identify a repository — the registry holds entries whose names are
	// also common upstream project names, and pull-requests filtering on the name
	// alone admitted crate-ci/typos under the same label as ~/code/typos.
	//
	// It is not a marker that the repo is somebody else's. Reading it as one is
	// what Reference below exists to replace.
	Owner string `json:"owner,omitempty" yaml:"owner,omitempty"`
	// Visibility is "public" or "private", and decides which runner a generated
	// workflow may name. Read it through IsPrivate rather than comparing here:
	// the two spellings of the same test are not equivalent, because an absent
	// value has to fall on the public side.
	Visibility string `json:"visibility,omitempty" yaml:"visibility,omitempty"`
	// Reference marks a third-party clone, read for its patterns and never
	// worked in, and excluded from every implicit sweep. LoadSyncerConfig
	// derives it by comparing Owner against the registry's own owner; it is not
	// a declared field, so a Repo built as a literal has to set it.
	Reference bool `json:"-" yaml:"-"`
	// Toolchain declares what forge's generators need to know about the repo.
	// Declared rather than detected: five different conventions for "where the
	// Go service lives" exist across the portfolio, and facts like the SQL
	// dialect are not derivable from the layout at all.
	Toolchain *Toolchain `json:"toolchain,omitempty" yaml:"toolchain,omitempty"`
	// SyncedDirs are extra repo-relative directories kept in the sync base
	// alongside .planning, which every repo gets.
	//
	// Declared because the planning die hardcoded `if repo_name = ichrisbirch`
	// to reach that repo's stats/data — a fleet-specific fact inside a generic
	// tool, which is the thing DefaultReposPath's comment above rules out. The
	// repo is what knows it keeps generated data outside git; forge only has to
	// know the operation.
	SyncedDirs []SyncedDir `json:"synced_dirs,omitempty" yaml:"synced_dirs,omitempty"`
	// Binary is the command this repo installs, when that name cannot be read
	// out of the repo. Declared for the same reason Toolchain is: a Go CLI has
	// no standard place to state its binary name, so ichrisbirch's `icb` lives
	// only in a cobra Use: string and a `go build -o` path, and nothing that
	// reads the registry can find it. Omit it wherever the repo name, a
	// pyproject [project.scripts] key, or a goreleaser binary: already says so.
	Binary string `json:"binary,omitempty" yaml:"binary,omitempty"`
}

// IsPrivate reports whether this repo may name a self-hosted runner.
//
// A pull request from a fork on a public repo runs the fork's own code on
// whatever runner it lands on, and a self-hosted runner sits inside a private
// network. So the two ways of writing this test are not equivalent: comparing
// against "private" puts an unreadable value on the public side, and comparing
// against "public" puts it on the side that executes a stranger's code. Only
// the first is safe, and having it in one place is what stops the second from
// being written at a call site.
//
// The registry the fleet ships declares the field on every entry. Another
// machine's registry need not, which is the case this default is for.
func (r Repo) IsPrivate() bool {
	return r.Visibility == "private"
}

// Toolchain is a repo's declared build surface.
type Toolchain struct {
	Components []Component `json:"components,omitempty" yaml:"components,omitempty"`
	// SQLDialect is the sqlfluff dialect for the repo's .sql files.
	// Empty means the repo has no SQL worth linting.
	SQLDialect string `json:"sql_dialect,omitempty" yaml:"sql_dialect,omitempty"`
	// ShellcheckDisable are rules this repo turns off in its deployed
	// .shellcheckrc, each with the reason it does not apply.
	//
	// The shared config carries no disables and should not: they hid six real
	// SC2155s per repo. But "never disable anything" is not a standard either —
	// a provisioning repo's deploy scripts interpolate local variables into
	// remote ssh commands dozens of times, which is what those scripts are for,
	// and SC2029 flags every one. The choice was between an inline suppression
	// at every call site and a lie, so
	// this is the third option: declared once, in the registry, with a reason
	// attached, visible to anyone reading what the repo asks for.
	ShellcheckDisable []ShellcheckException `json:"shellcheck_disable,omitempty" yaml:"shellcheck_disable,omitempty"`
	// Exclude is a regex emitted as pre-commit's top-level exclude, for paths
	// whose content is invalid on purpose. logsift keeps a tree of deliberately
	// broken files to generate real hook output for its pattern tests; every
	// file-shaped hook fails on them, and fail_fast only hid that behind the
	// first one. Excluding per hook would mean naming the same path in a dozen
	// generated blocks, and the repo is the only thing that knows the tree is
	// fixtures rather than source.
	Exclude string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
}

// SyncedDir is one repo-relative directory kept in the sync base, and the name
// it takes there.
//
// As is explicit rather than derived. ichrisbirch syncs `stats/data` into a
// directory called `stats`, so neither the basename nor the first segment is
// the rule — and a tool that guessed would silently point an existing link
// somewhere new, which reads as drift and repairs into a second copy.
type SyncedDir struct {
	Dir string `json:"dir" yaml:"dir"`
	As  string `json:"as"  yaml:"as"`
}

// ShellcheckException is one disabled rule and why it does not apply here.
// Reason is not optional: an undocumented disable is indistinguishable from
// one nobody has re-examined since, which is how the previous configs drifted.
type ShellcheckException struct {
	Rule   string `json:"rule"   yaml:"rule"`
	Reason string `json:"reason" yaml:"reason"`
}

// Component is one buildable unit: a stack and the directory it lives in.
// A repo can hold several of the same stack — an api/ and a cli/ that are both
// Go modules, deliberately isolated from each other.
type Component struct {
	Stack string `json:"stack" yaml:"stack"`
	Dir   string `json:"dir"   yaml:"dir"`
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
	// SyncBase is where per-repo directories are kept so a file-sync tool can
	// see them — the target every .planning symlink points into.
	//
	// Declared rather than compiled in. The path is a fact about one fleet's
	// Syncthing layout, and a generic tool carrying it has no way to be pointed
	// somewhere else. Empty means DefaultSyncBase, which preserves what the
	// bash die did and asks nobody to edit a registry to keep working.
	SyncBase string `json:"sync_base,omitempty"`
}

// DefaultSyncBase is where synced repo directories live when the registry does
// not say. Kept as the historical value so an existing registry needs no edit.
const DefaultSyncBase = "~/dev/repos"

// ResolvedSyncBase returns the sync base as an absolute path.
func (c *SyncerConfig) ResolvedSyncBase() (string, error) {
	base := c.SyncBase
	if base == "" {
		base = DefaultSyncBase
	}
	return ExpandTilde(base)
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
		cfg.Repos[i].Reference = isReference(cfg.Owner, cfg.Repos[i].Owner)
	}

	slices.SortFunc(cfg.Repos, func(a, b Repo) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &cfg, nil
}

// isReference reports whether an entry belongs to someone other than whoever
// the registry is a registry of. A file declaring no owner marks nothing, which
// keeps exemplar-repos.json — every entry a different owner, no file owner —
// from reading as entirely reference or entirely not.
func isReference(fileOwner, repoOwner string) bool {
	if fileOwner == "" || repoOwner == "" {
		return false
	}
	return !strings.EqualFold(fileOwner, repoOwner)
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

// LoadRepos reads the registry from wherever this machine keeps it: the
// config's repos_registry, else DefaultReposPath. --config still beats both.
//
// The key exists because the alternative was a symlink from forge's data
// directory to the real registry, made by hand on every machine and reported by
// nothing. That was the documented answer here, on the grounds that a tool
// resolving its own data directory needs no config key — which conflates
// carrying a path with accepting one. A path compiled in is what would make
// this tool fleet-specific; a path it is told is what keeps it generic, and it
// is the answer syncer and indy both already give.
//
// A config that cannot be read is not an error. It is optional by design, and
// failing here would break every machine that keeps its registry exactly where
// forge expects it — the case that needs no config at all.
func LoadRepos() (*SyncerConfig, error) {
	return LoadSyncerConfig(ReposPath())
}

// ReposPath is the registry this machine reads, ignoring the --config flag,
// which is the caller's to apply. The order is $FORGE_REPOS_REGISTRY, then the
// config's repos_registry, then DefaultReposPath.
//
// A rung below the config read $REPOS_JSON, on the reasoning that a machine
// should declare the path once for every reader. It came out because that
// variable lives in ~/.env and a process sourcing no profile never sees it, so
// the rung was empty in every unattended run. The config file is the machine
// layer already and it reaches every process. See standards/data.md § "A shared
// file is named in config; only the tool's own default is compiled in".
//
// Expanded here rather than left to the loader. LoadSyncerConfig expands too, so
// reading worked either way — but `forge config` stats what this returns to say
// whether the registry is present, and a literal ~ made it report a file that
// was there as missing, which is the one question that command exists to answer.
func ReposPath() string {
	declared := os.Getenv("FORGE_REPOS_REGISTRY")
	if declared == "" {
		cfg, err := LoadConfig(DefaultConfigPath())
		if err == nil {
			declared = cfg.ReposRegistry
		}
	}
	if declared == "" {
		return DefaultReposPath()
	}
	expanded, err := ExpandTilde(declared)
	if err != nil {
		return declared
	}
	return expanded
}

// VersionsPath is where this machine keeps the version declaration:
// $FORGE_VERSIONS_FILE, then versions_file in forge's config, then "" — which
// means the manifest embedded in this binary.
//
// The empty answer is the meaningful one here, and differs from ReposPath. A
// registry forge cannot find is a machine that cannot be operated on; a
// versions file it cannot find is a machine that rolls out what this binary
// shipped with, which is exactly what forge did before the file existed.
func VersionsPath() string {
	declared := os.Getenv("FORGE_VERSIONS_FILE")
	if declared == "" {
		cfg, err := LoadConfig(DefaultConfigPath())
		if err == nil {
			declared = cfg.VersionsFile
		}
	}
	if declared == "" {
		return ""
	}
	expanded, err := ExpandTilde(declared)
	if err != nil {
		return declared
	}
	return expanded
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
