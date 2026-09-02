package dies

import (
	"fmt"
	"sort"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// Builtin returns every die, in the order a walk presents them.
//
// A slice rather than a map so the order is the file's and not the runtime's:
// a walk over several dies has to read the same way twice. The order is
// presentation only — forge's dies are independent of one another, unlike a
// resource order where each step depends on the one before.
//
// This is the registry. There is no registry.yml for a builtin die, because a
// description in a side-file is a second place for the same fact to live and
// the two were free to disagree.
func Builtin() []reconcile.Die {
	return []reconcile.Die{
		Gitignore{},
		Planning{},
		ClaudeMD{},
		LargeFiles{},
		ConflictMarkers{},
		BrokenSymlinks{},
		MergeSettings{},
		BranchProtection{},
		DefaultBranch{},
		Pyproject{},
		GoMod{},
		PreCommit{},
		CI{},
	}
}

// Named resolves one die by the word typed to run it.
func Named(name string) (reconcile.Die, error) {
	for _, die := range Builtin() {
		if die.Name() == name {
			return die, nil
		}
	}
	return nil, fmt.Errorf("unknown die %q; known: %s", name, strings.Join(BuiltinNames(), ", "))
}

// BuiltinNames returns every die name, sorted, for shell completion.
func BuiltinNames() []string {
	names := make([]string, 0, len(Builtin()))
	for _, die := range Builtin() {
		names = append(names, die.Name())
	}
	sort.Strings(names)
	return names
}

// SearchBuiltin returns the dies whose name, description or tags contain the
// query, case-insensitively.
func SearchBuiltin(query string) []reconcile.Die {
	query = strings.ToLower(query)

	var matches []reconcile.Die
	for _, die := range Builtin() {
		haystack := strings.ToLower(die.Name() + " " + die.Description() + " " + strings.Join(die.Tags(), " "))
		if strings.Contains(haystack, query) {
			matches = append(matches, die)
		}
	}
	return matches
}
