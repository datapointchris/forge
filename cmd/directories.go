package cmd

import (
	"github.com/datapointchris/forge/v7/config"
	"github.com/datapointchris/forge/v7/runner"
)

// directories are the targets forge maintains that git does not version,
// declared in forge's own config rather than in the shared registry.
//
// Every die runs against them. The ones needing a remote, a branch or a
// workflow answer "not a git repo" themselves, which is a row worth reading —
// filtering them out here would report a skipped die and a converged one
// identically.
var directories = &reconcileNoun{
	name:  "directories",
	one:   "directory",
	many:  "directories",
	short: "Reconcile the maintained directories against the standards",
	long: `The same three verbs, over the directories git does not version.

A directory held to the standard but kept by a file-sync tool rather than a
remote — a home directory, a Syncthing folder — gets the same generated config
and the same tool configs a repo gets. Declared in forge's own config, because
the repo registry is read by several tools and each of them takes an entry there
to be a git repo with a remote.

Dies that read a remote, a branch or a workflow report themselves
not-applicable rather than being hidden, so a row says why it found nothing.

` + "`run`" + ` is the verb repos do not have: a repo's hooks are run by git and by CI,
and a directory has neither.

Exit codes: 0 converged, 1 changes pending (plan only), 3 something is wrong.`,
	resolve: func(names []string) ([]config.Repo, *config.SyncerConfig, error) {
		// Both files: the declarations come from forge's config, and the dies
		// still take the registry for the fleet-level facts they read from it.
		cfg, err := config.Load()
		if err != nil {
			return nil, nil, err
		}
		registry, err := loadRepos()
		if err != nil {
			return nil, nil, err
		}
		return runner.SelectRepos(cfg.MaintainedDirectories, names), registry, nil
	},
	only: directoriesOnly,
}

func init() {
	rootCmd.AddCommand(directories.command())
}
