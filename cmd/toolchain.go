package cmd

import (
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/toolchain"
)

// toolchainCmd reads the pinned-version declaration, and only reads it.
//
// A pin is chosen rather than discovered, so there is no verb here that writes
// one. Resolving "what has upstream released" answers a different question from
// "what do we roll out", and a tool that writes the first into the second turns
// a declaration into a survey of whatever each project happened to tag.
//
// Raising a pin is an edit to the file `versions_file` names, plus a
// `stamp.version` bump, fanned out by `forge repos apply precommit`. Both halves
// matter: the rev is what a repo installs and the stamp is what makes "is this
// repo current" answerable.
var toolchainCmd = &cobra.Command{
	Use:   "toolchain",
	Short: "The pinned tool versions every generated config rolls out",
	Long: `The pinned versions every generated config rolls out, and where they are declared.

  show    what is pinned now, and which file said so

Versions are declared, not discovered. Raise one by editing the file ` + "`show`" + `
names, bumping its ` + "`stamp.version`" + `, then rolling out to one repo before
fanning out:

  forge repos apply precommit -F <repo>`,
	RunE: requireSubcommand,
}

var toolchainShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the manifest version and what it pins",
	Args:  cobra.NoArgs,
	RunE:  runToolchainShow,
}

func init() {
	toolchainCmd.AddCommand(toolchainShowCmd)
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
	dim := color.New(color.Faint)

	row(out, "\n%s %d\n", bold.Sprint("toolchain version"), manifest.Version)
	// Which file answered, per configuration.md § "A resolved value reports
	// which layer set it". Any file of this shape prints the same numbers, so
	// the path is the only thing tying them to something somebody chose.
	row(out, "%s\n\n", dim.Sprint(config.VersionsPath()))
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

// loadManifest reads the declaration through the same resolution the dies get
// in loadAssets, so `show` cannot report pins the generators would not use.
func loadManifest() (*toolchain.Toolchain, error) {
	return loadVersions()
}
