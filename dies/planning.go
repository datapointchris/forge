package dies

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

// Planning keeps a repo's local-only directories inside the sync base, by
// symlink, so a file-sync tool carries them between machines.
//
// `.planning` is gitignored in every repo, which is what makes it useful — the
// notes are for one person across several machines rather than for the project
// — and also what makes it invisible to git. The symlink is how it reaches the
// other desk.
//
// This is sync-planning and has-planning-dir in one. The check reported a
// missing .planning as a failure; here it is Automatic drift, because the very
// die reporting it is the one that creates it.
type Planning struct{}

func (Planning) Name() string { return "planning" }

func (Planning) Description() string {
	return "Link .planning (and any declared synced_dirs) into the sync base, migrating a real directory when it is safe to. Refuses to migrate over a diverged copy."
}

func (Planning) Tags() []string {
	return []string{"planning", "symlink", "sync", "setup"}
}

// syncedDir is one repo-relative directory and where it belongs.
type syncedDir struct {
	rel    string
	target string

	link         string
	isRealDir    bool
	entries      []string
	conflicts    []string
	targetExists bool
}

// linked reports whether the repo already points at the right place.
func (d syncedDir) linked() bool { return d.link == d.target }

type planningState struct {
	dirs        []syncedDir
	unversioned bool
}

func (s planningState) Summary() string {
	if s.unversioned {
		return "already synced; nothing to link out of git"
	}
	return fmt.Sprintf("%s synced", plural(len(s.dirs), "directory", "directories"))
}

func (Planning) Observe(t reconcile.Target) (reconcile.Observation, error) {
	// This die exists to carry a gitignored directory out to where a file-sync
	// tool can see it. A target that git does not version is already there —
	// and where the sync base is inside the target, the link would point
	// from a synced folder into itself.
	if !t.Versioned() {
		return planningState{unversioned: true}, nil
	}

	base, err := syncBase(t)
	if err != nil {
		return nil, err
	}

	// The registry name, never the basename: basenames are neither unique — a
	// reference clone can share a name with a portfolio repo — nor always equal
	// to the registry name, which can differ from the directory it sits in.
	repoBase := filepath.Join(base, t.Repo.Name)

	wanted := []syncedDir{{rel: ".planning", target: filepath.Join(repoBase, "planning")}}
	for _, declared := range t.Repo.SyncedDirs {
		name := declared.As
		if name == "" {
			name = filepath.Base(declared.Dir)
		}
		wanted = append(wanted, syncedDir{rel: declared.Dir, target: filepath.Join(repoBase, name)})
	}

	state := planningState{}
	for _, dir := range wanted {
		observed, err := observeSyncedDir(t.Path(dir.rel), dir)
		if err != nil {
			return nil, err
		}
		state.dirs = append(state.dirs, observed)
	}
	return state, nil
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

func (Planning) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(planningState)
	if !ok {
		return nil, fmt.Errorf("planning: unexpected observation %T", observed)
	}

	var changes []reconcile.Change
	for _, dir := range state.dirs {
		switch {
		case dir.linked() && dir.targetExists:
			// Nothing to say.

		case dir.linked():
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
				Detail: "linked, but the synced directory it points at does not exist",
			})

		case dir.link != "":
			// Someone pointed this somewhere on purpose. Repointing it is not a
			// repair, it is overruling a decision forge cannot see the reason for.
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Undeclared, Repair: reconcile.ByHand,
				Detail: "symlinked somewhere other than the sync base", Observed: dir.link,
			})

		case dir.isRealDir && len(dir.conflicts) > 0:
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.ByHand,
				Detail:   "a real directory whose contents differ from the synced copy — merge by hand, or migrating loses the newer side",
				Observed: fmt.Sprintf("differs: %v", dir.conflicts),
			})

		case dir.isRealDir:
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Stale, Repair: reconcile.Automatic,
				Detail: fmt.Sprintf("a real directory — migrate %s to the sync base and link", plural(len(dir.entries), "entry", "entries")),
			})

		default:
			changes = append(changes, reconcile.Change{
				Item: dir.rel, Verdict: reconcile.Missing, Repair: reconcile.Automatic,
				Detail: "link to " + dir.target,
			})
		}
	}
	return changes, nil
}

func (p Planning) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := p.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}

	var dir syncedDir
	for _, candidate := range observed.(planningState).dirs {
		if candidate.rel == change.Item {
			dir = candidate
		}
	}
	if dir.rel == "" {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "no longer a synced directory"}, nil
	}
	if dir.linked() && dir.targetExists {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already linked"}, nil
	}
	// Re-checked live: the divergence may have appeared since the plan printed,
	// and migrating over it is the one mistake this die must never make.
	if len(dir.conflicts) > 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the synced copy has diverged since the plan"}, nil
	}

	if err := os.MkdirAll(dir.target, 0o755); err != nil {
		return reconcile.Outcome{}, err
	}

	path := t.Path(dir.rel)
	switch {
	case dir.linked():
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "created the missing synced directory"}, nil

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
