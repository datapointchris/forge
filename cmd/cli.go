package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/datapointchris/clisurface"
	"github.com/datapointchris/goselfupdate/cobracmd"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/cliaudit"
)

var cliCmd = &cobra.Command{
	Use:   "cli",
	Short: "Read the fleet's command surfaces and compare them",
	Long: `Read what the installed CLIs actually present, and compare it two ways:
against the rest of the fleet, and against what they presented before.

Everything is read from the outside — --help, and cobra's __complete callback
where a tool has one. No bare subcommand is ever run, because the shape most
worth finding is a noun that performs a read when invoked with no verb, and
running it to find out would fire that read.

` + "`audit`" + ` compares tools to each other, and variation there is reported, never
failed. standards/cli-design.md holds two registers: machine contracts
that bind, and design guidance that names a default and its alternatives. audit
covers the second, so a difference is worth a look, not a bug — read the
distribution, since one tool differing from ten is drift while an even split
usually means the fleet found a distinction the standard has not written down
yet.

` + "`snapshot`" + ` and ` + "`diff`" + ` compare one tool to its own past. That is the register
that does bind: a command or flag that was there and is not is a broken
contract for whatever called it, which is why it is worth keeping a surface
around to subtract from.`,
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

var cliSnapshotCmd = &cobra.Command{
	Use:   "snapshot [tool...]",
	Short: "Save the current command surface as the next version",
	Long: `Read the surfaces and keep them, so a later reading can be subtracted.

Saved under forge's own data directory as an incrementing integer — the number
is what gets typed at ` + "`forge cli diff`" + `. Nothing is pruned; a surface is a few
hundred kilobytes and the whole value is having the old one.

Take one before a change that touches a command or a flag, and diff after.`,
	Example: "  forge cli snapshot\n  forge cli snapshot --json",
	RunE:    runCLISnapshot,
}

var cliDiffCmd = &cobra.Command{
	Use:   "diff [from] [to]",
	Short: "Report what changed between two command surfaces",
	Long: `Subtract one surface from another and report the commands and flags that
moved.

Each argument is a saved version number or a path to a surface file. With one
argument, it is compared against the installed tools as they are now; with none,
the newest saved version is. So the common form is a bare ` + "`forge cli diff`" + `.

Tools present on only one side are reported whole rather than as every command
they carry — a tool that was not installed when the snapshot was taken would
otherwise bury the renamed flag this exists to surface.

Exits 0 whatever it finds. A removed command is a fact, not a verdict: whether
it broke anything depends on whether something called it.`,
	Args:    cobra.MaximumNArgs(2),
	Example: "  forge cli diff\n  forge cli diff 3 4\n  forge cli diff surface.json --json",
	RunE:    runCLIDiff,
}

var cliJSON bool

func init() {
	cliSpecCmd.Flags().BoolVar(&cliJSON, "json", false, "Output as JSON to stdout")
	cliAuditCmd.Flags().BoolVar(&cliJSON, "json", false, "Output as JSON to stdout")
	cliSnapshotCmd.Flags().BoolVar(&cliJSON, "json", false, "Output as JSON to stdout")
	cliDiffCmd.Flags().BoolVar(&cliJSON, "json", false, "Output as JSON to stdout")
	// Every verb here discovers its tools from the registry when no tool is
	// named, which is the common form of all four.
	addRegistryFlag(cliCmd)
	cliCmd.AddCommand(cliSpecCmd, cliAuditCmd, cliSnapshotCmd, cliDiffCmd)
	rootCmd.AddCommand(cliCmd)
}

func runCLISnapshot(cmd *cobra.Command, args []string) error {
	tools, unresolved, err := resolveTools(cmd, args)
	if err != nil {
		return err
	}
	dir, err := cliaudit.SnapshotDir()
	if err != nil {
		return err
	}
	snap, err := clisurface.Save(dir, tools, time.Now())
	if err != nil {
		return err
	}

	if cliJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "v%d  %d tools\n", snap.Version, len(snap.Tools)); err != nil {
		return err
	}
	// A snapshot holds surfaces only, so a repo with no binary is reported here
	// rather than written into it where nothing ever read it back.
	for _, u := range unresolved {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "no binary on PATH for %s (tried %v)\n", u.Repo, u.Tried)
	}
	return nil
}

// resolveSurface turns one diff argument into a surface. An empty argument means
// read the installed tools now, which is what makes a one-sided diff the useful
// default.
func resolveSurface(cmd *cobra.Command, arg string) (*clisurface.Snapshot, error) {
	if arg == "" {
		tools, _, err := resolveTools(cmd, nil)
		if err != nil {
			return nil, err
		}
		return &clisurface.Snapshot{Tools: tools}, nil
	}
	if version, err := strconv.Atoi(arg); err == nil {
		dir, err := cliaudit.SnapshotDir()
		if err != nil {
			return nil, err
		}
		return clisurface.Load(dir, version)
	}
	return clisurface.LoadFile(arg)
}

func runCLIDiff(cmd *cobra.Command, args []string) error {
	from, to := "", ""
	switch len(args) {
	case 1:
		from = args[0]
	case 2:
		from, to = args[0], args[1]
	}

	if from == "" {
		dir, err := cliaudit.SnapshotDir()
		if err != nil {
			return err
		}
		versions, err := clisurface.Versions(dir)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			return cobracmd.UsageError(errors.New("no saved surface to compare against: run `forge cli snapshot` first"))
		}
		from = strconv.Itoa(versions[len(versions)-1])
	}

	before, err := resolveSurface(cmd, from)
	if err != nil {
		return err
	}
	after, err := resolveSurface(cmd, to)
	if err != nil {
		return err
	}
	d := clisurface.Compare(before, after)

	if cliJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}
	return clisurface.WriteDiff(cmd.OutOrStdout(), d)
}

// resolveTools turns the positional arguments into extracted trees, falling
// back to registry discovery when none are named.
func resolveTools(cmd *cobra.Command, args []string) ([]*clisurface.Tool, []cliaudit.Unresolved, error) {
	if len(args) > 0 {
		tools := make([]*clisurface.Tool, 0, len(args))
		for _, name := range args {
			// A named tool that cannot be read is the answer to what was asked,
			// so this fails rather than reporting an empty surface for it.
			tool, err := clisurface.Extract(name, clisurface.Options{})
			if err != nil {
				return nil, nil, err
			}
			tools = append(tools, tool)
		}
		return tools, nil, nil
	}

	cfg, err := loadRepos(cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("load repo registry: %w", err)
	}
	found, unresolved := cliaudit.Discover(cfg.Repos)
	tools := make([]*clisurface.Tool, 0, len(found))
	for _, c := range found {
		tool, err := clisurface.Extract(c.Binary, clisurface.Options{})
		if err != nil {
			// Discovery saw the binary on PATH, so failing to read it now is a
			// gap in this run rather than a reason to abandon the sweep.
			unresolved = append(unresolved, cliaudit.Unresolved{Repo: c.Repo, Tried: []string{c.Binary}})
			continue
		}
		tools = append(tools, tool)
	}
	return tools, unresolved, nil
}

func runCLISpec(cmd *cobra.Command, args []string) error {
	tools, unresolved, err := resolveTools(cmd, args)
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
	tools, unresolved, err := resolveTools(cmd, args)
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
