package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadForgeConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yml")
		diesDir := filepath.Join(dir, "dies")

		if err := os.MkdirAll(diesDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte("dies_dir: "+diesDir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadForgeConfig(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.DiesDir != diesDir {
			t.Errorf("DiesDir = %q, want %q", cfg.DiesDir, diesDir)
		}
	})

	t.Run("empty dies_dir uses embedded", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yml")

		if err := os.WriteFile(cfgPath, []byte("other_field: value\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadForgeConfig(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.DiesDir != "" {
			t.Errorf("DiesDir = %q, want empty", cfg.DiesDir)
		}
	})
}
