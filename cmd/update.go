package cmd

import (
	"github.com/datapointchris/goselfupdate"
	"github.com/datapointchris/goselfupdate/cobracmd"
)

func init() {
	rootCmd.AddCommand(cobracmd.New(goselfupdate.Config{
		Owner:   "datapointchris",
		Repo:    "forge",
		Binary:  "forge",
		Version: buildVersion(),
	}))
}
