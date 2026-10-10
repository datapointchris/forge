package gitenv

import (
	"slices"
	"testing"
)

func TestOnlyTheVariablesAimingGitElsewhereAreDropped(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/elsewhere/.git",
		"GIT_WORK_TREE=/elsewhere",
		"GIT_INDEX_FILE=/elsewhere/.git/index",
		"GIT_COMMON_DIR=/elsewhere/.git",
		"GIT_AUTHOR_NAME=someone",
	}
	got := WithoutRepoTarget(env)
	if want := []string{"PATH=/usr/bin", "GIT_AUTHOR_NAME=someone"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
