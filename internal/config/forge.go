package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const DefaultForgeConfigPath = "~/.config/forge/config.yml"

type ForgeConfig struct {
	DiesDir string `yaml:"dies_dir"`
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
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing forge config: %w", err)
	}

	// dies_dir is optional — when empty, the binary uses embedded dies
	if cfg.DiesDir != "" {
		cfg.DiesDir, err = ExpandTilde(cfg.DiesDir)
		if err != nil {
			return nil, fmt.Errorf("expanding dies_dir: %w", err)
		}
	}

	return &cfg, nil
}
