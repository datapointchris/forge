package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/toolchain"
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

	declared, err := resolveToolchain()
	if err != nil {
		return err
	}

	unknown, err := precommit.Check(blocksFS, declared)
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
	precommitCmd.AddCommand(precommitStacksCmd)
	precommitCmd.AddCommand(precommitShellcheckDisablesCmd)
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

	declared, err := resolveToolchain()
	if err != nil {
		return err
	}

	if precommitDryRun {
		generated, err := precommit.DryRun(blocksFS, manifest, declared)
		if err != nil {
			return err
		}
		fmt.Print(generated)
		return nil
	}

	msg, err := precommit.Run(blocksFS, manifest, declared)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// resolveToolchain returns the repo's declared build surface, or synthesizes one
// from --detected. The override exists for a repo not yet in the registry; it
// cannot express where a stack lives or which SQL dialect the repo speaks, so
// everything lands at root and SQL is left out.
func resolveToolchain() (*config.Toolchain, error) {
	if detectedStack == "" {
		// Same declared source as CI: the registry, never a filesystem probe.
		return declaredToolchain()
	}

	declared := &config.Toolchain{}
	for _, stack := range strings.Split(detectedStack, ",") {
		stack = strings.TrimSpace(stack)
		if stack != "" {
			declared.Components = append(declared.Components, config.Component{Stack: stack, Dir: "."})
		}
	}
	return declared, nil
}

var precommitStacksCmd = &cobra.Command{
	Use:   "stacks",
	Short: "Print the block categories this repo declares, one per line",
	RunE:  runPrecommitStacks,
}

var precommitShellcheckDisablesCmd = &cobra.Command{
	Use:   "shellcheck-disables",
	Short: "Print this repo's declared shellcheck exceptions as .shellcheckrc lines",
	RunE:  runPrecommitShellcheckDisables,
}

// runPrecommitShellcheckDisables emits the block the sync die appends to the
// deployed .shellcheckrc. Printing rather than writing keeps one writer for
// that file, so a repo declaring nothing still gets the shared config byte for
// byte and the die's diff check keeps working.
func runPrecommitShellcheckDisables(_ *cobra.Command, _ []string) error {
	declared, err := resolveToolchain()
	if err != nil {
		return err
	}
	if len(declared.ShellcheckDisable) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("# Repo-specific exceptions, declared in the registry rather than written")
	fmt.Println("# here — see toolchain.shellcheck_disable. Each one carries its reason so")
	fmt.Println("# it can be re-examined instead of inherited.")
	for _, exception := range declared.ShellcheckDisable {
		fmt.Println()
		for _, line := range strings.Split(exception.Reason, "\n") {
			fmt.Printf("# %s\n", line)
		}
		fmt.Printf("disable=%s\n", exception.Rule)
	}
	return nil
}

// runPrecommitStacks exists so the sync die can deploy the right tool configs
// from the same declared source the generator reads, rather than re-probing the
// filesystem and disagreeing with it.
func runPrecommitStacks(_ *cobra.Command, _ []string) error {
	declared, err := resolveToolchain()
	if err != nil {
		return err
	}

	var categories []string
	for _, component := range declared.Components {
		category := precommit.StackToCategory(component.Stack)
		if !slices.Contains(categories, category) {
			categories = append(categories, category)
		}
	}
	// Not a component — a declared dialect is what pulls in the SQL block, and
	// the die deploys its ruleset on the same signal.
	if declared.SQLDialect != "" {
		categories = append(categories, "sql")
	}
	sort.Strings(categories)
	for _, category := range categories {
		fmt.Println(category)
	}
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
