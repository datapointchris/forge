package dies

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// trackedFile is one path git tracks, repo-relative, with what Lstat found at
// it. info is nil where the index names a path missing from the work tree.
//
// Lstat, not Stat: a symlink is reported as itself. Following one would count a
// huge target's size against the wrong repo, and would hide a broken link as a
// missing file.
type trackedFile struct {
	path string
	info fs.FileInfo
}

// trackedFilesKey is the memo key for a target's trackedFile listing.
type trackedFilesKey struct{}

// trackedFiles lists what git tracks in this repo, each with its Lstat.
//
// git rather than a filesystem walk, and the difference is the whole point for
// the dies that use it. A walk finds `node_modules`, `.venv`, build output and
// everything `.gitignore` exists to keep out — so a large-file check would
// report a dependency tree as a finding, every time, in every repo. What is
// tracked is what a clone gets and what a reviewer reads.
//
// -z because a repo can hold a filename with a newline in it, and splitting on
// newlines would turn one such file into two nonexistent ones. It also hands
// back a non-ASCII name as its bytes rather than git's quoted spelling.
//
// The listing is kept on the target's memo. Several dies read it in one walk,
// and no die stages or unstages anything, so the index it lists holds still.
func trackedFiles(ctx context.Context, t reconcile.Target) ([]trackedFile, error) {
	if kept, ok := t.Memo.Load(trackedFilesKey{}); ok {
		return kept.([]trackedFile), nil
	}
	out, err := runIn(ctx, t.Repo.Path, "git", "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	var files []trackedFile
	for _, name := range strings.Split(out, "\x00") {
		if name == "" {
			continue
		}
		file := trackedFile{path: name}
		if info, err := os.Lstat(filepath.Join(t.Repo.Path, name)); err == nil {
			file.info = info
		}
		files = append(files, file)
	}
	t.Memo.Store(trackedFilesKey{}, files)
	return files, nil
}
