package reconcile

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/toolchain"
)

// Assets is everything embedded in the binary that a die may need.
//
// Handed to the die rather than reached for, so a test can substitute an
// fstest.MapFS and a die never depends on which mode the binary is running in.
type Assets struct {
	// PreCommit is the pre-commit tree: blocks/, configs/, scripts/.
	PreCommit fs.FS
	// CI is the ci/blocks tree.
	CI fs.FS
	// CIScripts is the ci/scripts tree: scripts a CI job runs, deployed whole.
	CIScripts fs.FS
	// Manifest is the single source of truth for every pinned tool version.
	Manifest *toolchain.Toolchain
}

// Target is one repo a die acts on, with what it needs to act.
//
// A die resolves every path through Path and never calls os.Chdir, which
// TestNoDieChangesTheProcessWorkingDirectory is what pins. AssessAll measures
// several targets at once in one process, so a chdir is global state the
// walk would race on. The working directory is a value here rather than an
// ambient fact for that reason.
type Target struct {
	Repo   config.Repo
	Assets Assets
	// Config is the registry this repo came from, for the dies that need a
	// fleet-level fact rather than a repo-level one — where synced directories
	// live, say. Carried whole rather than as a field per die, so one die
	// needing one setting does not widen this struct for the other eight.
	Config *config.SyncerConfig
	// Memo is what this target's dies derive from the repo once and share for
	// the walk. Nil keeps nothing, so every value is derived afresh.
	Memo *Memo
}

// Memo holds facts about one target that several of its dies read: what git
// tracks there, or which GitHub repo its remote names. Each die reading such a
// fact from its source again costs a subprocess or a network call per die, for
// an answer the walk already has.
//
// One Memo belongs to one Target for one walk, so an entry describes that repo
// and nothing else, and the next run derives every entry again. Keys are types
// private to the package that stores them, never a path. A die whose Perform
// changes a fact deletes its entry, so a later reader derives it again.
//
// The methods accept a nil receiver and keep nothing there.
type Memo struct {
	values sync.Map
}

// Load returns the value stored under key, if any.
func (m *Memo) Load(key any) (any, bool) {
	if m == nil {
		return nil, false
	}
	return m.values.Load(key)
}

// Store keeps value under key for the rest of the walk.
func (m *Memo) Store(key, value any) {
	if m == nil {
		return
	}
	m.values.Store(key, value)
}

// Delete drops the value under key, so the next Load derives it again.
func (m *Memo) Delete(key any) {
	if m == nil {
		return
	}
	m.values.Delete(key)
}

// Path resolves a repo-relative path against this target.
func (t Target) Path(rel ...string) string {
	return filepath.Join(append([]string{t.Repo.Path}, rel...)...)
}

// Versioned reports whether git tracks this target, which decides whether the
// dies reading a remote, a branch or a workflow have anything to measure.
//
// Read from the filesystem rather than declared, for the reason
// ci.ReleaseGatesOnValidate is: the failure worth preventing is someone
// registering a directory and forgetting to say what it is, which a flag cannot
// prevent and a file read cannot get wrong. It also re-derives every run, so a
// directory that later becomes a repo starts getting the git dies by itself.
//
// Stat, not IsDir: .git is a file in a worktree and in a submodule, and both of
// those are repos.
func (t Target) Versioned() bool {
	_, err := os.Stat(t.Path(".git"))
	return err == nil
}

// Observation is whatever a die measured. Opaque to everything but its own Diff.
//
// Except for one sentence: Summary is what a repo's row says when nothing
// drifted, and it belongs to the observation because that is the only thing
// that knows how much was examined. "gitignore current (12 entries)" is a
// different claim from "validate.yml current", and a shared count field would
// be a number meaning something different in every row.
type Observation interface {
	Summary() string
}

// Die is one reusable operation, applied to one repo at a time.
//
// Registered as a Go value rather than a bash script found on a filesystem
// walk. The exit-code channel a script had — 0, 2, anything else, plus a last
// line of stdout — cannot carry the four things a plan has to say: nothing
// differs, these differ and I can fix them, these differ and I cannot, and I
// could not tell.
type Die interface {
	// Name is the word typed to run it: `forge repos plan gitignore`.
	Name() string

	// Description is the one line `forge dies list` shows. On the value rather
	// than in a registry.yml beside it, so a die carries its own metadata and
	// the two cannot disagree.
	Description() string

	// Tags are the words `forge dies search` matches on.
	Tags() []string

	// Observe measures the repo. Reads only. May be slow, may need the network.
	Observe(context.Context, Target) (Observation, error)

	// Diff is pure: standard × observed → decided work, in the order it must
	// happen. Its purity is what the no-writes property test asserts.
	Diff(Target, Observation) ([]Change, error)

	// Perform does one Change, re-checking live that it is still the right
	// thing to do, and returns Refused rather than forcing when it is not.
	// Apply passes it only Actionable changes.
	Perform(context.Context, Target, Change) (Outcome, error)
}

// ReadsOutput is implemented by a die whose Observe reads what other dies
// write. A walk measures every die before any performs, so this die's first
// measurement reads the file as it stood. apply measures it again on a target
// where a named die wrote, and acts on that measurement. A walk's die order
// puts each named die ahead of this one.
type ReadsOutput interface {
	// ReadsOutputOf names the dies whose writes this die's Observe reads.
	ReadsOutputOf() []string
}

// ReportOnly is embedded by a die whose every change is ByHand, so Apply never
// calls its Perform.
type ReportOnly struct{}

func (ReportOnly) Perform(_ context.Context, _ Target, change Change) (Outcome, error) {
	return Outcome{Change: change, Status: Refused, Message: "not an automatic repair"}, nil
}
