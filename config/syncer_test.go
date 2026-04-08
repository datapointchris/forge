package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSyncerConfig(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	configJSON := `{
		"owner": "testuser",
		"host": "https://github.com",
		"search_paths": ["~/code"],
		"repos": [
			{"name": "repo-one", "path": "~/code/repo-one"},
			{"name": "repo-two", "path": "/absolute/path/repo-two"}
		]
	}`

	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadSyncerConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadSyncerConfig() error: %v", err)
	}

	if cfg.Owner != "testuser" {
		t.Errorf("Owner = %q, want %q", cfg.Owner, "testuser")
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("len(Repos) = %d, want 2", len(cfg.Repos))
	}

	wantPath := filepath.Join(home, "code", "repo-one")
	if cfg.Repos[0].Path != wantPath {
		t.Errorf("Repos[0].Path = %q, want %q", cfg.Repos[0].Path, wantPath)
	}

	if cfg.Repos[1].Path != "/absolute/path/repo-two" {
		t.Errorf("Repos[1].Path = %q, want %q", cfg.Repos[1].Path, "/absolute/path/repo-two")
	}
}

func TestLoadSyncerConfigWithStatus(t *testing.T) {
	configJSON := `{
		"owner": "testuser",
		"host": "https://github.com",
		"search_paths": [],
		"repos": [
			{"name": "active-repo", "path": "/code/active", "status": "active"},
			{"name": "retired-repo", "path": "/code/retired", "status": "retired"},
			{"name": "no-status-repo", "path": "/code/nostatus"}
		]
	}`

	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadSyncerConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadSyncerConfig() error: %v", err)
	}

	if len(cfg.Repos) != 3 {
		t.Fatalf("len(Repos) = %d, want 3", len(cfg.Repos))
	}

	// Repos are sorted by name
	if cfg.Repos[0].Name != "active-repo" || cfg.Repos[0].Status != "active" {
		t.Errorf("Repos[0] = {%q, %q}, want {active-repo, active}", cfg.Repos[0].Name, cfg.Repos[0].Status)
	}
	if cfg.Repos[1].Name != "no-status-repo" || cfg.Repos[1].Status != "" {
		t.Errorf("Repos[1] = {%q, %q}, want {no-status-repo, \"\"}", cfg.Repos[1].Name, cfg.Repos[1].Status)
	}
	if cfg.Repos[2].Name != "retired-repo" || cfg.Repos[2].Status != "retired" {
		t.Errorf("Repos[2] = {%q, %q}, want {retired-repo, retired}", cfg.Repos[2].Name, cfg.Repos[2].Status)
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"~/code/repo", filepath.Join(home, "code", "repo"), false},
		{"~", home, false},
		{"/absolute/path", "/absolute/path", false},
		{"relative/path", "relative/path", false},
		{"~otheruser/path", "", true},
	}

	for _, tt := range tests {
		got, err := ExpandTilde(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ExpandTilde(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
