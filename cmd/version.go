package cmd

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/datapointchris/goselfupdate"
	"github.com/spf13/cobra"
)

// Set via -ldflags at build time by goreleaser.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// buildVersion reports the running binary's version. `go install pkg@latest` — how an
// installer deploys this — applies no ldflags but does stamp the module version into
// build info, so without this fallback every installed copy identifies as a dev build
// and `forge update` refuses to run.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return resolveVersion(version, info.Main.Version)
}

func resolveVersion(ldflagsVersion, moduleVersion string) string {
	if ldflagsVersion != "dev" && ldflagsVersion != "" {
		return ldflagsVersion
	}
	// Only a plain vX.Y.Z counts. Go stamps a VCS-derived pseudo-version
	// (v1.6.1-0.20260724161156-2c04703+dirty) onto local `go build` output, and
	// that string is valid semver — which is exactly why goselfupdate separates
	// this check from IsValidVersion rather than leaving every consumer to
	// rewrite the regex.
	if !goselfupdate.IsReleaseVersion(moduleVersion) {
		return ldflagsVersion
	}
	return strings.TrimPrefix(moduleVersion, "v")
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print forge version information",
	Run: func(cmd *cobra.Command, args []string) {
		current := buildVersion()
		fmt.Printf("forge %s\n", current)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		if current == "dev" {
			if info, ok := debug.ReadBuildInfo(); ok {
				fmt.Printf("  go:     %s\n", info.GoVersion)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)

	// `forge version` alone made this the one tool in the fleet where --version
	// was an "unknown flag" error, which is the reflex reach for a version and
	// what a script tries first. The subcommand stays: it prints commit and
	// build date, which the flag's one line does not.
	rootCmd.Version = buildVersion()
	rootCmd.SetVersionTemplate("forge {{.Version}}\n")

	// Free -v for a future --verbose flag, matching the other CLIs here: cobra's
	// auto version flag claims the shorthand, but the convention reserves -v
	// for verbose and -V/--version for version.
	rootCmd.InitDefaultVersionFlag()
	if f := rootCmd.Flags().Lookup("version"); f != nil {
		f.Shorthand = ""
	}
}
