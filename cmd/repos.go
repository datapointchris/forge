package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

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
	name:   "repos",
	one:    "repo",
	many:   "repos",
	member: "forge",
	short:  "Reconcile the repos against the standards",
	long: `Three verbs over one measurement, Terraform-shaped.

  check   what is wrong: findings apply cannot fix
  plan    what apply would change
  apply   make it so

plan is apply minus its last step — the same walk, stopping before the write —
so there is no --dry-run for apply to be the opposite of. check is a different
question: a repo missing a standard .gitignore entry is drift, which is what
apply is for, while a hand-written pipeline or an unmarked custom hook needs a
person. One verb answering both means one exit code carrying both.

Naming a die selects it; omitting one selects them all.

With no -F, a verb acts on the active repos the registry owns. Dormant repos
and reference clones are left out, and -F reaches either by name. Nothing
reaches a retired repo. ` + "`forge repos list`" + ` names the set and counts what it
left out.

Exit codes: 0 converged, 1 changes pending (plan only), 3 something is wrong.`,
	resolve: func(cmd *cobra.Command, names []string) ([]config.Repo, *config.SyncerConfig, error) {
		cfg, err := loadRepos(cmd)
		if err != nil {
			return nil, nil, err
		}
		return runner.SelectRepos(cfg.Repos, names), cfg, nil
	},
	only:  func(*reconcileNoun) []*cobra.Command { return []*cobra.Command{execCmd} },
	scope: reposScope,
}

// reposScope says which repos the default selection took and what it left out,
// each entry counted once under the first reason that drops it. A registry is
// read by other tools that take a wider set, so a bare list of names reads as
// the whole registry.
func reposScope(registry []config.Repo, selected int) []string {
	var dormant, unstated, reference, retired int
	for _, repo := range registry {
		switch {
		case repo.Status == "retired":
			retired++
		case repo.Status == "dormant":
			dormant++
		case repo.Status != "active":
			unstated++
		case repo.Reference:
			reference++
		}
	}
	lines := []string{plural(selected, "repo", "repos") + ": the active ones the registry owns."}
	var left []string
	for _, part := range []struct {
		count     int
		one, many string
	}{
		{dormant, "dormant", "dormant"},
		{reference, "reference clone", "reference clones"},
		{unstated, "with no status", "with no status"},
		{retired, "retired", "retired"},
	} {
		if part.count > 0 {
			left = append(left, plural(part.count, part.one, part.many))
		}
	}
	if len(left) > 0 {
		lines = append(lines, "Left out: "+strings.Join(left, ", ")+". -F reaches any but a retired one by name.")
	}
	return lines
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
// forge ships no pins of its own, so a machine naming no versions file gets
// errNoVersionDeclaration, never a default. A version bump is then one edit to
// that file and one `repos apply`.
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
// command that resolves it. A reader can run a command; a location leaves them
// to work out what to do with it.
var errNoVersionDeclaration = errors.New(
	"no pinned-version declaration\n\n" +
		"Every generated config takes its hook revs, action versions and language floors\n" +
		"from one file, and forge has been told about none.\n\n" +
		"Set `versions_file` in forge's config to the file that pins what these\n" +
		"machines install, or export FORGE_VERSIONS_FILE to name one for a single run.\n" +
		"`forge config show` prints where forge looks and which layer answered")
