package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/precommit"
)

var precommitCmd = &cobra.Command{
	Use:   "precommit",
	Short: "Pre-commit config management",
}

var precommitGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate .pre-commit-config.yaml from standard blocks",
	Long: `Generate a .pre-commit-config.yaml in the current directory from standard
block templates, filtered by the detected tech stack.

Preserves custom hooks marked with # > custom:POSITION markers.
Aborts if unrecognized hooks exist without markers.`,
	RunE: runPrecommitGenerate,
}

var detectedStack string

func init() {
	precommitGenerateCmd.Flags().StringVar(&detectedStack, "detected", "", "comma-separated detected tech stack (python,go,vue,docker,actions,terraform)")

	precommitCmd.AddCommand(precommitGenerateCmd)
	rootCmd.AddCommand(precommitCmd)
}

func runPrecommitGenerate(cmd *cobra.Command, args []string) error {
	blocksFS, err := resolveBlocksFS()
	if err != nil {
		return err
	}

	detected := make(map[string]bool)
	if detectedStack != "" {
		for _, tech := range strings.Split(detectedStack, ",") {
			tech = strings.TrimSpace(tech)
			if tech != "" {
				detected[tech] = true
			}
		}
	}

	msg, err := precommit.Run(blocksFS, detected)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// resolveBlocksFS returns an fs.FS rooted at the blocks directory.
// Uses filesystem when dies_dir is configured, embedded otherwise.
func resolveBlocksFS() (fs.FS, error) {
	if diesDir := os.Getenv("FORGE_DIES_DIR"); diesDir != "" {
		// Filesystem mode: blocks are sibling to dies dir
		forgeRoot := strings.TrimSuffix(diesDir, "/dies")
		forgeRoot = strings.TrimSuffix(forgeRoot, "/dies/")
		blocksDir := forgeRoot + "/pre-commit/blocks"
		if _, err := os.Stat(blocksDir); err != nil {
			return nil, fmt.Errorf("blocks directory not found: %s", blocksDir)
		}
		return os.DirFS(blocksDir), nil
	}

	// Embedded mode
	blocksFS, err := fs.Sub(embeddedPreCommit, "pre-commit/blocks")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded blocks: %w", err)
	}
	return blocksFS, nil
}
