package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/toolchain"
)

var ciDetectedStack string

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "Manage generated CI workflows",
}

var ciGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate .github/workflows/validate.yml from standard blocks",
	RunE:  runCIGenerate,
}

func init() {
	ciGenerateCmd.Flags().StringVar(&ciDetectedStack, "detected", "", "comma-separated detected stack (go,python,vue)")
	ciCmd.AddCommand(ciGenerateCmd)
	rootCmd.AddCommand(ciCmd)
}

func runCIGenerate(_ *cobra.Command, _ []string) error {
	blocksFS, err := resolveCIBlocksFS()
	if err != nil {
		return err
	}
	assetsFS, err := resolvePreCommitFS()
	if err != nil {
		return err
	}
	manifest, err := toolchain.Load(assetsFS)
	if err != nil {
		return err
	}

	detected := make(map[string]bool)
	for _, tech := range strings.Split(ciDetectedStack, ",") {
		if tech = strings.TrimSpace(tech); tech != "" {
			detected[tech] = true
		}
	}

	msg, err := ci.Run(blocksFS, manifest, detected)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// resolveCIBlocksFS returns an fs.FS rooted at the CI blocks directory.
func resolveCIBlocksFS() (fs.FS, error) {
	if diesDir := os.Getenv("FORGE_DIES_DIR"); diesDir != "" {
		forgeRoot := strings.TrimSuffix(diesDir, "/dies")
		forgeRoot = strings.TrimSuffix(forgeRoot, "/dies/")
		blocksDir := forgeRoot + "/ci/blocks"
		if _, err := os.Stat(blocksDir); err != nil {
			return nil, fmt.Errorf("CI blocks directory not found: %s", blocksDir)
		}
		return os.DirFS(blocksDir), nil
	}

	blocksFS, err := fs.Sub(embeddedCI, "ci/blocks")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded CI blocks: %w", err)
	}
	return blocksFS, nil
}
