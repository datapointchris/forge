package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/internal/config"
)

var cfgPath string

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "Run commands across all your git repos",
	Long:  "forge reads your syncer config and executes commands across all (or a subset of) repos.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", config.DefaultConfigPath, "path to syncer config file")
}
