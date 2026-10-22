package dies

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

// SyncedDirs keeps a repo's declared local-only directories inside the sync
// base, by symlink, so a file-sync tool carries them between machines.
//
// The directories come from the registry's synced_dirs, because the repo is what
// knows it keeps generated data outside git; forge only has to know the
// operation. A repo declaring none is converged.
//
// It also removes a `.planning` link into the sync base, and an empty `.planning`
// directory. A repo's status is kept in the sync base itself and read from there
// by path, so a link to it from inside the checkout is a second path to the same
// files, and git shows it as untracked because no .gitignore lists it.
type SyncedDirs struct{}

func (SyncedDirs) Name() string { return "synced-dirs" }

func (SyncedDirs) Description() string {
	return "Link each declared synced_dirs entry into the sync base, migrating a real directory when it is safe to and relinking a link to nothing, and remove the retired .planning link. Refuses to migrate over a diverged copy."
}

func (SyncedDirs) Tags() []string {
	return []string{"symlink", "sync", "setup"}
}

// retiredLink is the repo-relative path this die removes when it points into the sync base.
const retiredLink = ".planning"

// syncedDir is one repo-relative directory and where it belongs.
type syncedDir struct {
	rel    string
	target string

	link         string
	linkResolves bool
	isRealDir    bool
	entries      []string
	conflicts    []string
	targetExists bool
}

// linked reports whether the repo already points at the right place.
func (d syncedDir) linked() bool { return d.link == d.target }

// retiredPath is what sits at the retired link's path: a link, a directory, or nothing.
type retiredPath struct {
	link      string
	isRealDir bool
	entries   int
}

type syncedDirsState struct {
	base        string
	dirs        []syncedDir
	retired     retiredPath
	unversioned bool
}

func (s syncedDirsState) Summary() string {
	if s.unversioned {
		return "already synced; nothing to link out of git"
	}
	if len(s.dirs) == 0 {
		return "no synced directories declared"
	}
	return fmt.Sprintf("%s synced", plural(len(s.dirs), "directory", "directories"))
}

func (SyncedDirs) Observe(t reconcile.Target) (reconcile.Observation, error) {
	// This die exists to carry a gitignored directory out to where a file-sync
	// tool can see it. A target that git does not version is already there —
	// and where the sync base is inside the target, the link would point
	// from a synced folder into itself.
	if !t.Versioned() {
		return syncedDirsState{unversioned: true}, nil
	}

	retired, err := observeRetired(t.Path(retiredLink))
	if err != nil {
		return nil, err
	}
	state := syncedDirsState{retired: retired}

	// The sync base is needed only to make a link or to judge one. A registry
	// that declares none still converges a repo that has neither.
	if len(t.Repo.SyncedDirs) == 0 && retired.link == "" {
		return state, nil
	}

	base, err := syncBase(t)
	if err != nil {
		return nil, err
	}
	state.base = base

	// The registry name, never the basename: basenames are neither unique — a
	// reference clone can share a name with a portfolio repo — nor always equal
	// to the registry name, which can differ from the directory it sits in.
	repoBase := filepath.Join(base, t.Repo.Name)

	for _, declared := range t.Repo.SyncedDirs {
		name := declared.As
		if name == "" {
			name = filepath.Base(declared.Dir)
		}
		observed, err := observeSyncedDir(t.Path(declared.Dir), syncedDir{rel: declared.Dir, target: filepath.Join(repoBase, name)})
		if err != nil {
			return nil, err
		}
		state.dirs = append(state.dirs, observed)
	}
	return state, nil
}

func observeRetired(path string) (retiredPath, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return retiredPath{}, nil
	}
	if err != nil {
		return retiredPath{}, err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(path)
		return retiredPath{link: link}, err
	}
	if !info.IsDir() {
		return retiredPath{}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return retiredPath{}, err
	}
	return retiredPath{isRealDir: true, entries: len(entries)}, nil
}

func observeSyncedDir(path string, dir syncedDir) (syncedDir, error) {
	if info, err := os.Stat(dir.target); err == nil && info.IsDir() {
		dir.targetExists = true
	}

	// Lstat, not Stat: a symlink pointing at a directory answers "directory" to
	// Stat, and telling the two apart is the whole of this die's state machine.
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return dir, nil
	}
	if err != nil {
		return dir, err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		// Only a target that is definitely absent makes the link dead. A loop or
		// an unreadable target is someone's link that forge cannot judge.
		_, statErr := os.Stat(path)
		dir.linkResolves = !errors.Is(statErr, fs.ErrNotExist)
		dir.link, err = os.Readlink(path)
		return dir, err
	}
	if !info.IsDir() {
		return dir, nil
	}

	dir.isRealDir = true
	entries, err := os.ReadDir(path)
	if err != nil {
		return dir, err
	}
	for _, entry := range entries {
		dir.entries = append(dir.entries, entry.Name())

		// The synced copy is what other machines have been editing. Moving this
		// repo's copy over it would lose the newer content silently, so a
		// difference refuses the migration and lets a person merge. Identical
		// files are not a conflict.
		peer := filepath.Join(dir.target, entry.Name())
		if _, err := os.Lstat(peer); err != nil {
			continue
		}
		same, err := sameTree(filepath.Join(path, entry.Name()), peer)
		if err != nil {
			return dir, err
		}
		if !same {
			dir.conflicts = append(dir.conflicts, entry.Name())
		}
	}
	return dir, nil
}

func (SyncedDirs) Diff(t reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(syncedDirsState)
	if !ok {
		return nil, fmt.Errorf("synced-dirs: unexpected observation %T", observed)
	}

	var changes []reconcile.Change
	for _, dir := range state.dirs {
		switch {
		case dir.linked() && dir.targetExists:
			// Nothing to say.

		case dir.linked():
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
				Detail: fmt.Sprintf("%s links to %s, which does not exist; expected that directory to exist", dir.rel, homeRelative(dir.target)),
			})

		case dir.link != "" && !dir.linkResolves:
			// A link to nothing is left over from a move, so relinking it overrules no one.
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
				Detail: fmt.Sprintf("%s links to %s, which does not exist; expected a link to %s",
					dir.rel, homeRelative(dir.link), homeRelative(dir.target)),
				Observed: dir.link,
			})

		case dir.link != "":
			// Someone pointed this somewhere on purpose. Repointing it is not a
			// repair, it is overruling a decision forge cannot see the reason for.
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Undeclared, Repair: reconcile.ByHand,
				Detail:   fmt.Sprintf("%s links to %s; expected a link to %s", dir.rel, homeRelative(dir.link), homeRelative(dir.target)),
				Observed: dir.link,
			})

		case dir.isRealDir && len(dir.conflicts) > 0:
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.ByHand,
				Detail: fmt.Sprintf("%s is a directory, expected a link to %s; %s differ between the two",
					dir.rel, homeRelative(dir.target), strings.Join(dir.conflicts, ", ")),
				Observed: fmt.Sprintf("differs: %v", dir.conflicts),
				Advice:   "make " + strings.Join(dir.conflicts, ", ") + " identical in both places, then run apply",
			})

		case dir.isRealDir:
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
				Detail: fmt.Sprintf("%s is a directory of %s; expected a link to %s, with the entries moved there",
					dir.rel, plural(len(dir.entries), "entry", "entries"), homeRelative(dir.target)),
			})

		default:
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Missing, Repair: reconcile.Automatic,
				Detail: fmt.Sprintf("missing link: %s; expected it to point to %s", dir.rel, homeRelative(dir.target)),
			})
		}
	}

	// A link aimed anywhere else, or a file, is the repo's own and not this die's.
	switch retired := state.retired; {
	case retired.link != "" && within(state.base, retired.link):
		changes = append(changes, reconcile.Change{
			Item: retiredLink, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
			Detail:   fmt.Sprintf("%s links to %s; expected no %s", retiredLink, homeRelative(retired.link), retiredLink),
			Observed: retired.link,
		})

	case retired.isRealDir && retired.entries == 0:
		changes = append(changes, reconcile.Change{
			Item: retiredLink, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
			Detail: fmt.Sprintf("%s is an empty directory; expected no %s", retiredLink, retiredLink),
		})

	case retired.isRealDir:
		home := "the repo's directory in the sync base"
		if state.base != "" {
			home = homeRelative(filepath.Join(state.base, t.Repo.Name))
		}
		changes = append(changes, reconcile.Change{
			Item: retiredLink, Verdict: reconcile.Stale, Repair: reconcile.ByHand,
			Detail: fmt.Sprintf("%s holds %s; expected no %s",
				retiredLink, plural(retired.entries, "entry", "entries"), retiredLink),
			Advice: fmt.Sprintf("move them into %s and delete %s", home, retiredLink),
		})
	}
	return changes, nil
}

func (s SyncedDirs) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	observed, err := s.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(syncedDirsState)

	if change.Item == retiredLink {
		return removeRetired(t, change, state)
	}

	var dir syncedDir
	for _, candidate := range state.dirs {
		if candidate.rel == change.Item {
			dir = candidate
		}
	}
	if dir.rel == "" {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the registry declares no such synced directory"}, nil
	}
	if dir.linked() && dir.targetExists {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already linked"}, nil
	}
	// Re-checked live: the divergence may have appeared since the plan printed,
	// and migrating over it is the one mistake this die must never make.
	if len(dir.conflicts) > 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the synced copy has diverged since the plan"}, nil
	}
	if dir.link != "" && !dir.linked() && dir.linkResolves {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the link resolves since the plan"}, nil
	}

	if err := os.MkdirAll(dir.target, 0o755); err != nil {
		return reconcile.Outcome{}, err
	}

	path := t.Path(dir.rel)
	switch {
	case dir.linked():
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "created the missing synced directory"}, nil

	// os.Remove on a link removes the link, never what it points at.
	case dir.link != "":
		if err := os.Remove(path); err != nil {
			return reconcile.Outcome{}, err
		}

	case dir.isRealDir:
		for _, entry := range dir.entries {
			if err := os.Rename(filepath.Join(path, entry), filepath.Join(dir.target, entry)); err != nil {
				return reconcile.Outcome{}, err
			}
		}
		if err := os.Remove(path); err != nil {
			return reconcile.Outcome{}, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return reconcile.Outcome{}, err
	}
	if err := os.Symlink(dir.target, path); err != nil {
		return reconcile.Outcome{}, err
	}

	if len(dir.entries) > 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: fmt.Sprintf("migrated %s and linked", plural(len(dir.entries), "entry", "entries"))}, nil
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "linked"}, nil
}

// removeRetired deletes the retired link or empty directory, re-checked live: a
// link repointed since the plan is somebody's decision, and a directory that
// gained a file holds something only a person can place.
func removeRetired(t reconcile.Target, change reconcile.Change, state syncedDirsState) (reconcile.Outcome, error) {
	retired := state.retired
	removable := (retired.link != "" && within(state.base, retired.link)) || (retired.isRealDir && retired.entries == 0)
	if !removable {
		if retired.link == "" && !retired.isRealDir {
			return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already gone"}, nil
		}
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "changed since the plan"}, nil
	}

	// os.Remove, never RemoveAll: on a link it removes the link and not what it
	// points at, and on a directory it refuses one that is not empty.
	if err := os.Remove(t.Path(retiredLink)); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "removed"}, nil
}

// homeRelative is path as a person types it, with the home directory as ~.
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || !within(home, path) {
		return path
	}
	rel, _ := filepath.Rel(home, path)
	return filepath.Join("~", rel)
}

// within reports whether path sits inside base. An empty base holds nothing.
func within(base, path string) bool {
	if base == "" {
		return false
	}
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func syncBase(t reconcile.Target) (string, error) {
	if t.Config == nil {
		return "", config.ErrNoSyncBase
	}
	return t.Config.ResolvedSyncBase()
}

// sameTree reports whether two paths hold identical content, recursively —
// what `diff -rq` answered for the bash die.
func sameTree(a, b string) (bool, error) {
	infoA, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	infoB, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	if infoA.IsDir() != infoB.IsDir() {
		return false, nil
	}

	if !infoA.IsDir() {
		contentA, err := os.ReadFile(a)
		if err != nil {
			return false, err
		}
		contentB, err := os.ReadFile(b)
		if err != nil {
			return false, err
		}
		return string(contentA) == string(contentB), nil
	}

	entriesA, err := os.ReadDir(a)
	if err != nil {
		return false, err
	}
	entriesB, err := os.ReadDir(b)
	if err != nil {
		return false, err
	}
	if len(entriesA) != len(entriesB) {
		return false, nil
	}
	for i, entry := range entriesA {
		if entry.Name() != entriesB[i].Name() {
			return false, nil
		}
		same, err := sameTree(filepath.Join(a, entry.Name()), filepath.Join(b, entry.Name()))
		if err != nil || !same {
			return same, err
		}
	}
	return true, nil
}
