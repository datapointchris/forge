package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/dies"
	"github.com/datapointchris/forge/reconcile"
)

var cfgPath string

// Embedded asset filesystems, set by main before Execute().
var (
	embeddedPreCommit fs.FS
	embeddedCI        fs.FS
)

// SetEmbeddedAssets stores the embedded filesystems for use by subcommands.
func SetEmbeddedAssets(preCommit, ciBlocks fs.FS) {
	embeddedPreCommit = preCommit
	embeddedCI = ciBlocks
}

// rootCmd's help is the first screen anyone reads, so it says where to start
// and in what order, not only what exists.
//
// A die is an argument to a reconcile verb, never a command of its own, and
// nothing in a bare list of subcommands says so: `forge precommit`, `forge ci`
// and `forge dies release` all read as plausible. The help names every die
// from dies.BuiltinNames, so the list cannot lag a die being added, and
// TestEveryCommandTheRootHelpShowsExists holds each command line it prints to
// the real tree.
var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "Reconcile each repo in the registry against the standards",
	Long: "forge reads the repo registry and operates on each repo: reconciling it\n" +
		"against the standards, running a die or an arbitrary command in it, and\n" +
		"testing what it declares it is built from.\n" +
		"\n" +
		"Start with `forge repos plan` for what apply would change, then\n" +
		"`forge repos apply <die>` to make it so. Naming the die is the\n" +
		"confirmation, so that form runs without asking.\n" +
		"\n" +
		"`forge repos check` asks a different question from plan. plan lists\n" +
		"drift a die repairs; check lists what only a person can settle, such as\n" +
		"a hand-written pipeline. A repo can be clean on one and not the other.\n" +
		"\n" +
		"A die is the word given to those verbs, never a command of its own: the\n" +
		"pre-commit config is `forge repos plan precommit`, and the workflows are\n" +
		"`forge repos plan ci`. `forge dies show <die>` says what one does. The\n" +
		"dies are:\n" +
		"\n" +
		wrapList(dies.BuiltinNames(), "  ", 78) + "\n" +
		"\n" +
		"A repo's stacks are declared in the registry rather than reached by a\n" +
		"command: `forge test` runs their suites.\n" +
		"\n" +
		"Reach for `forge toolchain show` when a pinned version is the line that\n" +
		"caught your eye. It names the file every generated pin comes from.\n" +
		"\n" +
		"Questions about the portfolio as a whole belong to fleet: `fleet status`\n" +
		"for what each repo's planning says, `fleet info` for everything in\n" +
		"flight on one page, `fleet stats` for the shape of the set.\n" +
		"\n" +
		"`forge config show` prints the registry it resolved and which layer\n" +
		"named it.",
	Example: "  forge repos plan\n" +
		"  forge repos apply precommit -F forge\n" +
		"  forge repos check\n" +
		"  forge dies list\n" +
		"  forge toolchain show",
	// Execute prints the error itself; cobra's own printer would double every line.
	SilenceErrors: true,
	// A command that fails at runtime — no registry entry, an aborting safety
	// check — is not a misuse of its flags, and burying the reason under a usage
	// dump is how the die's output became unreadable.
	SilenceUsage: true,
}

// run drives the command tree through the shared bootstrap and returns its
// error. Separate from Execute, which exits the process, so a test can run a
// whole command line with the version check suppressed and read the error.
func run(config autoupdate.Config) error {
	return goclikit.Execute(context.Background(), rootCmd, config)
}

func Execute() {
	if err := run(autoupdate.Config{Update: updateConfig()}); err != nil {
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
		if !errors.Is(err, goclikit.ErrReported) {
			fmt.Fprintln(os.Stderr, err)
		}
		// 2 says the command line was wrong rather than the run, which is the
		// only failure a caller should retry with different arguments.
		if errors.Is(err, goclikit.ErrUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// wrapList joins words with commas into indented lines no wider than width.
func wrapList(words []string, indent string, width int) string {
	var lines []string
	line := indent
	for i, word := range words {
		if i < len(words)-1 {
			word += ","
		}
		switch {
		case line == indent:
			line += word
		case len(line)+1+len(word) > width:
			lines = append(lines, line)
			line = indent + word
		default:
			line += " " + word
		}
	}
	return strings.Join(append(lines, line), "\n")
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
