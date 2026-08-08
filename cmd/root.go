package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/datapointchris/goselfupdate/autoupdate"
	"github.com/datapointchris/goselfupdate/cobracmd"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v5/config"
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
	// Execute prints the error itself; cobra's own printer would double every line.
	SilenceErrors: true,
	// A command that fails at runtime — no registry entry, an aborting safety
	// check — is not a misuse of its flags, and burying the reason under a usage
	// dump is how the die's output became unreadable.
	SilenceUsage: true,
}

func Execute() {
	autoConfig := autoupdate.Config{Update: updateConfig()}
	if err := cobracmd.Execute(context.Background(), rootCmd, autoConfig); err != nil {
		// The update command writes its own ✗ line; printing here too would
		// report the same failure twice.
		if !errors.Is(err, cobracmd.ErrReported) {
			fmt.Fprintln(os.Stderr, err)
		}
		// 2 says the command line was wrong rather than the run, which is the
		// only failure a caller should retry with different arguments.
		if errors.Is(err, cobracmd.ErrUsage) {
			os.Exit(2)
		}
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
	return config.LoadRepos()
}
