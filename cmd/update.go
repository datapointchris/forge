package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

const (
	githubOwner = "datapointchris"
	githubRepo  = "forge"
)

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

	if current == "dev" || current == "" {
		fmt.Fprintln(os.Stderr, "✗ forge upgrade failed: cannot update a dev build")
		return fmt.Errorf("dev build")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ forge upgrade failed: %s\n", err)
		return err
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ forge upgrade failed: %s\n", err)
		return err
	}

	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(githubOwner+"/"+githubRepo))
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ forge upgrade failed: %s\n", err)
		return err
	}
	if !found {
		fmt.Fprintln(os.Stderr, "✗ forge upgrade failed: no releases found")
		return fmt.Errorf("no releases")
	}

	beforeTag := ensureVPrefix(current)
	latestTag := ensureVPrefix(latest.Version())

	if latest.LessOrEqual(current) {
		fmt.Printf("✓ forge already at latest: %s\n", latestTag)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ forge upgrade failed: %s\n", err)
		return err
	}

	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		fmt.Fprintf(os.Stderr, "✗ forge upgrade failed: %s\n", err)
		return err
	}

	fmt.Printf("✓ forge upgraded: %s → %s\n", beforeTag, latestTag)

	if subjects, err := fetchChanges(ctx, githubOwner, githubRepo, beforeTag, latestTag); err == nil && len(subjects) > 0 {
		fmt.Println()
		fmt.Println("Changes:")
		for _, s := range subjects {
			fmt.Printf("  • %s\n", s)
		}
	}

	return nil
}

func ensureVPrefix(v string) string {
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

func fetchChanges(ctx context.Context, owner, repo, fromTag, toTag string) ([]string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/compare/%s...%s", owner, repo, fromTag, toTag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}

	var body struct {
		Commits []struct {
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	subjects := make([]string, 0, len(body.Commits))
	for _, c := range body.Commits {
		subject, _, _ := strings.Cut(c.Commit.Message, "\n")
		if subject != "" {
			subjects = append(subjects, subject)
		}
	}
	return subjects, nil
}
