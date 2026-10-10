package dies

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/forge/toolchain"
)

// StampedFiles is what fleet reads to know where stamps live, so it has to be
// exactly the files the dies stamp. Each die is applied alone to a private Go
// repo, the shape owed every stamped file, and the stamps it leaves are what
// it writes.
func TestStampedFilesAreExactlyTheFilesTheDiesStamp(t *testing.T) {
	var written []StampedFile
	for _, die := range Builtin() {
		target := privateFixture(t, stacks("go", "shell"), map[string]string{"go.mod": "module fixture\n\ngo 1.26\n"})
		initRepo(t, target, "main")
		applyAll(t, target, die)
		for _, rel := range stampedUnder(t, target.Repo.Path) {
			written = append(written, StampedFile{Path: rel, Die: die.Name()})
		}
	}

	declared := StampedFiles()
	sortStamped := func(files []StampedFile) {
		slices.SortFunc(files, func(a, b StampedFile) int { return strings.Compare(a.Path+a.Die, b.Path+b.Die) })
	}
	sortStamped(written)
	sortStamped(declared)
	if !slices.Equal(written, declared) {
		t.Errorf("the dies stamp %v, and StampedFiles names %v", written, declared)
	}
}

// stampedUnder lists every file below root, outside .git, whose stamp line
// opens with the prefix.
func stampedUnder(t *testing.T, root string) []string {
	t.Helper()
	var stamped []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		scanner := bufio.NewScanner(file)
		for line := 1; scanner.Scan(); line++ {
			if line == toolchain.StampLine {
				if strings.HasPrefix(scanner.Text(), toolchain.StampPrefix) {
					rel, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					stamped = append(stamped, rel)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %s", root, err)
	}
	return stamped
}
