package toolchain

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the manifest path relative to the asset root.
const File = "toolchain.yml"

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
)

// uvxHookRepos maps a tool a CI block runs as `uvx <tool>@<version>` to the
// pre-commit repo pinning it. The version is derived from that hook's rev
// rather than declared a second time, so CI and the local hook cannot disagree
// about a finding — a second entry could drift, a derived one cannot.
var uvxHookRepos = map[string]string{
	"ruff": "https://github.com/astral-sh/ruff-pre-commit",
}

// Toolchain is the manifest of pinned tool versions shared by every generated
// config. Blocks carry their own revs for readability; this overrides them so
// a version is declared in exactly one place.
type Toolchain struct {
	Version int    `yaml:"version"`
	Hooks   []Hook `yaml:"hooks"`
	// Actions pins GitHub Actions used by generated CI workflows, so an action
	// version is declared in the same place as a pre-commit hook version.
	Actions []Action `yaml:"actions"`
	// Tools pins CLIs that generated CI installs with `go install`.
	Tools []Tool `yaml:"tools"`
	// Runtimes pins language runtimes generated CI sets up.
	Runtimes []Runtime `yaml:"runtimes"`
	// Binaries pins tools generated CI downloads from a release, for tools with
	// no module ecosystem to install from. Without this they would resolve to
	// whatever the runner image happens to ship, which is the floating-version
	// problem the rest of this manifest exists to prevent.
	Binaries []Binary `yaml:"binaries"`
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

// Load reads the manifest from the asset root.
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
	return &manifest, nil
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
// version for the repo it belongs to. A repo the manifest does not manage — a
// `repo: local` block, or one added without a manifest entry — is left alone;
// UnmanagedRepos is what reports that case as an error.
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

// UnmanagedRepos returns remote repo URLs used by blocks that the manifest does
// not pin. Non-empty means a block would ship a version nothing tracks.
func (t *Toolchain) UnmanagedRepos(blocksFS fs.FS) ([]string, error) {
	var unmanaged []string
	seen := make(map[string]bool)

	err := fs.WalkDir(blocksFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, err := fs.ReadFile(blocksFS, path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			m := repoLineRE.FindStringSubmatch(line)
			if len(m) < 3 {
				continue
			}
			if m[2] == "local" {
				continue
			}
			if _, managed := t.RevFor(m[2]); !managed && !seen[m[2]] {
				seen[m[2]] = true
				unmanaged = append(unmanaged, m[2])
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return unmanaged, nil
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
// manifest does not pin are left alone.
func (t *Toolchain) ApplyActionVersions(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		m := usesLineRE.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		if version, managed := t.ActionVersion(m[2]); managed {
			lines[i] = m[1] + m[2] + "@" + version + m[3]
		}
	}
	return strings.Join(lines, "\n")
}

// UnmanagedActions returns actions used by CI blocks that the manifest does not
// pin. Non-empty means a block would ship a version nothing tracks.
func (t *Toolchain) UnmanagedActions(blocksFS fs.FS) ([]string, error) {
	var unmanaged []string
	seen := make(map[string]bool)

	err := fs.WalkDir(blocksFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, err := fs.ReadFile(blocksFS, path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			m := usesLineRE.FindStringSubmatch(line)
			if len(m) < 4 {
				continue
			}
			if _, managed := t.ActionVersion(m[2]); !managed && !seen[m[2]] {
				seen[m[2]] = true
				unmanaged = append(unmanaged, m[2])
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return unmanaged, nil
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

// ApplyUvxVersions rewrites `uvx <tool>@<version>` to the version pinned for
// that tool's pre-commit hook. A tool with no mapping is left alone.
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
		repo, mapped := uvxHookRepos[m[2]]
		if !mapped {
			continue
		}
		if rev, managed := t.RevFor(repo); managed {
			lines[i] = m[1] + m[2] + "@" + strings.TrimPrefix(rev, "v") + m[4]
		}
	}
	return strings.Join(lines, "\n")
}

// ApplyAll runs every substitution a generated file may need.
func (t *Toolchain) ApplyAll(content string) string {
	return t.ApplyUvxVersions(t.ApplyBinaryVersions(t.ApplyRuntimeVersions(t.ApplyToolVersions(t.ApplyActionVersions(t.ApplyRevs(content))))))
}

// BinaryVersion returns the pinned version for a released binary, and whether
// it is managed.
func (t *Toolchain) BinaryVersion(name string) (string, bool) {
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
