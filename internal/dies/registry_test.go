package dies

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestDies(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "maintenance"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maintenance", "fix.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maintenance", "cleanup.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checks", "lint.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestLoadRegistry(t *testing.T) {
	t.Run("scans filesystem", func(t *testing.T) {
		dir := setupTestDies(t)

		reg, err := LoadRegistry(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reg.Dies) != 3 {
			t.Errorf("got %d dies, want 3", len(reg.Dies))
		}

		if _, ok := reg.Dies["maintenance/fix.sh"]; !ok {
			t.Error("missing maintenance/fix.sh")
		}
	})

	t.Run("merges registry metadata", func(t *testing.T) {
		dir := setupTestDies(t)
		registry := `dies:
  maintenance/fix.sh:
    description: "Fix broken things"
    tags: [repair, maintenance]
`
		if err := os.WriteFile(filepath.Join(dir, "registry.yml"), []byte(registry), 0o644); err != nil {
			t.Fatal(err)
		}

		reg, err := LoadRegistry(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		die := reg.Dies["maintenance/fix.sh"]
		if die.Description != "Fix broken things" {
			t.Errorf("description = %q, want %q", die.Description, "Fix broken things")
		}
		if !die.Registered {
			t.Error("expected Registered = true")
		}

		unregistered := reg.Dies["checks/lint.sh"]
		if unregistered.Registered {
			t.Error("expected Registered = false for unregistered die")
		}
	})

	t.Run("no registry file is ok", func(t *testing.T) {
		dir := setupTestDies(t)

		reg, err := LoadRegistry(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reg.Dies) != 3 {
			t.Errorf("got %d dies, want 3", len(reg.Dies))
		}
	})

	t.Run("skips registry.yml from scan", func(t *testing.T) {
		dir := setupTestDies(t)
		if err := os.WriteFile(filepath.Join(dir, "registry.yml"), []byte("dies: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		reg, err := LoadRegistry(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for name := range reg.Dies {
			if name == "registry.yml" {
				t.Error("registry.yml should not appear as a die")
			}
		}
	})
}

func TestResolve(t *testing.T) {
	dir := setupTestDies(t)
	reg, _ := LoadRegistry(dir)

	t.Run("valid die", func(t *testing.T) {
		got, err := reg.Resolve(dir, "maintenance/fix.sh")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(dir, "maintenance", "fix.sh")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("nonexistent die", func(t *testing.T) {
		_, err := reg.Resolve(dir, "nope.sh")
		if err == nil {
			t.Fatal("expected error for nonexistent die")
		}
	})
}

func TestCategories(t *testing.T) {
	dir := setupTestDies(t)
	reg, _ := LoadRegistry(dir)

	cats := reg.Categories()
	if len(cats) != 2 {
		t.Fatalf("got %d categories, want 2", len(cats))
	}
	if cats[0] != "checks" || cats[1] != "maintenance" {
		t.Errorf("categories = %v, want [checks, maintenance]", cats)
	}
}

func TestByCategory(t *testing.T) {
	dir := setupTestDies(t)
	reg, _ := LoadRegistry(dir)

	t.Run("all categories", func(t *testing.T) {
		grouped := reg.ByCategory("")
		if len(grouped) != 2 {
			t.Errorf("got %d categories, want 2", len(grouped))
		}
		if len(grouped["maintenance"]) != 2 {
			t.Errorf("maintenance has %d dies, want 2", len(grouped["maintenance"]))
		}
	})

	t.Run("filter to one", func(t *testing.T) {
		grouped := reg.ByCategory("checks")
		if len(grouped) != 1 {
			t.Errorf("got %d categories, want 1", len(grouped))
		}
	})
}

func TestSearch(t *testing.T) {
	dir := setupTestDies(t)
	registry := `dies:
  maintenance/fix.sh:
    description: "Fix broken gitignore entries"
    tags: [gitignore, repair]
`
	if err := os.WriteFile(filepath.Join(dir, "registry.yml"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, _ := LoadRegistry(dir)

	t.Run("match by name", func(t *testing.T) {
		matches := reg.Search("lint")
		if len(matches) != 1 || matches[0] != "checks/lint.sh" {
			t.Errorf("got %v, want [checks/lint.sh]", matches)
		}
	})

	t.Run("match by description", func(t *testing.T) {
		matches := reg.Search("gitignore")
		if len(matches) != 1 || matches[0] != "maintenance/fix.sh" {
			t.Errorf("got %v, want [maintenance/fix.sh]", matches)
		}
	})

	t.Run("match by tag", func(t *testing.T) {
		matches := reg.Search("repair")
		if len(matches) != 1 || matches[0] != "maintenance/fix.sh" {
			t.Errorf("got %v, want [maintenance/fix.sh]", matches)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		matches := reg.Search("FIX")
		if len(matches) != 1 {
			t.Errorf("got %d matches, want 1", len(matches))
		}
	})

	t.Run("no match", func(t *testing.T) {
		matches := reg.Search("nonexistent")
		if len(matches) != 0 {
			t.Errorf("got %d matches, want 0", len(matches))
		}
	})
}
