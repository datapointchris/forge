package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/toolchain"
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

	components, err := declaredComponents()
	if err != nil {
		return err
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

// declaredComponents resolves the current directory to its registry entry and
// returns its declared components. Declared, never detected: the portfolio has
// five different conventions for where a Go service lives, and no probe
// survives all of them.
func declaredComponents() ([]config.Component, error) {
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
	if repo.Toolchain == nil || len(repo.Toolchain.Components) == 0 {
		return nil, fmt.Errorf("%s declares no toolchain.components in the registry", repo.Name)
	}
	return repo.Toolchain.Components, nil
}
