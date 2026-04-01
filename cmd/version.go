package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time by goreleaser.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print forge version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("forge %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		if version == "dev" {
			if info, ok := debug.ReadBuildInfo(); ok {
				fmt.Printf("  go:     %s\n", info.GoVersion)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
