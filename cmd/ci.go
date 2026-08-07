package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/v4/ci"
	"github.com/datapointchris/forge/v4/config"
	"github.com/datapointchris/forge/v4/toolchain"
)

var ciDryRun bool

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
	ciGenerateCmd.Flags().BoolVar(&ciDryRun, "dry-run", false, "print the workflow instead of writing it")
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

	declared, err := declaredToolchain()
	if err != nil {
		return err
	}
	components := declared.Components
	// A repo with nothing declared has nothing to build. Pre-commit still has a
	// generic baseline to give it; CI does not.
	if len(components) == 0 {
		return fmt.Errorf("declares no toolchain.components in the registry")
	}

	if ciDryRun {
		workflow, err := ci.DryRun(blocksFS, manifest, components)
		if err != nil {
			return err
		}
		fmt.Print(workflow)
		return nil
	}

	msg, err := ci.Run(blocksFS, manifest, components)
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

// declaredToolchain resolves the current directory to its registry entry and
// returns its declared build surface. Declared, never detected: the portfolio
// has five different conventions for where a Go service lives, no probe
// survives all of them, and a fact like the SQL dialect is not derivable from a
// layout at any level of tidiness.
func declaredToolchain() (*config.Toolchain, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	syncerCfg, err := loadRepos()
	if err != nil {
		return nil, fmt.Errorf("loading repo registry: %w", err)
	}

	repo := config.FindRepoByPath(syncerCfg.Repos, cwd)
	if repo == nil {
		return nil, fmt.Errorf("%s is not in the repo registry", cwd)
	}
	if repo.Toolchain == nil {
		return &config.Toolchain{}, nil
	}
	return repo.Toolchain, nil
}
