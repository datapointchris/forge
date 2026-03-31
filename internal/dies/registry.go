package dies

import (
	"fmt"
	"os"
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

func LoadRegistry(diesDir string) (*Registry, error) {
	reg := &Registry{Dies: make(map[string]Die)}

	if err := reg.scan(diesDir); err != nil {
		return nil, fmt.Errorf("scanning dies directory: %w", err)
	}

	registryPath := filepath.Join(diesDir, "registry.yml")
	if err := reg.mergeMetadata(registryPath); err != nil {
		return nil, err
	}

	return reg, nil
}

func (r *Registry) scan(diesDir string) error {
	return filepath.WalkDir(diesDir, func(path string, d os.DirEntry, err error) error {
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

		rel, err := filepath.Rel(diesDir, path)
		if err != nil {
			return err
		}

		r.Dies[rel] = Die{}
		return nil
	})
}

func (r *Registry) mergeMetadata(registryPath string) error {
	data, err := os.ReadFile(registryPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading registry %s: %w", registryPath, err)
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
