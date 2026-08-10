package cmd

import (
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v6/config"
	"github.com/datapointchris/forge/v6/reconcile"
	"github.com/datapointchris/forge/v6/runner"
	"github.com/datapointchris/forge/v6/toolchain"
)

// repos is the portfolio: everything in the registry that git versions.
//
// SelectRepos is deliberately not shared with directories. status, brief and
// exec all mean repos, and a maintained directory entering one of those sweeps
// would be a target with no remote handed to something that assumes one.
var repos = &reconcileNoun{
	name:  "repos",
	one:   "repo",
	many:  "repos",
	short: "Reconcile the repos against the standards",
	long: `Three verbs over one measurement, Terraform-shaped.

  check   what is wrong: findings apply cannot fix
  plan    what apply would change
  apply   make it so

plan is apply minus its last step — the same walk, stopping before the write —
so there is no --dry-run for apply to be the opposite of. check is a different
question: a repo missing a standard .gitignore entry is drift, which is what
apply is for, while a hand-written pipeline or an unmarked custom hook needs a
person. One verb answering both means one exit code carrying both.

Naming a die selects it; omitting one selects them all. -F narrows the repos.

Exit codes: 0 converged, 1 changes pending (plan only), 3 something is wrong.`,
	resolve: func(names []string) ([]config.Repo, *config.SyncerConfig, error) {
		cfg, err := loadRepos()
		if err != nil {
			return nil, nil, err
		}
		return runner.SelectRepos(cfg.Repos, names), cfg, nil
	},
	only: func(*reconcileNoun) []*cobra.Command { return []*cobra.Command{execCmd} },
}

func init() {
	rootCmd.AddCommand(repos.command())
}

// resolvePreCommitFS roots an fs.FS at the pre-commit asset directory, the
// parent of both the blocks and the toolchain manifest.
func resolvePreCommitFS() (fs.FS, error) {
	assetsFS, err := fs.Sub(embeddedPreCommit, "pre-commit")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded assets: %w", err)
	}
	return assetsFS, nil
}

// resolveCIBlocksFS roots an fs.FS at the CI blocks directory.
func resolveCIBlocksFS() (fs.FS, error) {
	blocksFS, err := fs.Sub(embeddedCI, "ci/blocks")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded CI blocks: %w", err)
	}
	return blocksFS, nil
}

// loadAssets gathers the embedded trees and the version manifest once, so every
// die in a walk reads the same manifest rather than each loading its own.
func loadAssets() (reconcile.Assets, error) {
	preCommitFS, err := resolvePreCommitFS()
	if err != nil {
		return reconcile.Assets{}, err
	}

	manifest, err := toolchain.Load(preCommitFS)
	if err != nil {
		return reconcile.Assets{}, err
	}

	ciFS, err := resolveCIBlocksFS()
	if err != nil {
		return reconcile.Assets{}, err
	}

	return reconcile.Assets{PreCommit: preCommitFS, CI: ciFS, Manifest: manifest}, nil
}
