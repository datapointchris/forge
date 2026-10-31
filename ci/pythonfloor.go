package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/precommit"
)

// PythonFloors maps each python component's directory to the lowest Python
// its requires-python admits, as "3.13". A component whose pyproject.toml
// declares no lower bound takes fallback, the manifest's Python runtime.
//
// uv otherwise picks the first installed Python that satisfies the bound, and
// downloads the newest release where none does. A runner image's Python can
// sit below the floor, and the newest release can lack wheels the lock needs.
// pydantic-core had none for CPython 3.15, and its source build needs a
// compiler the image lacks. Pinned to the floor, CI also runs the oldest
// Python the project claims to support.
//
// The components of one uv workspace share the newest floor among them and
// the workspace root. uv resolves a workspace once for every member, on the
// intersection of their requires-python, so `uv sync` in a member whose own
// floor is lower refuses that Python.
func PythonFloors(root string, components []config.Component, fallback string) map[string]string {
	floors := map[string]string{}
	for _, component := range components {
		if precommit.StackToCategory(component.Stack) != "python" {
			continue
		}
		if floor := declaredPythonFloor(filepath.Join(root, component.Dir), fallback); floor != "" {
			floors[component.Dir] = floor
		}
	}

	members := map[string][]string{}
	for dir := range floors {
		if workspace, ok := uvWorkspaceRoot(root, dir); ok {
			members[workspace] = append(members[workspace], dir)
		}
	}
	for workspace, dirs := range members {
		shared := declaredPythonFloor(filepath.Join(root, workspace), "")
		for _, dir := range dirs {
			if shared == "" || olderPython(shared, floors[dir]) {
				shared = floors[dir]
			}
		}
		for _, dir := range dirs {
			floors[dir] = shared
		}
	}
	return floors
}

// declaredPythonFloor is the floor dir's pyproject.toml declares, or fallback.
func declaredPythonFloor(dir, fallback string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		if match := requiresPythonFloorRE.FindSubmatch(data); match != nil {
			return string(match[1])
		}
	}
	return fallback
}

// uvWorkspaceRoot is the nearest directory from dir up to root whose
// pyproject.toml declares a uv workspace, relative to root, as uv finds it.
func uvWorkspaceRoot(root, dir string) (string, bool) {
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		if data, err := os.ReadFile(filepath.Join(root, current, "pyproject.toml")); err == nil && uvWorkspaceRE.Match(data) {
			return current, true
		}
		if current == "." || current == string(filepath.Separator) {
			return "", false
		}
	}
}

var (
	requiresPythonFloorRE = regexp.MustCompile(`(?m)^requires-python\s*=\s*["'][^"'\n]*?(?:>=|~=|==)\s*(\d+\.\d+)`)
	uvWorkspaceRE         = regexp.MustCompile(`(?m)^\[tool\.uv\.workspace\]`)
)

// highestFloor is the newest of floors, which every python component admits.
func highestFloor(floors map[string]string) string {
	highest := ""
	for _, floor := range floors {
		if highest == "" || olderPython(highest, floor) {
			highest = floor
		}
	}
	return highest
}

func olderPython(a, b string) bool {
	aMajor, aMinor := splitPython(a)
	bMajor, bMinor := splitPython(b)
	return aMajor < bMajor || (aMajor == bMajor && aMinor < bMinor)
}

func splitPython(version string) (int, int) {
	majorText, minorText, _ := strings.Cut(version, ".")
	major, _ := strconv.Atoi(majorText)
	minor, _ := strconv.Atoi(minorText)
	return major, minor
}
