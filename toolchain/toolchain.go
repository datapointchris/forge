package toolchain

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the manifest path relative to the directory Load reads.
const File = "toolchain.yml"

// Pin is what a block writes where a version belongs. Every version a generated
// file carries comes from the versions file, so a block names none of its own.
// A pin the versions file does not fill survives rendering, and Unpinned names
// it so the generator can refuse the file rather than ship the placeholder.
const Pin = "{{pin}}"

// StampPrefix opens the first line of every file forge generates from the
// declaration, and the declaration's stamp version follows it. A file at a
// generated path without it is hand-written, which is what keeps forge from
// overwriting one.
const StampPrefix = "# forge-toolchain: "

// StampLine is the 1-based line of a generated file that carries the stamp.
const StampLine = 1

// Stamp is the first line of a file generated from this declaration.
func (t *Toolchain) Stamp() string {
	return StampFor(t.Version)
}

// StampFor is the first line of a file generated at stamp version.
func StampFor(version int) string {
	return StampPrefix + strconv.Itoa(version)
}

var (
	repoLineRE    = regexp.MustCompile(`^(\s*-\s*repo:\s*)(\S+)\s*$`)
	revLineRE     = regexp.MustCompile(`^(\s*rev:\s*)(\S+)\s*$`)
	usesLineRE    = regexp.MustCompile(`^(\s*-?\s*uses:\s*)([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)@\S+(.*)$`)
	goInstallRE   = regexp.MustCompile(`^(.*go install\s+)(\S+?)@\S+(.*)$`)
	runtimeLineRE = regexp.MustCompile(`^(\s*)([a-z]+)-version:\s*\S+\s*$`)
	// The name may hold underscores (bats_support), so the greedy class has to
	// include one and let backtracking find the final `_version`. Without that a
	// multi-word tool silently never matches, and ships whatever version the
	// block happens to name — the exact drift the manifest exists to prevent.
	binaryLineRE = regexp.MustCompile(`^(\s*)([a-z0-9_]+)_version="\S+"\s*$`)
	uvxLineRE    = regexp.MustCompile(`^(.*\buvx\s+)([a-z0-9-]+)@(\S+)(.*)$`)
	// A YAML list item naming a Go module at a version, which is how a
	// `language: golang` hook's additional_dependencies spells what it installs.
	dependencyLineRE = regexp.MustCompile(`^(\s*-\s+)([A-Za-z0-9._~/-]+)@(\S+)\s*$`)
	// A full commit id, which is a stronger pin than any tag the manifest names.
	commitRefRE = regexp.MustCompile(`@[0-9a-f]{40}\b`)
)

// hookPinnedTools maps a tool generated CI runs to the pre-commit repo whose rev
// pins it. CI takes the hook's release rather than a version of its own, so the
// two cannot disagree about a finding: a second entry could drift, and a derived
// one cannot.
var hookPinnedTools = map[string]string{
	"ruff":       "https://github.com/astral-sh/ruff-pre-commit",
	"shellcheck": "https://github.com/koalaman/shellcheck-precommit",
	"shfmt":      "https://github.com/scop/pre-commit-shfmt",
	"stylua":     "https://github.com/JohnnyMorganz/StyLua",
	"uv":         "https://github.com/astral-sh/uv-pre-commit",
}

// modulePinnedBinaries maps a tool generated CI downloads to the Go module its
// pre-commit hook installs. The hook takes the tools pin for that module, so CI
// takes the same release from it rather than from a binaries entry that could
// drift from the hook.
var modulePinnedBinaries = map[string]string{
	"tflint":         "github.com/terraform-linters/tflint",
	"terraform_docs": "github.com/terraform-docs/terraform-docs",
}

// hookRevisionSuffix is the counter a wrapper repo appends when it re-tags one
// upstream release: pre-commit-shfmt's v3.13.1-1 wraps shfmt 3.13.1.
var hookRevisionSuffix = regexp.MustCompile(`-\d+$`)

// Toolchain is the manifest of pinned tool versions shared by every generated
// config. Blocks carry Pin where a version goes, so a version is declared in
// exactly one place.
type Toolchain struct {
	Version int    `yaml:"version"`
	Hooks   []Hook `yaml:"hooks"`
	// Actions pins GitHub Actions used by generated CI workflows, so an action
	// version is declared in the same place as a pre-commit hook version.
	Actions []Action `yaml:"actions"`
	// Tools pins Go modules installed as CLIs. Generated CI installs each with
	// `go install`, except a tool in modulePinnedBinaries, whose release binary
	// it downloads at this version. A `language: golang` hook installs the
	// same version from its additional_dependencies.
	Tools []Tool `yaml:"tools"`
	// Runtimes pins language runtimes generated CI sets up.
	Runtimes []Runtime `yaml:"runtimes"`
	// Binaries pins tools generated CI downloads from a release, for tools with
	// no module ecosystem to install from. Without this they would resolve to
	// whatever the runner image happens to ship, which is the floating-version
	// problem the rest of this manifest exists to prevent.
	Binaries []Binary `yaml:"binaries"`
	// HostedRunner is the GitHub-hosted image a public repo's workflows run on,
	// named by the label for one image release. `ubuntu-latest` moves to a new
	// release on GitHub's schedule, so a job that passed the day before can
	// fail with nothing in the repo changed.
	HostedRunner string `yaml:"hosted_runner"`
	// Languages holds each language's floor and, where the language separates
	// them, the toolchain its builds use. Populated only by LoadFile — the
	// embedded YAML predates the declaration and carries runtimes alone, so a
	// consumer must treat an absent entry as "not declared" rather than as zero.
	Languages map[string]Language `yaml:"-"`
}

// Binary is a released executable pinned to a version.
type Binary struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Runtime is a language runtime pinned to a version.
type Runtime struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Tool is a Go module installed as a CLI, pinned to a version.
type Tool struct {
	Module  string `yaml:"module"`
	Version string `yaml:"version"`
}

// Hook is a pre-commit repo pinned to a rev.
type Hook struct {
	Repo string `yaml:"repo"`
	Rev  string `yaml:"rev"`
}

// Action is a GitHub Action pinned to a version ref.
type Action struct {
	Uses    string `yaml:"uses"`
	Version string `yaml:"version"`
}

// Load reads a manifest in the YAML shape the test fixture uses. Every command
// reads the versions file through LoadFile instead.
func Load(assetsFS fs.FS) (*Toolchain, error) {
	data, err := fs.ReadFile(assetsFS, File)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", File, err)
	}

	var manifest Toolchain
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", File, err)
	}
	if manifest.Version < 1 {
		return nil, fmt.Errorf("%s: version must be >= 1, got %d", File, manifest.Version)
	}
	if err := manifest.refuseDerivedBinaries(); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	if err := manifest.refuseFloatingRunner(); err != nil {
		return nil, fmt.Errorf("%s: hosted_runner: %w", File, err)
	}
	return &manifest, nil
}

// refuseFloatingRunner rejects a hosted runner that is not one image release.
// Empty would write a bare runs-on, and `ubuntu-latest` is the floating label
// the pin exists to replace.
func (t *Toolchain) refuseFloatingRunner() error {
	if !pinnedImageRE.MatchString(t.HostedRunner) {
		return fmt.Errorf("must name one Ubuntu image release such as ubuntu-26.04, got %q", t.HostedRunner)
	}
	return nil
}

// refuseDerivedBinaries rejects a binaries entry for a tool whose CI version
// comes from its hook's pin. The entry would be a second copy of that version,
// and ApplyBinaryVersions would never read it.
func (t *Toolchain) refuseDerivedBinaries() error {
	for _, binary := range t.Binaries {
		if repo, derived := hookPinnedTools[binary.Name]; derived {
			return fmt.Errorf("binaries pins %s, whose CI version is the release its hook pin %s wraps — remove the binaries entry", binary.Name, repo)
		}
		if module, derived := modulePinnedBinaries[binary.Name]; derived {
			return fmt.Errorf("binaries pins %s, whose CI version is the tools pin for %s that its hook installs — remove the binaries entry", binary.Name, module)
		}
	}
	return nil
}

// Unpinned names each Pin that survived rendering, as the repo for a hook's rev
// and as the trimmed line otherwise. Non-empty means the versions file does not
// pin something a block uses.
func Unpinned(content string) []string {
	var missing []string
	repo := ""
	for _, line := range strings.Split(content, "\n") {
		if m := repoLineRE.FindStringSubmatch(line); m != nil {
			repo = m[2]
		}
		if !strings.Contains(line, Pin) {
			continue
		}
		if revLineRE.MatchString(line) && repo != "" {
			missing = append(missing, repo)
			continue
		}
		missing = append(missing, strings.TrimSpace(line))
	}
	return missing
}

// RevFor returns the pinned rev for a repo URL, and whether it is managed.
func (t *Toolchain) RevFor(repo string) (string, bool) {
	for _, hook := range t.Hooks {
		if hook.Repo == repo {
			return hook.Rev, true
		}
	}
	return "", false
}

// ApplyRevs rewrites each `rev:` line in a block to the manifest's pinned
// version for the repo it belongs to. A repo the manifest does not manage is
// left alone, so a block's Pin survives for Unpinned to report.
func (t *Toolchain) ApplyRevs(content string) string {
	lines := strings.Split(content, "\n")
	currentRepo := ""

	for i, line := range lines {
		if m := repoLineRE.FindStringSubmatch(line); m != nil {
			currentRepo = m[2]
			continue
		}
		m := revLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if rev, managed := t.RevFor(currentRepo); managed {
			lines[i] = m[1] + rev
		}
	}
	return strings.Join(lines, "\n")
}

// ActionVersion returns the pinned version ref for an action, and whether it is managed.
func (t *Toolchain) ActionVersion(uses string) (string, bool) {
	for _, action := range t.Actions {
		if action.Uses == uses {
			return action.Version, true
		}
	}
	return "", false
}

// ApplyActionVersions rewrites each `uses: owner/action@ref` to the manifest's
// pinned version. A local workflow reference (`uses: ./...`) and any action the
// manifest does not pin are left alone, and so is an action pinned to a commit:
// replacing that with a tag would loosen the pin.
func (t *Toolchain) ApplyActionVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := usesLineRE.FindStringSubmatch(line)
		if len(m) < 4 || commitRefRE.MatchString(line) {
			continue
		}
		if version, managed := t.ActionVersion(m[2]); managed {
			lines[i] = m[1] + m[2] + "@" + version + m[3]
		}
	}
	return strings.Join(lines, "\n")
}

// ToolVersion returns the pinned version for a Go module, and whether it is managed.
func (t *Toolchain) ToolVersion(module string) (string, bool) {
	for _, tool := range t.Tools {
		if tool.Module == module {
			return tool.Version, true
		}
	}
	return "", false
}

// ApplyToolVersions rewrites each `go install <module>@<ref>` to the manifest's
// pinned version. A module the manifest does not pin is left alone.
func (t *Toolchain) ApplyToolVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := goInstallRE.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		if version, managed := t.ToolVersion(m[2]); managed {
			lines[i] = m[1] + m[2] + "@" + version + m[3]
		}
	}
	return strings.Join(lines, "\n")
}

// RuntimeVersion returns the pinned version for a runtime, and whether it is managed.
func (t *Toolchain) RuntimeVersion(name string) (string, bool) {
	for _, runtime := range t.Runtimes {
		if runtime.Name == name {
			return runtime.Version, true
		}
	}
	return "", false
}

// ApplyDependencyVersions rewrites each `- <module>@<ref>` list item to the
// tools pin for that module, which is how a `language: golang` hook installs
// the release CI does. A module the manifest does not pin is left alone.
func (t *Toolchain) ApplyDependencyVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := dependencyLineRE.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		if version, managed := t.ToolVersion(m[2]); managed {
			lines[i] = m[1] + m[2] + "@" + version
		}
	}
	return strings.Join(lines, "\n")
}

// ApplyPreCommitPins runs every substitution a pre-commit config takes: hook
// revs, and the modules a local Go hook installs.
func (t *Toolchain) ApplyPreCommitPins(content string) string {
	return t.ApplyDependencyVersions(t.ApplyRevs(content))
}

// ApplyRuntimeVersions rewrites `<name>-version: X` inputs to the manifest's
// pinned version. `<name>-version-file:` does not match — that points at a file
// in the repo, which is the repo's business, not the manifest's.
func (t *Toolchain) ApplyRuntimeVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := runtimeLineRE.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}
		if version, managed := t.RuntimeVersion(m[2]); managed {
			lines[i] = fmt.Sprintf("%s%s-version: %q", m[1], m[2], version)
		}
	}
	return strings.Join(lines, "\n")
}

// ApplyUvxVersions rewrites `uvx <tool>@<version>` to the release that tool's
// pre-commit hook pins. A tool with no hook in hookPinnedTools is left alone.
//
// CI runs these through uvx rather than `uv run` because `uv run ruff` resolves
// ruff from the repo's own dependencies, and a repo that treats ruff as a fleet
// tool rather than a project dependency does not declare it — CI then failed to
// spawn the binary instead of linting.
func (t *Toolchain) ApplyUvxVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := uvxLineRE.FindStringSubmatch(line)
		if len(m) < 5 {
			continue
		}
		if version, derived := t.HookPinnedVersion(m[2]); derived {
			lines[i] = m[1] + m[2] + "@" + version + m[4]
		}
	}
	return strings.Join(lines, "\n")
}

// ApplyWorkflowPins rewrites the pins a workflow forge did not write shares
// with the one it did: actions, `go install` tools, uvx tools, binaries and the
// hosted runner image. A runtime version is left alone, because a hand-written
// matrix may test several on purpose.
func (t *Toolchain) ApplyWorkflowPins(content string) string {
	return t.ApplyRunnerLabels(t.ApplyUvxVersions(t.ApplyBinaryVersions(t.ApplyToolVersions(t.ApplyActionVersions(content)))))
}

var (
	// pinnedImageRE is a label naming one Ubuntu image release.
	pinnedImageRE = regexp.MustCompile(`^ubuntu-[0-9]{2}\.[0-9]{2}$`)
	// labelTokenRE is a maximal run of the characters a runner label is made
	// of, so `ubuntu-24.04-arm` and `my-ubuntu-latest` arrive whole and are
	// never mistaken for the label inside them.
	labelTokenRE = regexp.MustCompile(`[A-Za-z0-9._-]+`)
	// listItemRE is a YAML block-list item, capturing its indent.
	listItemRE = regexp.MustCompile(`^( *)-(\s|$)`)
)

// ApplyRunnerLabels rewrites every general-purpose Ubuntu label in a workflow,
// `ubuntu-latest` or a release such as `ubuntu-24.04`, to the declared hosted
// image. That covers runs-on values and OS matrices alike.
//
// Left alone: a variant such as `ubuntu-24.04-arm`, which names a different
// machine; a label right after `:` or `/`, which is an image tag such as
// `base:ubuntu-22.04`; a comment line, which is prose about a runner; and a
// list naming two distinct labels, which tests several releases on purpose
// and would collapse into one release named twice.
func (t *Toolchain) ApplyRunnerLabels(content string) string {
	if t.HostedRunner == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	held := multiReleaseLists(lines)
	for i, line := range lines {
		if held[i] {
			continue
		}
		spans := runnerLabelSpans(line)
		for j := len(spans) - 1; j >= 0; j-- {
			line = line[:spans[j][0]] + t.HostedRunner + line[spans[j][1]:]
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// runnerLabelSpans is where each label ApplyRunnerLabels may rewrite sits in
// one line, and none on a comment line.
func runnerLabelSpans(line string) [][]int {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return nil
	}
	var spans [][]int
	for _, loc := range labelTokenRE.FindAllStringIndex(line, -1) {
		if loc[0] > 0 && (line[loc[0]-1] == ':' || line[loc[0]-1] == '/') {
			continue
		}
		if token := line[loc[0]:loc[1]]; token == "ubuntu-latest" || pinnedImageRE.MatchString(token) {
			spans = append(spans, loc)
		}
	}
	return spans
}

// listItemIndent is the indent of a YAML block-list item, and whether the line
// is one.
func listItemIndent(line string) (int, bool) {
	match := listItemRE.FindStringSubmatch(line)
	if len(match) < 2 {
		return 0, false
	}
	return len(match[1]), true
}

// multiReleaseLists marks every line of a flow list or block list that names
// two or more distinct labels. A block list runs from an item to the last line
// indented under it or beside it at the same indent.
func multiReleaseLists(lines []string) map[int]bool {
	held := map[int]bool{}
	labelsIn := func(from, to int) map[string]bool {
		labels := map[string]bool{}
		for _, line := range lines[from:to] {
			for _, span := range runnerLabelSpans(line) {
				labels[line[span[0]:span[1]]] = true
			}
		}
		return labels
	}
	for i := range lines {
		if len(labelsIn(i, i+1)) > 1 {
			held[i] = true
		}
	}
	for i := 0; i < len(lines); {
		indent, isItem := listItemIndent(lines[i])
		if !isItem {
			i++
			continue
		}
		end := i + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			nextIndent, nextIsItem := listItemIndent(lines[end])
			deeper := len(lines[end])-len(strings.TrimLeft(lines[end], " ")) > indent
			if !deeper && (!nextIsItem || nextIndent != indent) {
				break
			}
			end++
		}
		if len(labelsIn(i, end)) > 1 {
			for j := i; j < end; j++ {
				held[j] = true
			}
		}
		i = end
	}
	return held
}

// ApplyAll runs every substitution a generated file may need.
func (t *Toolchain) ApplyAll(content string) string {
	return t.ApplyUvxVersions(t.ApplyBinaryVersions(t.ApplyRuntimeVersions(t.ApplyToolVersions(t.ApplyActionVersions(t.ApplyPreCommitPins(content))))))
}

// HookPinnedVersion is the upstream release a tool's hook rev wraps, and whether
// the tool's version comes from a hook at all.
func (t *Toolchain) HookPinnedVersion(tool string) (string, bool) {
	repo, derived := hookPinnedTools[tool]
	if !derived {
		return "", false
	}
	rev, managed := t.RevFor(repo)
	if !managed {
		return "", false
	}
	return hookRevisionSuffix.ReplaceAllString(strings.TrimPrefix(rev, "v"), ""), true
}

// BinaryVersion returns the pinned version for a released binary, and whether
// it is managed. A tool with a hook takes the release that hook pins, or the
// release of the module its hook installs.
func (t *Toolchain) BinaryVersion(name string) (string, bool) {
	if version, derived := t.HookPinnedVersion(name); derived {
		return version, true
	}
	if module, derived := modulePinnedBinaries[name]; derived {
		version, managed := t.ToolVersion(module)
		return strings.TrimPrefix(version, "v"), managed
	}
	for _, binary := range t.Binaries {
		if binary.Name == name {
			return binary.Version, true
		}
	}
	return "", false
}

// ApplyBinaryVersions rewrites `<name>_version="X"` shell assignments in a
// block to the manifest's pinned version. An unmanaged name is left alone, so a
// block can still use the same shape for a version the manifest does not own.
func (t *Toolchain) ApplyBinaryVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := binaryLineRE.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}
		if version, managed := t.BinaryVersion(m[2]); managed {
			lines[i] = fmt.Sprintf("%s%s_version=%q", m[1], m[2], version)
		}
	}
	return strings.Join(lines, "\n")
}

// sortRuntimes keeps the derived list stable, so two reads of one declaration
// generate identical configs.
func sortRuntimes(runtimes []Runtime) {
	sort.Slice(runtimes, func(i, j int) bool { return runtimes[i].Name < runtimes[j].Name })
}
