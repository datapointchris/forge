package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadForgeConfig(t *testing.T) {
	t.Run("valid config with repos_file", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		reposFile := filepath.Join(dir, "repos.json")

		content := `repos_file = "` + reposFile + `"` + "\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadForgeConfig(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ReposFile != reposFile {
			t.Errorf("ReposFile = %q, want %q", cfg.ReposFile, reposFile)
		}
	})

	t.Run("empty repos_file", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")

		if err := os.WriteFile(cfgPath, []byte("other_field = \"value\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadForgeConfig(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ReposFile != "" {
			t.Errorf("ReposFile = %q, want empty", cfg.ReposFile)
		}
	})

	t.Run("repos_file tilde expansion", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")

		content := `repos_file = "~/config/repos.json"` + "\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadForgeConfig(cfgPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "config", "repos.json")
		if cfg.ReposFile != want {
			t.Errorf("ReposFile = %q, want %q", cfg.ReposFile, want)
		}
	})
}
