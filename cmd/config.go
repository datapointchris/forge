package cmd

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
)

var configJSON bool

// config is a namespace rather than a command that prints, per cli-design.md
// § "A resource that could ever grow a second command is a namespace today" —
// whose worked example is this exact name. syncer and dectl already carry
// `config init|example|path|edit`, so the second verb is foreseeable rather
// than hypothetical, and the rename is free only until someone types the bare
// form.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "The machine config",
	Long: `Inspect the machine config — the directories forge maintains that git does
not version, and the paths forge resolves for its own files.`,
	RunE: requireSubcommand,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the resolved config and where each value came from",
	Long: `Print what forge will actually use, not what the files say.

Tildes are expanded, defaults are filled in, and every path reports the layer
that set it — a flag, an environment variable, or the built-in default. The
source is the part that distinguishes "I asked for this" from "something else
asked for me": a stale XDG_CONFIG_HOME resolves to a perfectly plausible path
and reads identically to the one you meant.`,
	Example: `  # What forge will use, and which layer set each value
  forge config show

  # Whether a directory you declared is actually being seen
  forge config show --json | jq '.maintained_directories'`,
	Args: cobra.NoArgs,
	RunE: runConfig,
}

func init() {
	configShowCmd.Flags().BoolVar(&configJSON, "json", false, "machine-readable output")
	// show reads the flag rather than the registry: naming a path is how you
	// ask what forge resolves when told one, which is the question this command
	// exists to answer.
	addRegistryFlag(configCmd)
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}

// resolved is one setting, what it came out as, and what set it.
type resolved struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Source  string `json:"source"`
	// Exists is nil for settings that are not paths.
	Exists *bool `json:"exists,omitempty"`
}

type configReport struct {
	Settings    []resolved          `json:"settings"`
	Directories []directoryResolved `json:"maintained_directories"`
}

type directoryResolved struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Stacks []string `json:"stacks"`
	Exists bool     `json:"exists"`
}

func runConfig(cmd *cobra.Command, _ []string) error {
	reposPath, reposSource := resolveReposPath()
	versionsPath, versionsSource := resolveVersionsPath()
	configPath, configSource := resolveConfigPath()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	report := configReport{
		Settings: []resolved{
			{Setting: "repos file", Value: reposPath, Source: reposSource, Exists: pathExists(reposPath)},
			{Setting: "versions file", Value: versionsPath, Source: versionsSource, Exists: versionsExists(versionsPath)},
			{Setting: "config file", Value: configPath, Source: configSource, Exists: pathExists(configPath)},
		},
		Directories: make([]directoryResolved, 0, len(cfg.MaintainedDirectories)),
	}
	for _, dir := range cfg.MaintainedDirectories {
		report.Directories = append(report.Directories, directoryResolved{
			Name:   dir.Name,
			Path:   dir.Path,
			Stacks: dir.Toolchain.Stacks(),
			Exists: *pathExists(dir.Path),
		})
	}

	if configJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
	}
	printConfigReport(cmd.OutOrStdout(), report)
	return nil
}

// resolveReposPath answers where the registry comes from, in the order
// LoadRepos resolves it. The layer is the whole point of this command: a
// registry read from somewhere unexpected is indistinguishable from an empty
// one until something says which file was opened.
func resolveReposPath() (string, string) {
	if cfgPath != "" {
		return cfgPath, "--config flag"
	}
	if os.Getenv("FORGE_REPOS_REGISTRY") != "" {
		return config.ReposPath(), "$FORGE_REPOS_REGISTRY"
	}
	if cfg, err := config.LoadConfig(config.DefaultConfigPath()); err == nil && cfg.ReposRegistry != "" {
		return config.ReposPath(), "repos_registry in " + config.DefaultConfigPath()
	}
	if os.Getenv("XDG_DATA_HOME") != "" {
		return config.DefaultReposPath(), "$XDG_DATA_HOME"
	}
	return config.DefaultReposPath(), "default"
}

// resolveVersionsPath answers the same question for the version declaration,
// and has an answer resolveReposPath does not: no file at all. That is a
// machine forge refuses to generate for, so this screen has to say so — it is
// where the error about it points, and a reader arrives here already stuck.
func resolveVersionsPath() (string, string) {
	if os.Getenv("FORGE_VERSIONS_FILE") != "" {
		return config.VersionsPath(), "$FORGE_VERSIONS_FILE"
	}
	if cfg, err := config.LoadConfig(config.DefaultConfigPath()); err == nil && cfg.VersionsFile != "" {
		return config.VersionsPath(), "versions_file in " + config.DefaultConfigPath()
	}
	return "", "not declared — set `versions_file` in forge's config"
}

func resolveConfigPath() (string, string) {
	if os.Getenv("XDG_CONFIG_HOME") != "" {
		return config.DefaultConfigPath(), "$XDG_CONFIG_HOME"
	}
	return config.DefaultConfigPath(), "default"
}

// versionsExists is nil when nothing is declared, because there is no path to
// stat. The source column carries that case; marking it absent as well would
// read as a file that went missing rather than one never named.
func versionsExists(path string) *bool {
	if path == "" {
		return nil
	}
	return pathExists(path)
}

func pathExists(path string) *bool {
	_, err := os.Stat(path)
	exists := err == nil
	return &exists
}

func printConfigReport(w io.Writer, report configReport) {
	label := color.New(color.FgHiCyan)
	source := color.New(color.FgHiBlue)
	missing := color.New(color.FgHiYellow)

	row(w, "\n")
	for _, setting := range report.Settings {
		row(w, "  %s  %s  %s",
			label.Sprintf("%-12s", setting.Setting),
			setting.Value,
			source.Sprintf("(%s)", setting.Source))
		if setting.Exists != nil && !*setting.Exists {
			row(w, "  %s", missing.Sprint("not present"))
		}
		row(w, "\n")
	}

	row(w, "\n  %s\n", label.Sprintf("maintained directories (%d)", len(report.Directories)))
	if len(report.Directories) == 0 {
		row(w, "    none declared in %s\n", report.Settings[1].Value)
		return
	}
	for _, dir := range report.Directories {
		stacks := strings.Join(dir.Stacks, ", ")
		if stacks == "" {
			stacks = "generic blocks only"
		}
		row(w, "    %s  %s  %s", label.Sprintf("%-10s", dir.Name), dir.Path, source.Sprint(stacks))
		if !dir.Exists {
			row(w, "  %s", missing.Sprint("not present"))
		}
		row(w, "\n")
	}
}
