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
	"regexp"
	"strings"

	"github.com/datapointchris/forge/v6/config"
	"github.com/datapointchris/forge/v6/precommit"
	"github.com/datapointchris/forge/v6/toolchain"
)

// WorkflowPath is where the generated workflow lands. Deliberately not ci.yml:
// several repos hand-wrote a ci.yml long before this existed, and generating
// over one would destroy work nothing could recover.
const WorkflowPath = ".github/workflows/validate.yml"

// releaseWorkflowPath is read, never written — release.yml is per-repo and
// deliberately not generated. See ReleaseGatesOnValidate.
const releaseWorkflowPath = ".github/workflows/release.yml"

// releaseGateRef matches a reusable-workflow call naming this workflow, the
// shape a release uses to gate on it:
//
//	validate:
//	  uses: ./.github/workflows/validate.yml
var releaseGateRef = regexp.MustCompile(`uses:\s*\./` + regexp.QuoteMeta(WorkflowPath))

// ReleaseGatesOnValidate reports whether the repo's release workflow runs this
// one as a job.
//
// Detected rather than declared, which is the opposite of how components work.
// A registry flag would be a second place to remember, and the failure it
// guards against is precisely a thing nobody remembers: add a release gate,
// forget the flag, and the duplicate run comes back silently. Reading the file
// cannot drift from the file.
//
// Every unknown answers false, which reproduces the old behavior — an extra
// run, never a missing one.
//
// Takes the repo root rather than reading the working directory: the dies walk
// many repos in one process, where a relative path answers about whichever repo
// the process happens to be standing in.
func ReleaseGatesOnValidate(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, releaseWorkflowPath))
	if err != nil {
		return false
	}
	return releaseGateRef.Match(data)
}

// Generate composes the workflow from the repo's declared components.
//
// Each component becomes its own job with a working-directory, because a repo
// can hold several of the same stack in different places — nomad's api/ and
// cli/ are both Go modules, deliberately isolated. One serial job would hide
// which one failed and force them to share a setup step.
func Generate(
	blocksFS fs.FS,
	manifest *toolchain.Toolchain,
	components []config.Component,
	customSections map[string]string,
	releaseGated bool,
) (string, error) {
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
	)

	// Development here is trunk-based: work lands on main directly and there are
	// rarely pull requests, so pull_request alone validates almost nothing. A
	// repo with no release pipeline therefore needs push, or the workflow exists
	// and never runs.
	//
	// Where a release does gate on this, push is the *duplicate* rather than the
	// safety net: release.yml fires on the same push and calls this workflow, so
	// emitting push here runs every job twice for one commit. That went unnoticed
	// across fourteen repos because both runs are green — the cost is only ever a
	// confusing history. workflow_call still covers main, so nothing is lost.
	if !releaseGated {
		lines = append(lines,
			"  push:",
			"    branches: [main]",
		)
	}

	lines = append(
		lines,
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
	return Generate(blocksFS, manifest, components, precommit.ExtractCustomSections(existing), ReleaseGatesOnValidate("."))
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

	workflow, err := Generate(blocksFS, manifest, components, customSections, ReleaseGatesOnValidate("."))
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
