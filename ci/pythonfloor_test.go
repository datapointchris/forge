package ci

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// uv syncs a workspace member on the floor of the whole workspace, so a
// member's lower floor would refuse the Python it pins. A project outside the
// workspace keeps its own.
func TestPythonFloorsGivesAUvWorkspaceItsNewestFloor(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"packages/pyproject.toml":         "[tool.uv.workspace]\nmembers = [\"library\", \"cli\"]\n",
		"packages/library/pyproject.toml": "[project]\nrequires-python = \">=3.11\"\n",
		"packages/cli/pyproject.toml":     "[project]\nrequires-python = \">=3.13\"\n",
		"lambda/pyproject.toml":           "[project]\nrequires-python = \">=3.12\"\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	floors := PythonFloors(root, comps("python", "packages/library", "python", "packages/cli", "python", "lambda"), "3.14")
	want := map[string]string{"packages/library": "3.13", "packages/cli": "3.13", "lambda": "3.12"}
	if !maps.Equal(floors, want) {
		t.Errorf("floors = %v, want %v", floors, want)
	}
}
