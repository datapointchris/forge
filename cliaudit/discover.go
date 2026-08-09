package cliaudit

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/datapointchris/forge/v5/config"
)

// Candidate is a repo that ships a CLI, resolved to the binary on PATH.
type Candidate struct {
	Repo   string
	Binary string
	Path   string
}

// Unresolved is a repo that declares a command-line entry point which is not
// installed. Reported rather than dropped: a sweep that silently skips a tool
// reads as "the fleet is clean" when it means "we did not look".
//
// A repo with no declared entry point whose name is also not on PATH is not
// listed — that is a library, a config repo, or a webapp without a CLI, and
// there are far more of those than there are tools.
type Unresolved struct {
	Repo  string
	Tried []string
}

var (
	scriptsRe   = regexp.MustCompile(`(?ms)^\[project\.scripts\]\s*\n(.*?)(?:\n\[|\z)`)
	scriptKeyRe = regexp.MustCompile(`(?m)^\s*["']?([A-Za-z][\w.-]*)["']?\s*=`)
	goBinaryRe  = regexp.MustCompile(`(?m)^\s*(?:binary|project_name):\s*["']?([\w.-]+)["']?`)
)

// Discover resolves active repos to installed binaries.
//
// Binary names are derived from what each repo already declares — a
// pyproject.toml [project.scripts] key, a goreleaser binary: — falling back to
// the repo's own name. Nothing is added to the registry to make this work, so a
// repo that names its binary only inside a build script cannot be found here
// and comes back in Unresolved.
func Discover(repos []config.Repo) (found []Candidate, unresolved []Unresolved) {
	for _, r := range repos {
		if r.Owner != "" || (r.Status != "" && r.Status != "active") {
			continue
		}
		names, declared := candidateNames(r)
		var hit bool
		for _, n := range names {
			if p, err := exec.LookPath(n); err == nil {
				found = append(found, Candidate{Repo: r.Name, Binary: n, Path: p})
				hit = true
				break
			}
		}
		if !hit && declared {
			unresolved = append(unresolved, Unresolved{Repo: r.Name, Tried: names})
		}
	}
	return found, unresolved
}

// candidateNames is the repo's declared entry points, most specific first, with
// the repo name last as the common case. declared reports whether the repo
// actually says it ships a command, which is what separates a missing tool from
// a library that was never one.
func candidateNames(r config.Repo) (names []string, declared bool) {
	root := expand(r.Path)
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}

	for _, dir := range componentDirs(r) {
		for _, n := range pyprojectScripts(filepath.Join(root, dir, "pyproject.toml")) {
			add(n)
			declared = true
		}
		for _, f := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
			if b := goreleaserBinary(filepath.Join(root, dir, f)); b != "" {
				add(b)
				declared = true
			}
		}
	}
	add(r.Name)
	return names, declared
}

func componentDirs(r config.Repo) []string {
	dirs := []string{"."}
	if r.Toolchain == nil {
		return dirs
	}
	seen := map[string]bool{".": true}
	for _, c := range r.Toolchain.Components {
		if c.Dir != "" && !seen[c.Dir] {
			seen[c.Dir] = true
			dirs = append(dirs, c.Dir)
		}
	}
	return dirs
}

func pyprojectScripts(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	block := scriptsRe.FindSubmatch(data)
	if block == nil {
		return nil
	}
	var out []string
	for _, m := range scriptKeyRe.FindAllSubmatch(block[1], -1) {
		out = append(out, string(m[1]))
	}
	return out
}

func goreleaserBinary(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := goBinaryRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
