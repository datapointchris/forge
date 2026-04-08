package dies

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Die struct {
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
	Registered  bool     // true if present in registry.yml
}

type registryFile struct {
	Dies map[string]Die `yaml:"dies"`
}

type Registry struct {
	Dies map[string]Die // key is relative path within dies_dir (e.g. "maintenance/fix.sh")
}

// LoadRegistry scans an fs.FS for die scripts and merges optional registry.yml metadata.
// Use os.DirFS(path) for filesystem directories or an embedded fs.FS.
func LoadRegistry(fsys fs.FS) (*Registry, error) {
	reg := &Registry{Dies: make(map[string]Die)}

	if err := reg.scan(fsys); err != nil {
		return nil, fmt.Errorf("scanning dies directory: %w", err)
	}

	if err := reg.mergeMetadata(fsys); err != nil {
		return nil, err
	}

	return reg, nil
}

func (r *Registry) scan(fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if name == "registry.yml" || name == "registry.yaml" {
			return nil
		}

		// fs.WalkDir with root "." produces paths like "maintenance/fix.sh"
		r.Dies[path] = Die{}
		return nil
	})
}

func (r *Registry) mergeMetadata(fsys fs.FS) error {
	data, err := fs.ReadFile(fsys, "registry.yml")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading registry: %w", err)
	}

	var rf registryFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return fmt.Errorf("parsing registry: %w", err)
	}

	for name, meta := range rf.Dies {
		existing, onDisk := r.Dies[name]
		if onDisk {
			existing.Description = meta.Description
			existing.Tags = meta.Tags
			existing.Registered = true
			r.Dies[name] = existing
		}
	}

	return nil
}

// Resolve validates the die exists and returns its absolute path within diesDir.
// For embedded mode, use the assets.Manager to extract instead.
func (r *Registry) Resolve(diesDir, diePath string) (string, error) {
	if _, ok := r.Dies[diePath]; !ok {
		return "", fmt.Errorf("die not found: %s", diePath)
	}
	return filepath.Join(diesDir, diePath), nil
}

func (r *Registry) Categories() []string {
	seen := make(map[string]bool)
	for name := range r.Dies {
		cat := filepath.Dir(name)
		if cat != "." {
			seen[cat] = true
		}
	}

	cats := make([]string, 0, len(seen))
	for c := range seen {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	return cats
}

func (r *Registry) ByCategory(filter string) map[string][]string {
	result := make(map[string][]string)
	for name := range r.Dies {
		cat := filepath.Dir(name)
		if cat == "." {
			cat = "uncategorized"
		}
		if filter != "" && cat != filter {
			continue
		}
		result[cat] = append(result[cat], name)
	}

	for cat := range result {
		sort.Strings(result[cat])
	}
	return result
}

func (r *Registry) Search(query string) []string {
	query = strings.ToLower(query)
	var matches []string

	for name, die := range r.Dies {
		var searchable strings.Builder
		searchable.WriteString(strings.ToLower(name))
		searchable.WriteString(" ")
		searchable.WriteString(strings.ToLower(die.Description))
		for _, tag := range die.Tags {
			searchable.WriteString(" ")
			searchable.WriteString(strings.ToLower(tag))
		}

		if strings.Contains(searchable.String(), query) {
			matches = append(matches, name)
		}
	}

	sort.Strings(matches)
	return matches
}

func (r *Registry) AllDiePaths() []string {
	paths := make([]string, 0, len(r.Dies))
	for name := range r.Dies {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}
