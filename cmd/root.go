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

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

var cfgPath string

// Embedded asset filesystems, set by main before Execute().
var (
	embeddedPreCommit fs.FS
	embeddedCI        fs.FS
)

// requireSubcommand is the RunE for a command that only groups others: bare
// shows help and exits 0, an unknown subcommand is a usage error and exits 2.
//
// Without it cobra treats the unknown word as an argument, prints the group's
// help, and exits 0 — so `forge dies nope` reported success and no caller could
// tell that from a real run. Matches the spelling the data CLIs already use.
func requireSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return cobracmd.UsageError(fmt.Errorf("unknown command %q for %q\nRun '%s --help' for usage",
		args[0], cmd.CommandPath(), cmd.CommandPath()))
}

// SetEmbeddedAssets stores the embedded filesystems for use by subcommands.
func SetEmbeddedAssets(preCommit, ciBlocks fs.FS) {
	embeddedPreCommit = preCommit
	embeddedCI = ciBlocks
}

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "Run commands across all your git repos",
	Long: "forge reads the repo registry and operates on each repo: reconciling it\n" +
		"against the standards, running a die or an arbitrary command in it, and\n" +
		"testing what it declares it is built from.\n" +
		"\n" +
		"The unit of work is one repo. Reaching many of them is machinery, not a\n" +
		"different kind of operation, so the test for a command is what it\n" +
		"operates on rather than whether it writes.\n" +
		"\n" +
		"Questions about the portfolio as a whole belong to fleet: `fleet status`\n" +
		"for what each repo's planning says, `fleet info` for everything in\n" +
		"flight on one page, `fleet stats` for the shape of the set.\n" +
		"\n" +
		"`forge config` prints the registry it resolved and which layer named it.",
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
		// A reconcile verb has already printed its rows, so what is left is the
		// number, not a message. Checked before the printer below: `plan` that
		// found drift exits 1 and prints nothing extra, because pending changes
		// are its answer rather than an error.
		var verdict *reconcile.ExitError
		if errors.As(err, &verdict) {
			os.Exit(int(verdict.Code))
		}
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

// registryFlag is the flag a command declares to accept a registry path.
const registryFlag = "config"

// addRegistryFlag declares -c on a command whose subtree opens the repo
// registry, and only on those.
//
// Cobra prints a root persistent flag under "Global Flags" on every subcommand,
// so declaring it once at the top puts it on `forge version` and `forge dies
// list`, neither of which ever opens a registry. A help screen naming a flag
// its command ignores reads as a fact and is wrong, and nothing distinguishes
// it from the flags that work.
//
// Persistent rather than local, because the namespaces that take it are the
// ones every command under them reads it: repos and directories resolve their
// targets from the registry, cli discovers its tools there, and config show
// reports which registry was resolved. `forge test` has no subtree, so its own
// persistent flag is a local one.
func addRegistryFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&cfgPath, registryFlag, "c", "",
		"path to repos file (overrides forge config)")
}

// loadRepos resolves the repo registry for one command, honoring the -c
// override when set and otherwise reading the path from the forge config (with
// the syncer fallback).
//
// It takes the command so the flag and the read cannot come apart: a command
// that opens the registry without declaring -c would silently ignore a path
// someone typed, and this names it instead. That is the half a list of carriers
// cannot see, since such a command is absent from both sides of the comparison.
func loadRepos(cmd *cobra.Command) (*config.SyncerConfig, error) {
	// LocalFlags covers a command's own declaration, persistent or not;
	// InheritedFlags covers the namespace form. Together they are what the help
	// screen shows, which is the claim being kept honest.
	if cmd.LocalFlags().Lookup(registryFlag) == nil && cmd.InheritedFlags().Lookup(registryFlag) == nil {
		return nil, fmt.Errorf("%s reads the repo registry without declaring -c, "+
			"so a path passed there would be ignored", cmd.CommandPath())
	}
	if cfgPath != "" {
		return config.LoadSyncerConfig(cfgPath)
	}
	return config.LoadRepos()
}
