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

func TestFindRepoByPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	repos := []Repo{
		{Name: "todoui", Path: "~/tools/todoui"},
		{Name: "forge", Path: "~/tools/forge"},
	}

	// A subdirectory resolves to its repo — dies run from wherever the user is.
	got := FindRepoByPath(repos, filepath.Join(home, "tools", "todoui", "db"))
	if got == nil || got.Name != "todoui" {
		t.Errorf("subdirectory did not resolve to todoui: %+v", got)
	}

	if got := FindRepoByPath(repos, filepath.Join(home, "tools")); got != nil {
		t.Errorf("a parent of several repos must not match one: %+v", got)
	}
}

func TestToolchainStacksDeduplicates(t *testing.T) {
	// nomad holds two Go modules; the stack list must report go once.
	toolchain := &Toolchain{Components: []Component{
		{Stack: "go", Dir: "api"},
		{Stack: "go", Dir: "cli"},
		{Stack: "vue", Dir: "web"},
	}}

	got := toolchain.Stacks()
	if len(got) != 2 || got[0] != "go" || got[1] != "vue" {
		t.Errorf("Stacks() = %v, want [go vue]", got)
	}
}

func TestEveryEntryDeclaringTheRegistrysOwnOwnerStaysInThePortfolio(t *testing.T) {
	// The registry made owner required on every entry on 2026-08-12, meaning the
	// GitHub owner rather than "somebody else's repo". Reading a present owner
	// as a reference clone emptied every implicit sweep: `forge repos list`
	// returned zero lines against a registry of 82.
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	body := `{
		"owner": "datapointchris",
		"repos": [
			{"name": "forge", "path": "~/tools/forge", "owner": "datapointchris"},
			{"name": "fleet", "path": "~/tools/fleet", "owner": "DATAPOINTCHRIS"},
			{"name": "httpx", "path": "~/code/refs/httpx", "owner": "encode"}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSyncerConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.Repos {
		want := r.Name == "httpx"
		if r.Reference != want {
			t.Errorf("%s: Reference = %v, want %v", r.Name, r.Reference, want)
		}
	}
}

func TestARegistryDeclaringNoOwnerMarksNothingAsAReference(t *testing.T) {
	// The exemplar registry is every entry a different owner and no file owner.
	// Comparing against an absent owner would mark all of them, or none.
	dir := t.TempDir()
	path := filepath.Join(dir, "exemplars.json")
	body := `{"repos": [
		{"name": "vuetify", "path": "~/code/refs/vuetify", "owner": "vuetifyjs"},
		{"name": "cobra", "path": "~/code/refs/cobra", "owner": "spf13"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSyncerConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.Repos {
		if r.Reference {
			t.Errorf("%s marked a reference against a registry declaring no owner", r.Name)
		}
	}
}
