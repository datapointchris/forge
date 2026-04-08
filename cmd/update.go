package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

const githubRepo = "datapointchris/forge"

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update forge to the latest release",
	Long: `Download and install the latest forge release from GitHub.

Checks for a newer version and replaces the current binary.
Uses pre-built binaries from GitHub releases — no Go toolchain required.`,
	RunE: runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	current := version
	fmt.Printf("Current version: %s\n", current)

	if current == "dev" || current == "" {
		return fmt.Errorf("cannot update a dev build; install from a release instead")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return fmt.Errorf("creating update source: %w", err)
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		return fmt.Errorf("creating updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(githubRepo))
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if !found {
		return fmt.Errorf("no releases found for %s", githubRepo)
	}

	if latest.LessOrEqual(current) {
		fmt.Printf("Already up to date (latest: %s)\n", latest.Version())
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}

	fmt.Printf("Updating to %s...\n", latest.Version())
	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("updating binary: %w", err)
	}

	fmt.Printf("Updated to %s\n", latest.Version())
	return nil
}
