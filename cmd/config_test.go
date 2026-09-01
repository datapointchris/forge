package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The source column is the whole point of the command: a stale XDG_CONFIG_HOME
// resolves to a perfectly plausible path and reads identically to the one you
// meant. Reporting the wrong layer is worse than reporting none, because it
// answers the question the reader came with.
func TestResolvedPathsReportTheLayerThatSetThem(t *testing.T) {
	tests := []struct {
		name       string
		dataHome   string
		configHome string
		reposEnv   string
		flag       string
		wantRepos  string
		wantConfig string
	}{
		{"nothing set", "", "", "", "", "default", "default"},
		{"xdg set", "/xdg/data", "/xdg/config", "", "", "$XDG_DATA_HOME", "$XDG_CONFIG_HOME"},
		{"declared beats xdg", "/xdg/data", "/xdg/config", "/declared/repos.json", "", "$FORGE_REPOS_REGISTRY", "$XDG_CONFIG_HOME"},
		{"flag beats xdg", "/xdg/data", "/xdg/config", "", "/elsewhere/repos.json", "--config flag", "$XDG_CONFIG_HOME"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// An empty XDG_CONFIG_HOME resolves against $HOME, and resolveReposPath
			// now reads the config it finds there — so without this the table reads
			// the developer's own file and its answer depends on the machine.
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", tt.dataHome)
			t.Setenv("XDG_CONFIG_HOME", tt.configHome)
			// Cleared per case for the same reason: it is set on a real fleet
			// machine, so leaving it makes every row resolve to $FORGE_REPOS_REGISTRY.
			t.Setenv("FORGE_REPOS_REGISTRY", tt.reposEnv)
			original := cfgPath
			cfgPath = tt.flag
			t.Cleanup(func() { cfgPath = original })

			if _, source := resolveReposPath(); source != tt.wantRepos {
				t.Errorf("repos source = %q, want %q", source, tt.wantRepos)
			}
			if _, source := resolveConfigPath(); source != tt.wantConfig {
				t.Errorf("config source = %q, want %q", source, tt.wantConfig)
			}
		})
	}
}

// -c names the registry and nothing else. Letting it move the config path too
// would make one flag silently retarget two files, and the config is where the
// maintained directories are declared.
func TestTheConfigFlagDoesNotMoveTheConfigPath(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	original := cfgPath
	cfgPath = "/elsewhere/repos.json"
	t.Cleanup(func() { cfgPath = original })

	path, _ := resolveConfigPath()
	if want := filepath.Join(configHome, "forge", "config.yml"); path != want {
		t.Errorf("config path = %q, want %q", path, want)
	}
}

// stdout is data: the JSON has to be parseable with nothing else on the stream,
// because a caller pipes it. cli-design.md § "stdout is data, stderr is
// everything else" — a bare fmt.Println anywhere in the report breaks this.
func TestJSONOutputIsTheOnlyThingOnStdout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	writeDeclaredDirectory(t, home)

	stdout, stderr := runConfigShow(t, true)

	var report configReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not parseable as JSON: %s\n%s", err, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr carried output during a successful run: %s", stderr.String())
	}
	if len(report.Directories) != 1 || report.Directories[0].Name != "sample" {
		t.Errorf("declared directory missing from the report: %+v", report.Directories)
	}
	if report.Directories[0].Exists {
		t.Error("a path that does not exist was reported as present")
	}
}

// The human report goes through the command's writer too — not os.Stdout, which
// a test cannot capture and a caller cannot redirect.
func TestTheHumanReportGoesThroughTheCommandWriter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	writeDeclaredDirectory(t, home)

	stdout, _ := runConfigShow(t, false)

	for _, want := range []string{"sample", "maintained directories (1)", "$XDG_CONFIG_HOME"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("report does not mention %q:\n%s", want, stdout.String())
		}
	}
}

// A config declaring nothing must say so and name the file it read, or the
// reader cannot tell "none declared" from "looking in the wrong place".
func TestAnEmptyConfigNamesTheFileItRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	stdout, _ := runConfigShow(t, false)

	configPath := filepath.Join(home, "forge", "config.yml")
	if !strings.Contains(stdout.String(), configPath) {
		t.Errorf("report does not name the config file it read:\n%s", stdout.String())
	}

	// The line the reader is stuck on, asserted on its own. Matching the whole
	// screen is satisfied by the settings table above it, which names the same
	// path for a different reason — so a wrong file here reads as correct.
	var empty string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.Contains(line, "none declared in") {
			empty = strings.TrimSpace(line)
		}
	}
	if empty == "" {
		t.Fatalf("no maintained-directories line:\n%s", stdout.String())
	}
	if !strings.HasSuffix(empty, configPath) {
		t.Errorf("empty line = %q, want it to name the config file %s", empty, configPath)
	}
}

// Drives runConfig against a throwaway command carrying the writers, rather
// than Execute() — cobra walks a subcommand's Execute up to the root, which
// dispatches the real tree and writes the root help to the process stdout.
func runConfigShow(t *testing.T, asJSON bool) (stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}

	original := configJSON
	configJSON = asJSON
	t.Cleanup(func() { configJSON = original })

	cmd := &cobra.Command{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	if err := runConfig(cmd, nil); err != nil {
		t.Fatalf("config show: %s", err)
	}
	return stdout, stderr
}

func writeDeclaredDirectory(t *testing.T, configHome string) {
	t.Helper()
	dir := filepath.Join(configHome, "forge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "maintained_directories:\n  - name: sample\n    path: /nonexistent/sample\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// forge reads no variable it does not own. $REPOS_JSON sat below the config key
// until it was measured empty under systemd, which is where every scheduled run
// lives — the rung answered only when a person was already at the keyboard.
//
// Asserted against both neighboring layers, because a re-added rung would show
// up differently depending on whether a config existed: it would displace the
// default on a bare machine and lose to the config on a configured one.
func TestTheUnprefixedVariableIsNeverConsulted(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("FORGE_REPOS_REGISTRY", "")
	t.Setenv("REPOS_JSON", "/unprefixed/repos.json")

	if got, _ := resolveReposPath(); got != "/xdg/data/forge/repos.json" {
		t.Errorf("$REPOS_JSON displaced the default: path = %q", got)
	}

	if err := os.MkdirAll(filepath.Join(configHome, "forge"), 0o755); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(configHome, "forge", "config.yml")
	if err := os.WriteFile(written, []byte("repos_registry: /shared/repos.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, source := resolveReposPath()
	if got != "/shared/repos.json" {
		t.Errorf("$REPOS_JSON displaced the config key: path = %q", got)
	}
	if source != "repos_registry in "+written {
		t.Errorf("repos source = %q, want the config that set it", source)
	}
}

// TestReposRegistryFromConfigBeatsTheDataDirectory covers the layer added when the
// symlink went away: a machine sharing one registry between tools names the path
// here, and forge's own data directory becomes the answer for a machine that
// says nothing.
func TestReposRegistryFromConfigBeatsTheDataDirectory(t *testing.T) {
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, "forge"), 0o755); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(configHome, "forge", "config.yml")
	if err := os.WriteFile(written, []byte("repos_registry: /shared/repos.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv("XDG_CONFIG_HOME", configHome)

	path, source := resolveReposPath()
	if path != "/shared/repos.json" {
		t.Errorf("repos path = %q, want /shared/repos.json", path)
	}
	if source != "repos_registry in "+written {
		t.Errorf("repos source = %q, want the config that set it", source)
	}
}

// TestTheFlagStillBeatsAConfiguredReposRegistry keeps -c the last word. It is what a
// caller reaches for to read a registry the machine does not use at all, and a
// config key that could shadow it would make that unreliable.
func TestTheFlagStillBeatsAConfiguredReposRegistry(t *testing.T) {
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, "forge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "forge", "config.yml"), []byte("repos_registry: /shared/repos.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)

	original := cfgPath
	cfgPath = "/elsewhere/repos.json"
	t.Cleanup(func() { cfgPath = original })

	if path, source := resolveReposPath(); path != "/elsewhere/repos.json" || source != "--config flag" {
		t.Errorf("got %q from %q, want the flag to win", path, source)
	}
}

// A hand-edited config carries ~, and the report stats what this returns to say
// whether the registry is present. An unexpanded path made a file that was there
// report as missing — the one question this command exists to answer.
func TestTheReportedReposPathIsExpanded(t *testing.T) {
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, "forge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "forge", "config.yml"), []byte("repos_registry: ~/dev/repos.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)

	original := cfgPath
	cfgPath = ""
	t.Cleanup(func() { cfgPath = original })

	path, _ := resolveReposPath()
	if strings.HasPrefix(path, "~") {
		t.Errorf("reported path %q still carries a literal tilde", path)
	}
}
