package precommit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	markerRE    = regexp.MustCompile(`^# > custom:(before|after):(\S+)`)
	generatedRE = regexp.MustCompile(`^# generated:(\S+)`)
	hookIDRE    = regexp.MustCompile(`^\s+-\s*id:\s*(\S+)`)
)

// categoryMap maps block names to the tech stack category that must be detected
// for the block to be included. Blocks not in this map are always included.
var categoryMap = map[string]string{
	"python-format":  "python",
	"python-lint":    "python",
	"go":             "go",
	"vue":            "vue",
	"docker":         "docker",
	"github-actions": "actions",
	"terraform":      "terraform",
}

// knownAliases are hook IDs that standard blocks intentionally replace.
// They're recognized so the safety check doesn't abort on repos that still have them.
var knownAliases = map[string]bool{
	"bandit":             true,
	"pyupgrade":          true,
	"refurb":             true,
	"prepare-commit-msg": true,
}

// BlockName extracts the block name from a numbered filename.
// "05-file-checks.yml" -> "file-checks"
func BlockName(filename string) string {
	stem := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	idx := strings.Index(stem, "-")
	if idx >= 0 {
		return stem[idx+1:]
	}
	return stem
}

// BlockDescription returns the first comment line's text from a block.
func BlockDescription(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return trimmed[2:]
		}
	}
	return ""
}

// ShouldIncludeBlock returns true if a block should be included for the detected stack.
func ShouldIncludeBlock(blockName string, detected map[string]bool) bool {
	required, ok := categoryMap[blockName]
	return !ok || detected[required]
}

// ExtractCustomSections parses an existing config for custom marker sections.
// Returns a map keyed by "before:BLOCK" or "after:BLOCK".
func ExtractCustomSections(configText string) map[string]string {
	sections := make(map[string]string)
	var currentKey string
	var currentLines []string

	for _, line := range strings.Split(configText, "\n") {
		isCustom := markerRE.FindStringSubmatch(line)
		isGenerated := generatedRE.FindStringSubmatch(line)

		if isCustom != nil || isGenerated != nil {
			if currentKey != "" {
				sections[currentKey] = joinTrimTrailing(currentLines)
			}

			if isCustom != nil {
				currentKey = isCustom[1] + ":" + isCustom[2]
				currentLines = []string{line}
			} else {
				currentKey = ""
				currentLines = nil
			}
		} else if currentKey != "" {
			currentLines = append(currentLines, line)
		}
	}

	if currentKey != "" {
		sections[currentKey] = joinTrimTrailing(currentLines)
	}

	return sections
}

// GetCustomHookIDs collects all hook IDs defined in custom sections.
func GetCustomHookIDs(customSections map[string]string) map[string]bool {
	ids := make(map[string]bool)
	for _, content := range customSections {
		for _, line := range strings.Split(content, "\n") {
			if m := hookIDRE.FindStringSubmatch(line); m != nil {
				ids[m[1]] = true
			}
		}
	}
	return ids
}

// StripHooksFromBlock removes hooks with the given IDs from a block's YAML content.
func StripHooksFromBlock(content string, hookIDs map[string]bool) string {
	if len(hookIDs) == 0 {
		return content
	}

	var result []string
	skip := false

	for _, line := range strings.Split(content, "\n") {
		if m := hookIDRE.FindStringSubmatch(line); m != nil {
			skip = hookIDs[m[1]]
		} else if skip {
			trimmed := strings.TrimSpace(line)
			isNewHook := regexp.MustCompile(`^\s+-\s*id:`).MatchString(line)
			isNewRepo := regexp.MustCompile(`^\s+-\s*repo:`).MatchString(line)
			isComment := strings.HasPrefix(trimmed, "#")

			if isNewHook {
				if m := hookIDRE.FindStringSubmatch(line); m != nil {
					skip = hookIDs[m[1]]
				} else {
					skip = false
				}
			} else if isNewRepo || isComment || trimmed == "" {
				skip = false
			}
		}

		if !skip {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// block holds a parsed block file.
type block struct {
	Name    string
	Content string
	Desc    string
}

// Generate composes a .pre-commit-config.yaml from blocks and custom sections.
func Generate(blocksFS fs.FS, detected map[string]bool, customSections map[string]string) (string, error) {
	blocks, err := loadBlocks(blocksFS, detected)
	if err != nil {
		return "", err
	}

	customIDs := GetCustomHookIDs(customSections)

	var lines []string
	lines = append(lines, "fail_fast: true")
	lines = append(lines, "default_stages: [pre-commit]")
	lines = append(lines, "repos:")

	for _, b := range blocks {
		content := StripHooksFromBlock(b.Content, customIDs)
		desc := BlockDescription(content)

		// Insert custom hooks that go BEFORE this block
		if section, ok := customSections["before:"+b.Name]; ok {
			lines = append(lines, "")
			lines = append(lines, section)
		}

		// Strip leading description comment (it's moved to the generated: header)
		stripped := content
		if desc != "" {
			contentLines := strings.Split(content, "\n")
			if len(contentLines) > 0 && strings.TrimSpace(contentLines[0]) == "# "+desc {
				stripped = strings.Join(contentLines[1:], "\n")
			}
		}

		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("# generated:%s - %s", b.Name, desc))
		lines = append(lines, stripped)

		// Insert custom hooks that go AFTER this block
		if section, ok := customSections["after:"+b.Name]; ok {
			lines = append(lines, "")
			lines = append(lines, section)
		}
	}

	// Custom hooks after everything
	if section, ok := customSections["after:all"]; ok {
		lines = append(lines, "")
		lines = append(lines, section)
	}

	lines = append(lines, "")
	return strings.Join(lines, "\n"), nil
}

// GetExistingHookIDs extracts all hook IDs from an existing config.
func GetExistingHookIDs(configText string) map[string]bool {
	ids := make(map[string]bool)
	for _, line := range strings.Split(configText, "\n") {
		if m := hookIDRE.FindStringSubmatch(line); m != nil {
			ids[m[1]] = true
		}
	}
	return ids
}

// GetStandardHookIDs extracts all hook IDs from standard block files.
func GetStandardHookIDs(blocksFS fs.FS) (map[string]bool, error) {
	ids := make(map[string]bool)

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
		data, err := fs.ReadFile(blocksFS, path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if m := hookIDRE.FindStringSubmatch(line); m != nil {
				ids[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for alias := range knownAliases {
		ids[alias] = true
	}
	return ids, nil
}

// SafetyCheck returns unknown hook IDs that exist in the current config but aren't
// in standard blocks and have no custom markers. Non-empty result means abort.
func SafetyCheck(configText string, blocksFS fs.FS, customSections map[string]string) ([]string, error) {
	if len(customSections) > 0 {
		return nil, nil
	}

	existing := GetExistingHookIDs(configText)
	standard, err := GetStandardHookIDs(blocksFS)
	if err != nil {
		return nil, err
	}

	var unknown []string
	for id := range existing {
		if !standard[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return unknown, nil
}

// Run executes the full generation pipeline: read existing config from CWD,
// extract custom sections, run safety check, generate, write if changed.
// Returns a status message and error.
func Run(blocksFS fs.FS, detected map[string]bool) (string, error) {
	configPath := ".pre-commit-config.yaml"

	var configText string
	data, err := os.ReadFile(configPath)
	if err == nil {
		configText = string(data)
	}

	customSections := ExtractCustomSections(configText)

	// Safety check
	if configText != "" {
		unknown, err := SafetyCheck(configText, blocksFS, customSections)
		if err != nil {
			return "", fmt.Errorf("safety check: %w", err)
		}
		if len(unknown) > 0 {
			return "", fmt.Errorf("ABORT: %d unrecognized hooks with no custom markers: %s\nAdd # > custom:POSITION markers to preserve them, then re-run",
				len(unknown), strings.Join(unknown, ", "))
		}
	}

	config, err := Generate(blocksFS, detected, customSections)
	if err != nil {
		return "", err
	}

	if configText == config {
		return "no changes", nil
	}

	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		return "", fmt.Errorf("writing config: %w", err)
	}

	if len(customSections) > 0 {
		return fmt.Sprintf("%d custom sections preserved", len(customSections)), nil
	}
	return "generated", nil
}

// loadBlocks reads and filters block files from the filesystem.
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
		if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning blocks: %w", err)
	}

	sort.Strings(names)

	var blocks []block
	for _, name := range names {
		blockName := BlockName(name)
		if !ShouldIncludeBlock(blockName, detected) {
			continue
		}
		data, err := fs.ReadFile(blocksFS, name)
		if err != nil {
			return nil, fmt.Errorf("reading block %s: %w", name, err)
		}
		blocks = append(blocks, block{
			Name:    blockName,
			Content: strings.TrimRight(string(data), "\n"),
			Desc:    BlockDescription(string(data)),
		})
	}

	return blocks, nil
}

func joinTrimTrailing(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
