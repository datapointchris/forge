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
func PythonFloors(root string, components []config.Component, fallback string) map[string]string {
	floors := map[string]string{}
	for _, component := range components {
		if precommit.StackToCategory(component.Stack) != "python" {
			continue
		}
		floor := fallback
		if data, err := os.ReadFile(filepath.Join(root, component.Dir, "pyproject.toml")); err == nil {
			if match := requiresPythonFloorRE.FindSubmatch(data); match != nil {
				floor = string(match[1])
			}
		}
		if floor != "" {
			floors[component.Dir] = floor
		}
	}
	return floors
}

var requiresPythonFloorRE = regexp.MustCompile(`(?m)^requires-python\s*=\s*["'][^"'\n]*?(?:>=|~=|==)\s*(\d+\.\d+)`)

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
