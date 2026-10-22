package dies

import (
	"slices"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

func components(pairs ...string) *config.Toolchain {
	declared := &config.Toolchain{}
	for i := 0; i < len(pairs); i += 2 {
		declared.Components = append(declared.Components, config.Component{Stack: pairs[i], Dir: pairs[i+1]})
	}
	return declared
}

// A multi-component root may hold any of its components, or none.
func TestLayoutAcceptsComponentsWhereverTheySit(t *testing.T) {
	cases := map[string]*config.Toolchain{
		"none at the root": components("go", "api", "go", "cli", "vue", "web", "docker", ".", "actions", "."),
		"one at the root":  components("python", ".", "go", "cli", "vue", "frontend", "docker", "."),
		"flat":             components("python", ".", "terraform", "infra", "shell", "."),
	}
	for name, declared := range cases {
		t.Run(name, func(t *testing.T) {
			if measured := reconcile.Assess(fixture(t, declared, nil), Layout{}); len(measured.Changes) != 0 {
				t.Errorf("changes = %v, want none", measured.Changes)
			}
		})
	}
}

// cmd/ as a package of its own is flat. A binary under it is a second layer.
func TestLayoutReportsNestedPackagesInAFlatGoTool(t *testing.T) {
	target := fixture(t, components("go", "."), map[string]string{
		"main.go":                "package main\n",
		"cmd/root.go":            "package cmd\n",
		"internal/store/s.go":    "package store\n",
		"cmd/migrate/main.go":    "package main\n",
		"pkg/client/client.go":   "package client\n",
		"api/internal/handle.go": "package internal\n",
	})

	measured := reconcile.Assess(target, Layout{})

	var items []string
	for _, change := range measured.Changes {
		items = append(items, change.Item)
	}
	slices.Sort(items)
	if want := []string{"cmd/migrate/main.go", "internal/", "pkg/"}; !slices.Equal(items, want) {
		t.Errorf("items = %v, want %v", items, want)
	}
}
