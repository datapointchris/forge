package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is forge's own XDG config directory.
//
// Separate from DefaultReposPath, and holding nothing the registry holds. The
// registry is shared — syncer, fleet and indy all read it, and each takes an
// entry there to be a git repo with a remote. A fact only forge can act on has
// to live somewhere those readers never look, or it changes what iterating the
// registry means for all of them at once.
func DefaultConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "forge", "config.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "forge", "config.yml")
}

// Config is forge's machine config: what this machine's layout is, as opposed
// to what the portfolio is.
//
// YAML rather than JSON because every value in here needs the reason it is
// there next to it, and the registry's format has nowhere to put one.
type Config struct {
	// MaintainedDirectories are held to the same standard as the repos and
	// generated from the same blocks, but are not versioned by git — a
	// Syncthing folder, a home directory, anything a person keeps to a standard
	// without a remote.
	//
	// They reuse Repo because they are the same thing to every generator: a
	// name, a path, and a declared toolchain. The one thing they lack is git,
	// and the dies that need it say so themselves rather than being listed here.
	MaintainedDirectories []Repo `yaml:"maintained_directories,omitempty"`

	// ReposFile points at a registry maintained somewhere other than forge's
	// own data directory, which is what a machine sharing one registry between
	// tools declares. Empty means DefaultReposPath, so a machine that keeps its
	// registry where forge puts it needs no config at all.
	//
	// This is a fact about the machine's layout rather than about the portfolio,
	// which is why it belongs here and not in the registry it names. The tool
	// stays generic either way: what would make it fleet-specific is a path
	// compiled into it, not one it is told.
	ReposFile string `yaml:"repos_file,omitempty"`
}

// LoadConfig reads forge's config from path, expanding tildes and sorting by
// name so output ordering does not depend on how the file was edited.
//
// A missing file yields an empty config rather than an error. Everything here
// is optional, and a machine maintaining no directories should not have to keep
// an empty file around to say so.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		return &Config{}, nil
	}

	expanded, err := ExpandTilde(path)
	if err != nil {
		return nil, fmt.Errorf("expanding config path: %w", err)
	}

	data, err := os.ReadFile(expanded)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", expanded, err)
	}

	var cfg Config
	// KnownFields so a misspelled key fails here rather than being silently
	// dropped — a directory that quietly does not exist reads as one that is
	// converged, which is the worst answer this file can give.
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	// A file holding only comments decodes to io.EOF. That is a config saying
	// nothing, which is exactly what the annotated template ships as.
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing config file %s: %w", expanded, err)
	}

	for i := range cfg.MaintainedDirectories {
		cfg.MaintainedDirectories[i].Path, err = ExpandTilde(cfg.MaintainedDirectories[i].Path)
		if err != nil {
			return nil, fmt.Errorf("expanding path for directory %s: %w",
				cfg.MaintainedDirectories[i].Name, err)
		}
	}

	slices.SortFunc(cfg.MaintainedDirectories, func(a, b Repo) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &cfg, nil
}

// Load reads the config from DefaultConfigPath.
func Load() (*Config, error) {
	return LoadConfig(DefaultConfigPath())
}
