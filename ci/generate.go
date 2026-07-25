// Package ci generates a baseline validation workflow from per-stack blocks,
// reusing the pre-commit system's block composition and custom-section markers.
//
// The generated workflow is deliberately one job doing the checks every repo
// should run. Repos with elaborate pipelines (matrix builds, deploys, image
// publishing) keep those as their own workflow files — this one is additive,
// and callable via workflow_call so a release workflow can gate on it.
package ci

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/toolchain"
)

// WorkflowPath is where the generated workflow lands. Deliberately not ci.yml:
// several repos hand-wrote a ci.yml long before this existed, and generating
// over one would destroy work nothing could recover.
const WorkflowPath = ".github/workflows/validate.yml"

// categoryMap maps block names to the stack that must be detected for the block
// to be included. Blocks not listed are always included.
var categoryMap = map[string]string{
	"go":     "go",
	"python": "python",
	"vue":    "vue",
}

// Generate composes the workflow from blocks, custom sections, and the manifest.
func Generate(blocksFS fs.FS, manifest *toolchain.Toolchain, detected map[string]bool, customSections map[string]string) (string, error) {
	blocks, err := loadBlocks(blocksFS, detected)
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
		"  pull_request:",
		"  workflow_call:",
		"",
		"permissions:",
		"  contents: read",
		"",
		"jobs:",
		"  validate:",
		"    runs-on: ubuntu-latest",
		"    steps:",
	)

	for _, b := range blocks {
		content := manifest.ApplyAll(b.Content)
		desc := precommit.BlockDescription(content)

		if section, ok := customSections["before:"+b.Name]; ok {
			lines = append(lines, "", section)
		}

		stripped := content
		if desc != "" {
			contentLines := strings.Split(content, "\n")
			if len(contentLines) > 0 && strings.TrimSpace(contentLines[0]) == "# "+desc {
				stripped = strings.Join(contentLines[1:], "\n")
			}
		}

		lines = append(lines, "", fmt.Sprintf("# generated:%s - %s", b.Name, desc), strings.TrimRight(stripped, "\n"))

		if section, ok := customSections["after:"+b.Name]; ok {
			lines = append(lines, "", section)
		}
	}

	if section, ok := customSections["after:all"]; ok {
		lines = append(lines, "", section)
	}

	lines = append(lines, "")
	return strings.Join(lines, "\n"), nil
}

// Run generates the workflow and writes it when changed.
// Returns a status message.
func Run(blocksFS fs.FS, manifest *toolchain.Toolchain, detected map[string]bool) (string, error) {
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

	workflow, err := Generate(blocksFS, manifest, detected, customSections)
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

type block struct {
	Name    string
	Content string
}

func loadBlocks(blocksFS fs.FS, detected map[string]bool) ([]block, error) {
	var names []string
	err := fs.WalkDir(blocksFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			return nil
		}
		names = append(names, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	var blocks []block
	for _, path := range names {
		name := precommit.BlockName(path)
		if required, ok := categoryMap[name]; ok && !detected[required] {
			continue
		}
		data, err := fs.ReadFile(blocksFS, path)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block{Name: name, Content: strings.TrimRight(string(data), "\n")})
	}
	return blocks, nil
}
