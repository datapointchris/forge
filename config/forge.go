package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

const DefaultForgeConfigPath = "~/.config/forge/config.toml"

type ForgeConfig struct {
	ReposFile string `toml:"repos_file"`
}

func LoadForgeConfig(path string) (*ForgeConfig, error) {
	expanded, err := ExpandTilde(path)
	if err != nil {
		return nil, fmt.Errorf("expanding config path: %w", err)
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("reading forge config %s: %w", expanded, err)
	}

	var cfg ForgeConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing forge config: %w", err)
	}

	if cfg.ReposFile != "" {
		cfg.ReposFile, err = ExpandTilde(cfg.ReposFile)
		if err != nil {
			return nil, fmt.Errorf("expanding repos_file: %w", err)
		}
	}

	return &cfg, nil
}
