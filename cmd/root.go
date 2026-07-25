package cmd

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
)

var cfgPath string

// Embedded asset filesystems, set by main before Execute().
var (
	embeddedDies      fs.FS
	embeddedPreCommit fs.FS
	embeddedCI        fs.FS
)

// SetEmbeddedAssets stores the embedded filesystems for use by subcommands.
func SetEmbeddedAssets(dies, preCommit, ciBlocks fs.FS) {
	embeddedDies = dies
	embeddedPreCommit = preCommit
	embeddedCI = ciBlocks
}

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "Run commands across all your git repos",
	Long:  "forge reads your syncer config and executes commands across all (or a subset of) repos.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "path to repos file (overrides forge config)")
}

// loadRepos resolves the repo registry, honoring the -c override when set and
// otherwise reading the path from the forge config (with the syncer fallback).
func loadRepos() (*config.SyncerConfig, error) {
	if cfgPath != "" {
		return config.LoadSyncerConfig(cfgPath)
	}
	return config.LoadReposFromForgeConfig()
}
