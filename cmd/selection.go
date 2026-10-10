package cmd

import (
	"fmt"
	"strings"

	"github.com/datapointchris/goclikit"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/runner"
)

// selectRepos is runner.SelectRepos with a name that matches nothing refused.
// It takes the whole registry, because a name reaches a dormant repo and the
// active portfolio is only what no name selects.
func selectRepos(repos []config.Repo, names []string) ([]config.Repo, error) {
	selected := runner.SelectRepos(repos, names)
	if missing := unmatched(selected, names); len(missing) > 0 {
		return nil, noneMatched("repos", missing)
	}
	return selected, nil
}

// unmatched is each name no selected entry carries. A run that drops one
// reports the rest, and a caller reads the dropped name as having passed.
func unmatched(selected []config.Repo, names []string) []string {
	found := make(map[string]bool, len(selected))
	for _, repo := range selected {
		found[repo.Name] = true
	}
	var missing []string
	for _, name := range names {
		if name = strings.TrimSpace(name); !found[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// noneMatched is a usage error rather than a runtime one: naming something that
// does not exist is the one failure worth retrying with different arguments,
// and it is almost always a typo or a shell that did not split the list.
func noneMatched(many string, missing []string) error {
	return goclikit.UsageError(fmt.Errorf("no %s matched: %s", many, strings.Join(missing, ", ")))
}
