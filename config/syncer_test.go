package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tool that shipped a fallback pointed every installation at one deployment's
// directory layout. The error is the whole replacement, so it has to name the
// key rather than fail at the path underneath it.
func TestAnUndeclaredSyncBaseIsAnErrorNamingTheKey(t *testing.T) {
	_, err := (&SyncerConfig{}).ResolvedSyncBase()

	if err == nil {
		t.Fatal("an undeclared sync_base resolved, so a default is still compiled in")
	}
	if !errors.Is(err, ErrNoSyncBase) {
		t.Errorf("err = %v, want ErrNoSyncBase", err)
	}
	if !strings.Contains(err.Error(), "sync_base") {
		t.Errorf("the error does not name the key to declare:\n%s", err)
	}
	// Dropping the constant was to keep one deployment's layout out of the
	// binary, and an error message is shipped text like any other.
	for _, leaked := range []string{"~/dev", "~/tools", "~/notes"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the error names a private path %q:\n%s", leaked, err)
		}
	}
}

func TestADeclaredSyncBaseResolves(t *testing.T) {
	got, err := (&SyncerConfig{SyncBase: "~/somewhere"}).ResolvedSyncBase()
	if err != nil {
		t.Fatalf("ResolvedSyncBase: %v", err)
	}
	if !strings.HasSuffix(got, "/somewhere") {
		t.Errorf("got %q, want the tilde expanded", got)
	}
}

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
		{Name: "widget", Path: "~/src/widget"},
		{Name: "forge", Path: "~/src/forge"},
	}

	// A subdirectory resolves to its repo — dies run from wherever the user is.
	got := FindRepoByPath(repos, filepath.Join(home, "src", "widget", "db"))
	if got == nil || got.Name != "widget" {
		t.Errorf("subdirectory did not resolve to widget: %+v", got)
	}

	if got := FindRepoByPath(repos, filepath.Join(home, "src")); got != nil {
		t.Errorf("a parent of several repos must not match one: %+v", got)
	}
}

func TestToolchainStacksDeduplicates(t *testing.T) {
	// A repo holding two Go modules must report go once in the stack list.
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
	// owner is required on every entry and means the GitHub owner, never
	// "somebody else's repo". Reading a present owner as a reference clone
	// empties every implicit sweep — `forge repos list` returns zero lines.
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	body := `{
		"owner": "datapointchris",
		"repos": [
			{"name": "forge", "path": "~/src/forge", "owner": "datapointchris"},
			{"name": "shouty", "path": "~/src/shouty", "owner": "DATAPOINTCHRIS"},
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

func TestVisibilityIsReadFromTheRegistryAndDefaultsToPublic(t *testing.T) {
	configJSON := `{
		"owner": "testuser",
		"host": "https://github.com",
		"search_paths": [],
		"repos": [
			{"name": "a-private", "path": "/code/p", "owner": "testuser", "visibility": "private"},
			{"name": "b-public", "path": "/code/q", "owner": "testuser", "visibility": "public"},
			{"name": "c-absent", "path": "/code/r", "owner": "testuser"},
			{"name": "d-garbage", "path": "/code/s", "owner": "testuser", "visibility": "Private"}
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

	// Sorted by name, so the order here is the order above.
	want := []bool{true, false, false, false}
	for i, wantPrivate := range want {
		if got := cfg.Repos[i].IsPrivate(); got != wantPrivate {
			t.Errorf("%s: IsPrivate() = %v, want %v (visibility=%q)",
				cfg.Repos[i].Name, got, wantPrivate, cfg.Repos[i].Visibility)
		}
	}
}

// A wrong-cased or misspelled value must land on the public side, because the
// other side runs a fork's code on a machine inside a private network.
func TestAnUnrecognisedVisibilityIsNotTreatedAsPrivate(t *testing.T) {
	for _, value := range []string{"", "Private", "PRIVATE", "internal", "priv", "public"} {
		if (Repo{Visibility: value}).IsPrivate() {
			t.Errorf("visibility %q was read as private", value)
		}
	}
}
