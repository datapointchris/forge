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

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadForgeConfig("/nonexistent/config.yml")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("missing dies_dir", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yml")

		if err := os.WriteFile(cfgPath, []byte("other_field: value\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadForgeConfig(cfgPath)
		if err == nil {
			t.Fatal("expected error for missing dies_dir")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yml")

		if err := os.WriteFile(cfgPath, []byte(":\ninvalid: [yaml\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadForgeConfig(cfgPath)
		if err == nil {
			t.Fatal("expected error for invalid YAML")
		}
	})

	t.Run("tilde expansion", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yml")

		if err := os.WriteFile(cfgPath, []byte("dies_dir: ~/some/dies\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadForgeConfig(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		home, _ := os.UserHomeDir()
		want := filepath.Join(home, "some", "dies")
		if cfg.DiesDir != want {
			t.Errorf("DiesDir = %q, want %q", cfg.DiesDir, want)
		}
	})
}
