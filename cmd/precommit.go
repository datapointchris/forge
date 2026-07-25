package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/datapointchris/forge/toolchain"

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

var (
	detectedStack   string
	precommitDryRun bool
)

var precommitCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Report hooks that would abort a sync (exit 1 if any)",
	RunE:  runPrecommitCheck,
	// Failure is the command's normal signal, not a misuse of it.
	SilenceUsage: true,
}

func runPrecommitCheck(_ *cobra.Command, _ []string) error {
	assetsFS, err := resolvePreCommitFS()
	if err != nil {
		return err
	}
	blocksFS, err := fs.Sub(assetsFS, "blocks")
	if err != nil {
		return fmt.Errorf("accessing blocks: %w", err)
	}

	unknown, err := precommit.Check(blocksFS)
	if err != nil {
		return err
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%d hooks need a # > custom: marker: %s", len(unknown), strings.Join(unknown, ", "))
	}
	fmt.Println("no unmarked hooks")
	return nil
}

func init() {
	precommitGenerateCmd.Flags().StringVar(&detectedStack, "detected", "", "override the registry's declared stack (python,go,vue,docker,actions,terraform)")
	precommitGenerateCmd.Flags().BoolVar(&precommitDryRun, "dry-run", false, "print the config instead of writing it")

	precommitCmd.AddCommand(precommitGenerateCmd)
	precommitCmd.AddCommand(precommitCheckCmd)
	rootCmd.AddCommand(precommitCmd)
}

func runPrecommitGenerate(cmd *cobra.Command, args []string) error {
	assetsFS, err := resolvePreCommitFS()
	if err != nil {
		return err
	}
	blocksFS, err := fs.Sub(assetsFS, "blocks")
	if err != nil {
		return fmt.Errorf("accessing blocks: %w", err)
	}
	manifest, err := toolchain.Load(assetsFS)
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
	} else {
		// Same declared source as CI: the registry, never a filesystem probe.
		components, err := declaredComponents()
		if err != nil {
			return err
		}
		for _, component := range components {
			detected[stackToCategory(component.Stack)] = true
		}
	}

	if precommitDryRun {
		config, err := precommit.DryRun(blocksFS, manifest, detected)
		if err != nil {
			return err
		}
		fmt.Print(config)
		return nil
	}

	msg, err := precommit.Run(blocksFS, manifest, detected)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// resolvePreCommitFS returns an fs.FS rooted at the pre-commit asset directory,
// the parent of both the blocks and the toolchain manifest.
// Uses filesystem when dies_dir is configured, embedded otherwise.
func resolvePreCommitFS() (fs.FS, error) {
	if diesDir := os.Getenv("FORGE_DIES_DIR"); diesDir != "" {
		// Filesystem mode: assets are sibling to dies dir
		forgeRoot := strings.TrimSuffix(diesDir, "/dies")
		forgeRoot = strings.TrimSuffix(forgeRoot, "/dies/")
		assetsDir := forgeRoot + "/pre-commit"
		if _, err := os.Stat(assetsDir + "/blocks"); err != nil {
			return nil, fmt.Errorf("blocks directory not found: %s/blocks", assetsDir)
		}
		return os.DirFS(assetsDir), nil
	}

	// Embedded mode
	assetsFS, err := fs.Sub(embeddedPreCommit, "pre-commit")
	if err != nil {
		return nil, fmt.Errorf("accessing embedded assets: %w", err)
	}
	return assetsFS, nil
}

// stackToCategory maps a declared stack to the pre-commit block category it
// enables. Most are identity; the registry names the technology, while the
// block category names the toolchain that lints it.
func stackToCategory(stack string) string {
	switch stack {
	case "actions":
		return "actions"
	case "node", "astro":
		return "vue"
	default:
		return stack
	}
}
