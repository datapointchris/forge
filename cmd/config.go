package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v6/config"
)

var configJSON bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Print the resolved config and where each value came from",
	Long: `Print what forge will actually use, not what the files say.

Tildes are expanded, defaults are filled in, and every path reports the layer
that set it — a flag, an environment variable, or the built-in default. The
source is the part that distinguishes "I asked for this" from "something else
asked for me": a stale XDG_CONFIG_HOME resolves to a perfectly plausible path
and reads identically to the one you meant.`,
	Example: "  forge config\n  forge config --json",
	RunE:    runConfig,
}

func init() {
	configCmd.Flags().BoolVar(&configJSON, "json", false, "machine-readable output")
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

func runConfig(_ *cobra.Command, _ []string) error {
	reposPath, reposSource := resolveReposPath()
	configPath, configSource := resolveConfigPath()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	report := configReport{
		Settings: []resolved{
			{Setting: "repos file", Value: reposPath, Source: reposSource, Exists: pathExists(reposPath)},
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
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printConfigReport(report)
	return nil
}

// resolveReposPath answers where the registry comes from. The -c flag is the
// only override by design: LoadRepos' comment rules out a config key naming it,
// because a tool that resolves its own data directory does not need one.
func resolveReposPath() (string, string) {
	if cfgPath != "" {
		return cfgPath, "--config flag"
	}
	if os.Getenv("XDG_DATA_HOME") != "" {
		return config.DefaultReposPath(), "$XDG_DATA_HOME"
	}
	return config.DefaultReposPath(), "default"
}

func resolveConfigPath() (string, string) {
	if os.Getenv("XDG_CONFIG_HOME") != "" {
		return config.DefaultConfigPath(), "$XDG_CONFIG_HOME"
	}
	return config.DefaultConfigPath(), "default"
}

func pathExists(path string) *bool {
	_, err := os.Stat(path)
	exists := err == nil
	return &exists
}

func printConfigReport(report configReport) {
	label := color.New(color.FgHiCyan)
	source := color.New(color.FgHiBlue)
	missing := color.New(color.FgHiYellow)

	fmt.Println()
	for _, setting := range report.Settings {
		fmt.Printf("  %s  %s  %s",
			label.Sprintf("%-12s", setting.Setting),
			setting.Value,
			source.Sprintf("(%s)", setting.Source))
		if setting.Exists != nil && !*setting.Exists {
			fmt.Printf("  %s", missing.Sprint("not present"))
		}
		fmt.Println()
	}

	fmt.Printf("\n  %s\n", label.Sprintf("maintained directories (%d)", len(report.Directories)))
	if len(report.Directories) == 0 {
		fmt.Printf("    none declared in %s\n", report.Settings[1].Value)
		return
	}
	for _, dir := range report.Directories {
		stacks := strings.Join(dir.Stacks, ", ")
		if stacks == "" {
			stacks = "generic blocks only"
		}
		fmt.Printf("    %s  %s  %s", label.Sprintf("%-10s", dir.Name), dir.Path, source.Sprint(stacks))
		if !dir.Exists {
			fmt.Printf("  %s", missing.Sprint("not present"))
		}
		fmt.Println()
	}
}
