package cmd

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
	"github.com/datapointchris/forge/runner"
	"github.com/datapointchris/forge/toolchain"
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

	manifest, err := loadVersions()
	if err != nil {
		return reconcile.Assets{}, err
	}

	ciFS, err := resolveCIBlocksFS()
	if err != nil {
		return reconcile.Assets{}, err
	}

	return reconcile.Assets{PreCommit: preCommitFS, CI: ciFS, Manifest: manifest}, nil
}

// loadVersions reads the declaration this machine names.
//
// One source, always. A missing declaration is an error rather than a fallback
// to the manifest embedded in this binary: the two carry the same shape and
// print the same kind of numbers, so a silent fallback rolls out whatever the
// binary shipped with and reports success — a bump that moves no repo, and a
// generated config nobody can account for. Naming the file is what makes a
// version bump one edit and a sweep, so a machine that names none is
// half-provisioned rather than defaulted.
func loadVersions() (*toolchain.Toolchain, error) {
	path := config.VersionsPath()
	if path == "" {
		return nil, errNoVersionDeclaration
	}

	manifest, err := toolchain.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("versions_file: %w", err)
	}
	return manifest, nil
}

// errNoVersionDeclaration names the key rather than a path, and points at the
// command that resolves it, per help.md § "Point onward with a command, never
// with a location".
var errNoVersionDeclaration = errors.New(
	"no pinned-version declaration\n\n" +
		"Every generated config takes its hook revs, action versions and language floors\n" +
		"from one file, and forge has been told about none.\n\n" +
		"Set `versions_file` in forge's config to the file that pins what this fleet\n" +
		"installs, or export FORGE_VERSIONS_FILE to name one for a single run.\n" +
		"`forge config show` prints where forge looks and which layer answered")
