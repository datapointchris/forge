// Package gitenv strips the inherited variables that aim git at a repository
// other than the one it runs in.
//
// pre-commit sets GIT_DIR and GIT_INDEX_FILE while it runs a hook. forge run
// from inside one, as its own tests are by the go-test hook, would otherwise
// read and write whatever repository started it.
package gitenv

import (
	"slices"
	"strings"
)

// targets are the variables that override where git looks.
var targets = []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_COMMON_DIR="}

// WithoutRepoTarget returns env minus every variable that would aim git
// elsewhere.
func WithoutRepoTarget(env []string) []string {
	var kept []string
	for _, entry := range env {
		if !slices.ContainsFunc(targets, func(prefix string) bool { return strings.HasPrefix(entry, prefix) }) {
			kept = append(kept, entry)
		}
	}
	return kept
}
