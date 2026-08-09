package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v5/cliaudit"
	"github.com/datapointchris/forge/v5/config"
)

var cliCmd = &cobra.Command{
	Use:   "cli",
	Short: "Read the fleet's command surfaces and compare them",
	Long: `Read what the installed CLIs actually present, and report where their
grammar varies from the rest of the fleet.

Everything is read from the outside — --help, and cobra's __complete callback
where a tool has one. No bare subcommand is ever run, because the shape most
worth finding is a noun that performs a read when invoked with no verb, and
running it to find out would fire that read.

Variation is reported, never failed. ~/dev/standards/cli-design.md holds two
registers: machine contracts that bind, and design guidance that names a
default and its alternatives. These commands cover the second, so a difference
here is worth a look, not a bug — read the distribution, since one tool
differing from ten is drift while an even split usually means the fleet found a
distinction the standard has not written down yet.`,
	RunE: requireSubcommand,
}

var cliSpecCmd = &cobra.Command{
	Use:   "spec [tool...]",
	Short: "Print the extracted command surface",
	Long: `Print each tool's command tree as it was read.

With no arguments, the tools are the active repos in the registry whose binary
is on PATH, resolved from what each repo already declares — a pyproject.toml
[project.scripts] key, a goreleaser binary:, or the repo's own name. Repos whose
binary could not be resolved are listed rather than dropped.`,
	Example: "  forge cli spec\n  forge cli spec icb meso --json",
	RunE:    runCLISpec,
}

var cliAuditCmd = &cobra.Command{
	Use:   "audit [tool...]",
	Short: "Report where command grammar varies across the fleet",
	Long: `Compare the extracted surfaces and report the variations.

Three things are reported. Verb spellings, as a distribution per job, so the
shape of the split is visible. Bare nouns that act, which cli-design.md asks be
namespaced or justified. And hyphenated verb-noun commands, which read as a
namespace that was never created.

Exits 0 whatever it finds.`,
	Example: "  forge cli audit\n  forge cli audit --json\n  forge cli audit icb todoui",
	RunE:    runCLIAudit,
}

var cliJSON bool

func init() {
	cliSpecCmd.Flags().BoolVar(&cliJSON, "json", false, "Output as JSON to stdout")
	cliAuditCmd.Flags().BoolVar(&cliJSON, "json", false, "Output as JSON to stdout")
	cliCmd.AddCommand(cliSpecCmd, cliAuditCmd)
	rootCmd.AddCommand(cliCmd)
}

// resolveTools turns the positional arguments into extracted trees, falling
// back to registry discovery when none are named.
func resolveTools(args []string) ([]*cliaudit.Tool, []cliaudit.Unresolved, error) {
	if len(args) > 0 {
		tools := make([]*cliaudit.Tool, 0, len(args))
		for _, name := range args {
			tools = append(tools, cliaudit.Extract(name, ""))
		}
		return tools, nil, nil
	}

	cfg, err := config.LoadRepos()
	if err != nil {
		return nil, nil, fmt.Errorf("load repo registry: %w", err)
	}
	found, unresolved := cliaudit.Discover(cfg.Repos)
	tools := make([]*cliaudit.Tool, 0, len(found))
	for _, c := range found {
		tools = append(tools, cliaudit.Extract(c.Binary, c.Repo))
	}
	return tools, unresolved, nil
}

func runCLISpec(cmd *cobra.Command, args []string) error {
	tools, unresolved, err := resolveTools(args)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	if cliJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"tools": tools, "unresolved": unresolved})
	}

	if err := cliaudit.WriteSpec(out, tools); err != nil {
		return err
	}
	// A repo whose binary was not found is a diagnostic, not data — it belongs
	// on stderr so `forge cli spec | ...` stays parseable.
	for _, u := range unresolved {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "no binary on PATH for %s (tried %v)\n", u.Repo, u.Tried)
	}
	return nil
}

func runCLIAudit(cmd *cobra.Command, args []string) error {
	tools, unresolved, err := resolveTools(args)
	if err != nil {
		return err
	}
	rep := cliaudit.Analyze(tools, unresolved)

	if cliJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	return cliaudit.WriteText(cmd.OutOrStdout(), rep)
}
