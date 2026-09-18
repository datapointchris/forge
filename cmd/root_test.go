package cmd

import (
	"slices"
	"strings"
	"testing"
)

// A word one slip from a subcommand is answered with the subcommand, so a
// mistyped verb names the one that was meant.
func TestAnUnknownSubcommandNamesTheNearOnes(t *testing.T) {
	repos, _, err := rootCmd.Find([]string{"repos"})
	if err != nil {
		t.Fatalf("find repos: %v", err)
	}
	refused := requireSubcommand(repos, []string{"plam"})
	if refused == nil || !slices.Contains(strings.Fields(refused.Error()), "plan") {
		t.Errorf("repos plam answered %v, want it to name plan", refused)
	}
}
