package cmd

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time by goreleaser.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Only a plain vX.Y.Z from build info counts as a real release. Go stamps a VCS-derived
// pseudo-version (v1.6.1-0.20260724161156-2c04703+dirty) onto local `go build` output,
// which must keep reporting itself as a dev build.
var releaseVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// buildVersion reports the running binary's version. `go install pkg@latest` — how the
// dotfiles installer deploys forge — applies no ldflags but does stamp the module version
// into build info, so without this fallback every fleet install identifies as a dev build
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
	if !releaseVersionPattern.MatchString(moduleVersion) {
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
}
