package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v6/reconcile"
	"github.com/datapointchris/forge/v6/toolchain"
)

// toolchainCmd operates on forge's own manifest, not on a repo — which is why
// it sits here rather than being a die.
//
// It replaces the pre-commit-update die, which was wrong at the level of the
// system rather than merely lacking a preview. That die ran `pre-commit
// autoupdate` inside each repo, rewriting revs in a generated file; generation
// takes revs from this manifest, so the next sync reverted whatever it wrote.
// The operation that was actually wanted runs once, here, and fans out through
// `forge repos apply precommit`.
var toolchainCmd = &cobra.Command{
	Use:   "toolchain",
	Short: "The pinned tool versions every generated config rolls out",
	Long: `The single source of truth for every pinned tool version forge rolls out.

  show    what is pinned now
  plan    what upstream has released since
  apply   take the new revs and bump the manifest version

Rolling out is a separate step, and deliberately so: bump here, resync one repo
with ` + "`forge repos apply precommit -F <repo>`" + `, verify, then fan out.`,
	RunE: requireSubcommand,
}

var toolchainShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the manifest version and what it pins",
	Args:  cobra.NoArgs,
	RunE:  runToolchainShow,
}

var toolchainPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Report the hook revs upstream has moved past, writing nothing",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return runToolchainUpdate(cmd, false) },
}

var toolchainApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Write the new revs into the manifest and bump its version",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return runToolchainUpdate(cmd, true) },
}

func init() {
	toolchainCmd.AddCommand(toolchainShowCmd, toolchainPlanCmd, toolchainApplyCmd)
	rootCmd.AddCommand(toolchainCmd)
}

func runToolchainShow(cmd *cobra.Command, _ []string) error {
	manifest, err := loadManifest()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	bold := color.New(color.Bold)
	cyan := color.New(color.FgHiCyan)

	row(out, "\n%s %d\n\n", bold.Sprint("toolchain version"), manifest.Version)
	for _, hook := range manifest.Hooks {
		row(out, "  %s %s\n", cyan.Sprintf("%-52s", shortRepo(hook.Repo)), hook.Rev)
	}
	for _, action := range manifest.Actions {
		row(out, "  %s %s\n", cyan.Sprintf("%-52s", action.Uses), action.Version)
	}
	for _, tool := range manifest.Tools {
		row(out, "  %s %s\n", cyan.Sprintf("%-52s", tool.Module), tool.Version)
	}
	row(out, "\n")
	return nil
}

func shortRepo(url string) string {
	return strings.TrimPrefix(strings.TrimPrefix(url, "https://github.com/"), "https://")
}

// runToolchainUpdate asks pre-commit what it would move each hook to.
//
// `pre-commit autoupdate` has no --dry-run, so plan and apply are the same call
// against a throwaway copy of a synthesized config; only apply writes the
// answer back. The read verb cannot reach the manifest at all, which is the
// same structural guarantee the dies have.
func runToolchainUpdate(cmd *cobra.Command, write bool) error {
	manifest, err := loadManifest()
	if err != nil {
		return err
	}

	updated, err := autoupdateRevs(manifest)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	err2 := cmd.ErrOrStderr()

	var moved []toolchain.Hook
	for _, hook := range manifest.Hooks {
		if rev, ok := updated[hook.Repo]; ok && rev != hook.Rev {
			moved = append(moved, toolchain.Hook{Repo: hook.Repo, Rev: rev})
			row(err2, "  %s %-52s %s → %s\n",
				color.New(color.FgHiYellow).Sprintf("%-11s", "stale"), shortRepo(hook.Repo), hook.Rev, rev)
		}
	}

	if len(moved) == 0 {
		row(out, "%s %s\n",
			color.New(color.FgHiGreen).Sprintf("%-9s", reconcile.Converged),
			fmt.Sprintf("toolchain %d: every hook is at its latest release", manifest.Version))
		return nil
	}

	if !write {
		row(out, "%s toolchain %d: %s would move, bumping to %d\n",
			color.New(color.FgHiYellow).Sprintf("%-9s", reconcile.Drift),
			manifest.Version, pluralHooks(len(moved)), manifest.Version+1)
		row(err2, "\nApply, then roll out to one repo before fanning out:\n"+
			"  forge toolchain apply\n  forge repos apply precommit -F <repo>\n")
		return &reconcile.ExitError{Code: reconcile.ExitDrift}
	}

	if err := writeManifest(manifest.Version+1, moved); err != nil {
		return err
	}
	row(out, "%s toolchain %d: %s updated\n",
		color.New(color.FgHiGreen).Sprintf("%-9s", "applied"), manifest.Version+1, pluralHooks(len(moved)))
	return nil
}

func pluralHooks(n int) string {
	if n == 1 {
		return "1 hook"
	}
	return fmt.Sprintf("%d hooks", n)
}

// autoupdateRevs runs pre-commit against a synthesized config in a temp
// directory and reads the revs back.
//
// The config is built from the manifest rather than copied from a repo,
// because the manifest is the thing being updated and a repo's config is a
// rendering of it. Nothing under the repo is touched at any point.
func autoupdateRevs(manifest *toolchain.Toolchain) (map[string]string, error) {
	if _, err := os.Stat(manifestPath()); err != nil {
		return nil, fmt.Errorf("this command runs in a forge checkout: %w", err)
	}

	dir, err := os.MkdirTemp("", "forge-toolchain-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	var synthesized strings.Builder
	synthesized.WriteString("repos:\n")
	for _, hook := range manifest.Hooks {
		fmt.Fprintf(&synthesized, "  - repo: %s\n    rev: %s\n    hooks: []\n", hook.Repo, hook.Rev)
	}

	path := filepath.Join(dir, ".pre-commit-config.yaml")
	if err := os.WriteFile(path, []byte(synthesized.String()), 0o644); err != nil {
		return nil, err
	}

	// pre-commit refuses to run outside a git repository, so the throwaway needs
	// to be one. It never gets a remote or a commit — this is a scratch area for
	// resolving tags, not a repo anything is done to.
	if _, err := runCommand(dir, "git", "init", "-q"); err != nil {
		return nil, fmt.Errorf("preparing the scratch repo: %w", err)
	}

	if out, err := runCommand(dir, "pre-commit", "autoupdate", "-c", path); err != nil {
		return nil, fmt.Errorf("pre-commit autoupdate: %w: %s", err, strings.TrimSpace(out))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	revs := map[string]string{}
	var current string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- repo: "):
			current = strings.TrimPrefix(trimmed, "- repo: ")
		case strings.HasPrefix(trimmed, "rev: ") && current != "":
			revs[current] = strings.TrimPrefix(trimmed, "rev: ")
		}
	}
	return revs, nil
}

// writeManifest edits the rev lines in place rather than re-serializing.
//
// The manifest is mostly comments explaining why each pin is what it is, and a
// yaml round-trip would delete every one of them.
func writeManifest(version int, moved []toolchain.Hook) error {
	data, err := os.ReadFile(manifestPath())
	if err != nil {
		return err
	}

	wanted := map[string]string{}
	for _, hook := range moved {
		wanted[hook.Repo] = hook.Rev
	}

	lines := strings.Split(string(data), "\n")
	var repo string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "version:") && repo == "":
			lines[i] = fmt.Sprintf("version: %d", version)
		case strings.HasPrefix(trimmed, "- repo: "):
			repo = strings.TrimPrefix(trimmed, "- repo: ")
		case strings.HasPrefix(trimmed, "rev: "):
			if rev, ok := wanted[repo]; ok {
				lines[i] = strings.Replace(line, trimmed, "rev: "+rev, 1)
			}
		}
	}

	return os.WriteFile(manifestPath(), []byte(strings.Join(lines, "\n")), 0o644)
}

func manifestPath() string { return filepath.Join("pre-commit", toolchain.File) }

func loadManifest() (*toolchain.Toolchain, error) {
	assetsFS, err := resolvePreCommitFS()
	if err != nil {
		return nil, err
	}
	return toolchain.Load(assetsFS)
}
