package dies

import (
	"strings"
)

// trackedFiles lists what git tracks in this repo, repo-relative.
//
// git rather than a filesystem walk, and the difference is the whole point for
// the dies that use it. A walk finds `node_modules`, `.venv`, build output and
// everything `.gitignore` exists to keep out — so a large-file check would
// report a dependency tree as a finding, every time, in every repo. What is
// tracked is what a clone gets and what a reviewer reads.
//
// -z because a repo can hold a filename with a newline in it, and splitting on
// newlines would turn one such file into two nonexistent ones.
func trackedFiles(dir string) ([]string, error) {
	out, err := runIn(dir, "git", "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return files, nil
}
