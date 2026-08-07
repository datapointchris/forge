// Package ci generates a baseline validation workflow from per-stack blocks,
// reusing the pre-commit system's block composition and custom-section markers.
//
// One job per declared component, so a repo's separate modules validate in
// parallel and a failure names which one broke. Repos with elaborate pipelines
// (matrix builds, deploys, image publishing) keep those as their own workflow
// files — this one is additive, and callable via workflow_call so a release
// workflow can gate on it.
package ci

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/v4/config"
	"github.com/datapointchris/forge/v4/precommit"
	"github.com/datapointchris/forge/v4/toolchain"
)

// WorkflowPath is where the generated workflow lands. Deliberately not ci.yml:
// several repos hand-wrote a ci.yml long before this existed, and generating
// over one would destroy work nothing could recover.
const WorkflowPath = ".github/workflows/validate.yml"

// Generate composes the workflow from the repo's declared components.
//
// Each component becomes its own job with a working-directory, because a repo
// can hold several of the same stack in different places — nomad's api/ and
// cli/ are both Go modules, deliberately isolated. One serial job would hide
// which one failed and force them to share a setup step.
func Generate(blocksFS fs.FS, manifest *toolchain.Toolchain, components []config.Component, customSections map[string]string) (string, error) {
	shared, err := loadBlock(blocksFS, "checkout")
	if err != nil {
		return "", err
	}

	var lines []string
	lines = append(
		lines,
		fmt.Sprintf("# forge-toolchain: %d", manifest.Version),
		"name: CI",
		"",
		"on:",
		// Development here is trunk-based: work lands on main directly and there
		// are rarely pull requests, so pull_request alone validates almost
		// nothing. workflow_call is how a release gates on this, but a repo with
		// no release pipeline had no trigger at all — the workflow existed and
		// never ran. A repo that does gate its release runs these checks twice on
		// a push to main, which is the cheaper of the two mistakes.
		"  push:",
		"    branches: [main]",
		"  pull_request:",
		"  workflow_call:",
		"",
		"permissions:",
		"  contents: read",
		"",
		"jobs:",
	)

	jobs := 0
	for _, component := range components {
		block, err := loadBlock(blocksFS, component.Stack)
		if err != nil {
			return "", err
		}
		// A declared stack forge has no CI block for yet — docker and terraform
		// are pre-commit concerns today. Silently skipping keeps the map free to
		// declare more than CI currently knows how to build.
		if block == "" {
			continue
		}
		jobs++

		name := JobName(component)
		lines = append(lines, "", fmt.Sprintf("  %s:", name), "    runs-on: ubuntu-latest")
		if component.Dir != "" && component.Dir != "." {
			lines = append(lines, "    defaults:", "      run:", fmt.Sprintf("        working-directory: %s", component.Dir))
		}
		lines = append(lines, "    steps:")

		// Checkout comes first, then before:<stack>. "Before" means before the
		// stack's own steps, not before the repo exists: ichrisbirch decrypts
		// its test secrets out of secrets/ in one of these, which cannot work
		// against a workspace that has not been checked out. Nothing has wanted
		// to run ahead of checkout.
		lines = append(lines, "", applyDir(indentComment(stripDescription(manifest.ApplyAll(shared))), component.Dir))
		if section, ok := customSections["before:"+name]; ok {
			lines = append(lines, "", section)
		}
		lines = append(lines, "", fmt.Sprintf("      # generated:%s", name), applyDir(indentComment(stripDescription(manifest.ApplyAll(block))), component.Dir))
		if section, ok := customSections["after:"+name]; ok {
			lines = append(lines, "", section)
		}
	}

	if jobs == 0 {
		return "", fmt.Errorf("no components with a CI block: nothing to generate")
	}

	if section, ok := customSections["after:all"]; ok {
		lines = append(lines, "", section)
	}

	lines = append(lines, "")
	return strings.Join(lines, "\n"), nil
}

// JobName is the workflow job id for a component: the stack, plus the directory
// when the repo holds more than one component of that stack.
func JobName(component config.Component) string {
	if component.Dir == "" || component.Dir == "." {
		return component.Stack
	}
	return component.Stack + "-" + strings.NewReplacer("/", "-", "_", "-").Replace(component.Dir)
}

// DryRun returns what Run would write, without touching the filesystem and
// without the hand-written-file abort — for verifying generation across the
// portfolio before a rollout.
func DryRun(blocksFS fs.FS, manifest *toolchain.Toolchain, components []config.Component) (string, error) {
	var existing string
	if data, err := os.ReadFile(WorkflowPath); err == nil && strings.HasPrefix(string(data), "# forge-toolchain:") {
		existing = string(data)
	}
	return Generate(blocksFS, manifest, components, precommit.ExtractCustomSections(existing))
}

// Run generates the workflow and writes it when changed.
// Returns a status message.
func Run(blocksFS fs.FS, manifest *toolchain.Toolchain, components []config.Component) (string, error) {
	var existing string
	if data, err := os.ReadFile(WorkflowPath); err == nil {
		existing = string(data)
	}

	// A workflow that predates generation is hand-written by definition —
	// overwriting it would discard work with no way back.
	if existing != "" && !strings.HasPrefix(existing, "# forge-toolchain:") {
		return "", fmt.Errorf("ABORT: %s exists but was not generated (no # forge-toolchain: header)", WorkflowPath)
	}

	customSections := precommit.ExtractCustomSections(existing)

	workflow, err := Generate(blocksFS, manifest, components, customSections)
	if err != nil {
		return "", err
	}

	if existing == workflow {
		return "no changes", nil
	}

	if err := os.MkdirAll(filepath.Dir(WorkflowPath), 0o755); err != nil {
		return "", fmt.Errorf("creating workflow directory: %w", err)
	}
	if err := os.WriteFile(WorkflowPath, []byte(workflow), 0o644); err != nil {
		return "", fmt.Errorf("writing workflow: %w", err)
	}

	if len(customSections) > 0 {
		return fmt.Sprintf("%d custom sections preserved", len(customSections)), nil
	}
	return "generated", nil
}

// loadBlock returns the block whose name matches, or "" when none does.
func loadBlock(blocksFS fs.FS, name string) (string, error) {
	var found string
	err := fs.WalkDir(blocksFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || precommit.BlockName(path) != name {
			return nil
		}
		data, readErr := fs.ReadFile(blocksFS, path)
		if readErr != nil {
			return readErr
		}
		found = strings.TrimRight(string(data), "\n")
		return nil
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

// stripDescription drops a block's leading description comment, which the
// generated: marker replaces.
func stripDescription(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		return strings.Join(lines[1:], "\n")
	}
	return content
}

// indentComment aligns a block's own comments with the steps they annotate.
// Column-zero comments are valid YAML but read as if they escaped the job.
func indentComment(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			lines[i] = "      " + line
		}
	}
	return strings.Join(lines, "\n")
}

// applyDir resolves the {{dir}} placeholder to the component's directory.
// Action inputs (go-version-file, cache-dependency-path) resolve from the
// workspace root regardless of the job's working-directory, so any path in one
// has to carry the component path explicitly.
func applyDir(content, dir string) string {
	if dir == "" {
		dir = "."
	}
	return strings.ReplaceAll(content, "{{dir}}", dir)
}
