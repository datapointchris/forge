package cmd

import (
	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate"
)

// updateConfig describes where forge's releases come from. Shared by the
// `update` command and the daily check in Execute, so the two cannot point at
// different releases.
func updateConfig() goselfupdate.Config {
	return goselfupdate.Config{
		Owner:   "datapointchris",
		Repo:    "forge",
		Binary:  "forge",
		Version: buildVersion(),
	}
}

func init() {
	rootCmd.AddCommand(goclikit.UpdateCommand(updateConfig()))
}
