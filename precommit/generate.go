package precommit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/toolchain"
)

// Both marker regexes tolerate leading whitespace because the same extractor
// serves .pre-commit-config.yaml, where markers sit at column zero, and
// validate.yml, where everything inside a job is indented six spaces. Anchored
// at ^ they matched nothing in a workflow: a before:/after:<block> section was
// dropped on the next sync, and generatedRE could not terminate a section, so
// after:<block> ran on and swallowed the following jobs.
var (
	markerRE    = regexp.MustCompile(`^\s*# > custom:(before|after):(\S+)`)
	generatedRE = regexp.MustCompile(`^\s*# generated:(\S+)`)
	hookIDRE    = regexp.MustCompile(`^\s+-\s*id:\s*(\S+)`)
	hookNameRE  = regexp.MustCompile(`^(\s+name:\s*)(.+)$`)
	repoLineRE  = regexp.MustCompile(`^\s*-\s*repo:\s*(\S+)`)
)

// categoryMap maps a block to the declared stack category that pulls it in.
//
// Three categories are gated by something other than a component, and Generate
// seeds each one itself: sql by a declared dialect, git by the target being
// versioned, and python-scripts by the scan finding a file identify cannot tag.
//
// git is gated that way because its block hooks commit-msg, which only fires on
// a commit. A target git does not version would carry a hook that can never run,
// and nothing would say so, because uninstalledHooks reports no missing stages
// where there is no .git to install them into.
var categoryMap = map[string]string{
	"conventional-commits": "git",
	"python-format":        "python",
	"python-lint":          "python",
	"python-scripts":       ScriptCategory,
	"go":                   "go",
	"go-release-major":     "go",
	"vue":                  "vue",
	"rust":                 "rust",
	"lua":                  "lua",
	"docker":               "docker",
	"sql":                  "sql",
	"github-actions":       "actions",
	"terraform":            "terraform",
}

// genericBlocks apply to every target regardless of stack. Membership is
// declared rather than inferred from categoryMap's gaps: rust and lua were
// absent from both for months, which read as generic, and every Go repo was
// being handed cargo-clippy and stylua hooks.
var genericBlocks = map[string]bool{
	"file-checks": true,
	"markdown":    true,
	"shell":       true,
	"codespell":   true,
	"refcheck":    true,
}

// knownAliases are hook IDs that standard blocks intentionally replace, listed
// so the safety check does not abort on a repo that still names the old id.
//
// Only 1:1 replacements belong here. A hook implying behavior no standard block
// provides — pytest, vitest, or a per-module go-vet-api — must keep triggering
// the abort, because aliasing it away silently drops that coverage. Those need a
// # > custom: marker instead.
var knownAliases = map[string]bool{
	// Superseded by ruff's rule sets.
	"flake8":       true,
	"bandit":       true,
	"pyupgrade":    true,
	"refurb":       true,
	"black":        true,
	"blacken-docs": true,
	"isort":        true,
	"ruff":         true,
	// Superseded by the uv-lock hook.
	"poetry-check":  true,
	"poetry-export": true,
	"poetry-lock":   true,
	// Same tool, different id, in the vue block.
	"eslint":           true,
	"prettier":         true,
	"stylelint":        true,
	"typecheck":        true,
	"typescript-check": true,
	// Same tool, different id, in the go block.
	"gofmt":   true,
	"gofumpt": true,
	// Same tool, different id, in the lua block.
	"stylua": true,
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

// ShouldIncludeBlock reports whether a block applies to the declared components.
// A block belonging to neither table is an error, not a default.
func ShouldIncludeBlock(blockName string, dirs map[string][]string) (bool, error) {
	if genericBlocks[blockName] {
		return true, nil
	}
	required, ok := categoryMap[blockName]
	if !ok {
		return false, fmt.Errorf("block %q is in neither categoryMap nor genericBlocks", blockName)
	}
	return len(dirs[required]) > 0, nil
}

// StackToCategory maps a declared stack to the block category that lints it.
// Most are identity; the registry names the technology, the block names the
// toolchain.
// A bespoke Astro blog keeps its own prettier and eslint setup in its
// package.json; it is deliberately not held to the shared frontend block, so it
// maps to a category no block claims and gets the generic hooks only.
func StackToCategory(stack string) string {
	switch stack {
	case "node":
		return "vue"
	default:
		return stack
	}
}

// dirsByCategory groups the declared component directories under the block
// category that lints them, preserving declaration order and dropping repeats.
func dirsByCategory(components []config.Component) map[string][]string {
	dirs := make(map[string][]string)
	for _, component := range components {
		category := StackToCategory(component.Stack)
		dir := component.Dir
		if dir == "" {
			dir = "."
		}
		if slices.Contains(dirs[category], dir) {
			continue
		}
		dirs[category] = append(dirs[category], dir)
	}
	return dirs
}

// renderForDirs resolves a block's directory placeholders, once per directory the
// stack was declared in. Identical renders collapse: the Go hooks walk every
// go.mod in the repo already, so api/ and cli/ produce one copy, not two.
//
// When a category genuinely renders more than once, hook ids and names take the
// directory as a suffix — pre-commit reports failures by hook, and two hooks
// called vue-eslint would leave no way to tell which app broke.
func renderForDirs(b block, dirs []string) string {
	if len(dirs) == 0 {
		return b.Content
	}

	var renders []string
	for _, dir := range dirs {
		render := applyDir(b.Content, dir)
		if !slices.Contains(renders, render) {
			renders = append(renders, render)
		}
	}
	if len(renders) == 1 {
		return renders[0]
	}

	var suffixed []string
	for index, render := range renders {
		suffix := "-" + strings.NewReplacer("/", "-", "_", "-").Replace(dirs[index])
		suffixed = append(suffixed, suffixHookNames(render, suffix))
	}
	return strings.Join(suffixed, "\n")
}

// applyDir resolves {{dir}} for a `cd` target and {{dirprefix}} for a path
// anchor in a files: pattern. A root component needs "." for the first and an
// empty string for the second — "^\./" matches nothing pre-commit ever passes.
func applyDir(content, dir string) string {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	return strings.NewReplacer("{{dir}}", dir, "{{dirprefix}}", prefix).Replace(content)
}

func suffixHookNames(content, suffix string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if m := hookIDRE.FindStringSubmatch(line); m != nil {
			lines[i] = line + suffix
			continue
		}
		if m := hookNameRE.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + strings.TrimSpace(m[2]) + suffix
		}
	}
	return strings.Join(lines, "\n")
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

// StripHooksFromBlock removes hooks with the given IDs from a block's YAML
// content.
//
// A hook's extent is decided by indentation, not by what its lines look like.
// Treating any comment as the end of a hook left the body of a stripped hook
// behind as orphaned keys, which is invalid YAML — and a hook is exactly where
// a comment explaining a non-obvious entry belongs.
func StripHooksFromBlock(content string, hookIDs map[string]bool) string {
	if len(hookIDs) == 0 {
		return content
	}

	var result []string
	stripping := -1

	for _, line := range strings.Split(content, "\n") {
		if stripping >= 0 {
			if strings.TrimSpace(line) == "" || indentOf(line) > stripping {
				continue
			}
			stripping = -1
		}
		if m := hookIDRE.FindStringSubmatch(line); len(m) > 1 && hookIDs[m[1]] {
			stripping = indentOf(line)
			continue
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// containsHook reports whether any line declares a hook. hookIDRE anchors at
// the start of a line, so it only ever matches one line at a time.
func containsHook(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if hookIDRE.MatchString(line) {
			return true
		}
	}
	return false
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// dropEmptyRepoEntries removes repo entries left with no hooks, which pre-commit
// rejects outright. Two things produce one: a custom section overriding the only
// hook a repo provides, and a marker placed mid-file — a section runs to the next
// marker or EOF, so it absorbs whatever sits below it, hooks included.
func dropEmptyRepoEntries(content string) string {
	var result []string
	var entry []string
	hasHook := false

	flush := func() {
		if hasHook {
			result = append(result, entry...)
		}
		entry = nil
		hasHook = false
	}

	for _, line := range strings.Split(content, "\n") {
		if repoLineRE.MatchString(line) {
			flush()
			entry = []string{line}
			continue
		}
		if entry == nil {
			result = append(result, line)
			continue
		}
		entry = append(entry, line)
		if hookIDRE.MatchString(line) {
			hasHook = true
		}
	}
	flush()

	return strings.Join(result, "\n")
}

// block holds a parsed block file.
type block struct {
	Name    string
	Content string
	Desc    string
}

// Generate composes a .pre-commit-config.yaml from blocks and custom sections.
// Tool versions come from the toolchain manifest, not the blocks' own rev lines.
//
// Components carry where each stack lives, so a stack block is rendered once per
// directory it was declared in. A repo whose frontend is in web/ and one whose
// frontend is in frontend/ get hooks that enter the right directory — the block
// itself names neither.
func Generate(
	blocksFS fs.FS,
	manifest *toolchain.Toolchain,
	declared *config.Toolchain,
	customSections map[string]string,
	versioned bool,
	scripts []string,
) (string, error) {
	dirs := dirsByCategory(declared.Components)
	// The SQL block is gated by a declared dialect rather than by a component:
	// a repo's .sql files are not a build surface with a directory of their own,
	// and no probe can tell postgres from sqlite by looking at them.
	if declared.SQLDialect != "" {
		dirs["sql"] = []string{"."}
	}
	// The commit-stage blocks are gated by git being present, for the reason in
	// categoryMap. Seeded exactly as sql is: neither has a build directory of
	// its own, so both take the root.
	if versioned {
		dirs["git"] = []string{"."}
	}
	// Seeded from the scan for the same reason: an app identify cannot tag is
	// not a build surface with a directory of its own, and no declaration can
	// name it without going stale the next time one is added.
	scriptsPattern := ScriptsPattern(scripts)
	if scriptsPattern != "" {
		dirs[ScriptCategory] = []string{"."}
	}
	blocks, err := loadBlocks(blocksFS, dirs)
	if err != nil {
		return "", err
	}

	customIDs := GetCustomHookIDs(customSections)

	var lines []string
	lines = append(lines, fmt.Sprintf("# forge-toolchain: %d", manifest.Version))
	lines = append(lines, "fail_fast: true")
	lines = append(lines, "default_stages: [pre-commit]")
	if declared.Exclude != "" {
		lines = append(lines, fmt.Sprintf("exclude: %s", declared.Exclude))
	}
	lines = append(lines, "repos:")

	for _, b := range blocks {
		rendered := strings.NewReplacer(
			"{{dialect}}", declared.SQLDialect,
			"{{scripts}}", scriptsPattern,
		).Replace(renderForDirs(b, dirs[categoryMap[b.Name]]))
		content := manifest.ApplyRevs(StripHooksFromBlock(rendered, customIDs))
		content = dropEmptyRepoEntries(content)
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

		// A block every one of whose hooks the repo overrides contributes nothing
		// but its header, which reads as if generation dropped the hooks.
		if containsHook(stripped) {
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("# generated:%s - %s", b.Name, desc))
			lines = append(lines, stripped)
		}

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
//
// Marking one hook must not disarm the check for the rest of the file: a repo is
// marked up incrementally, and the first marker added is exactly when an unmarked
// sibling is most likely to be sitting one block away.
func SafetyCheck(configText string, blocksFS fs.FS, declared *config.Toolchain, customSections map[string]string) ([]string, error) {
	existing := GetExistingHookIDs(configText)
	marked := GetCustomHookIDs(customSections)
	standard, err := GetStandardHookIDs(blocksFS)
	if err != nil {
		return nil, err
	}
	standard = withDirSuffixes(standard, declared)

	var unknown []string
	for id := range existing {
		if !standard[id] && !marked[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return unknown, nil
}

// withDirSuffixes adds the per-directory ids renderForDirs produces when one
// category's renders differ, which the block files themselves never contain.
//
// Without this a repo is rejected by its own generated config: timeline holds
// three npm packages, so its vue hooks are written as vue-eslint-client,
// -server and -shared, and the second sync aborted on all nine as unrecognized.
// It stayed a version behind the fleet because of it. Only suffixes built from
// a declared directory are accepted, so a genuinely unknown hook that merely
// starts with a standard id is still reported.
func withDirSuffixes(standard map[string]bool, declared *config.Toolchain) map[string]bool {
	if declared == nil {
		return standard
	}

	widened := make(map[string]bool, len(standard))
	for id := range standard {
		widened[id] = true
	}
	replacer := strings.NewReplacer("/", "-", "_", "-")
	for _, component := range declared.Components {
		if component.Dir == "" || component.Dir == "." {
			continue
		}
		suffix := "-" + replacer.Replace(component.Dir)
		for id := range standard {
			widened[id+suffix] = true
		}
	}
	return widened
}

// Check reports the hook IDs that would abort a real Run: present in the
// existing config, not provided by a standard block, and not under a custom
// marker. Empty means a sync would land cleanly.
func Check(blocksFS fs.FS, declared *config.Toolchain) ([]string, error) {
	data, err := os.ReadFile(".pre-commit-config.yaml")
	if err != nil {
		return nil, nil // no config yet; nothing to preserve
	}
	configText := string(data)
	return SafetyCheck(configText, blocksFS, declared, ExtractCustomSections(configText))
}

// loadBlocks reads and filters block files from the filesystem.
func loadBlocks(blocksFS fs.FS, dirs map[string][]string) ([]block, error) {
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
		include, err := ShouldIncludeBlock(blockName, dirs)
		if err != nil {
			return nil, err
		}
		if !include {
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

// ShellcheckDisables renders a repo's declared exceptions as the block appended
// to its deployed .shellcheckrc.
//
// Printed as an appendix rather than merged into the shared config, so one
// writer owns that file and a repo declaring nothing still gets the shared
// config byte for byte. Each exception carries its reason so it can be
// re-examined instead of inherited.
func ShellcheckDisables(declared *config.Toolchain) string {
	if declared == nil || len(declared.ShellcheckDisable) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("\n# Repo-specific exceptions, declared in the registry rather than written\n")
	out.WriteString("# here — see toolchain.shellcheck_disable. Each one carries its reason so\n")
	out.WriteString("# it can be re-examined instead of inherited.\n")
	for _, exception := range declared.ShellcheckDisable {
		out.WriteString("\n")
		for _, line := range strings.Split(exception.Reason, "\n") {
			fmt.Fprintf(&out, "# %s\n", line)
		}
		fmt.Fprintf(&out, "disable=%s\n", exception.Rule)
	}
	return out.String()
}
