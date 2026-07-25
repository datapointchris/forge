package precommit

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ToolchainFile is the manifest path relative to the pre-commit asset root.
const ToolchainFile = "toolchain.yml"

var (
	repoLineRE = regexp.MustCompile(`^(\s*-\s*repo:\s*)(\S+)\s*$`)
	revLineRE  = regexp.MustCompile(`^(\s*rev:\s*)(\S+)\s*$`)
)

// Toolchain is the manifest of pinned tool versions shared by every generated
// config. Blocks carry their own revs for readability; this overrides them so
// a version is declared in exactly one place.
type Toolchain struct {
	Version int `yaml:"version"`
	Hooks   []struct {
		Repo string `yaml:"repo"`
		Rev  string `yaml:"rev"`
	} `yaml:"hooks"`
}

// LoadToolchain reads the manifest from the pre-commit asset root.
func LoadToolchain(assetsFS fs.FS) (*Toolchain, error) {
	data, err := fs.ReadFile(assetsFS, ToolchainFile)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ToolchainFile, err)
	}

	var toolchain Toolchain
	if err := yaml.Unmarshal(data, &toolchain); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ToolchainFile, err)
	}
	if toolchain.Version < 1 {
		return nil, fmt.Errorf("%s: version must be >= 1, got %d", ToolchainFile, toolchain.Version)
	}
	return &toolchain, nil
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
