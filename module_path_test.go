package main

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	modulePathMajor = regexp.MustCompile(`^module .*/v(\d+)$`)
	releaseTag      = regexp.MustCompile(`^v(\d+)\.\d+\.\d+$`)
)

// moduleMajor reports the major a go.mod declares. No /vN suffix means v0 or
// v1, neither of which carries the major in the path.
func moduleMajor(goMod string) int {
	for _, line := range strings.Split(goMod, "\n") {
		if m := modulePathMajor.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			major, _ := strconv.Atoi(m[1])
			return major
		}
	}
	return 1
}

// latestReleaseMajor reports the highest major among plain vN.M.P tags, given
// them in descending version order. Returns 0 when the repo has none.
// Prereleases and pseudo-tags are ignored: only a published release is a claim
// about what the proxy will serve.
func latestReleaseMajor(tags []string) int {
	for _, tag := range tags {
		if m := releaseTag.FindStringSubmatch(tag); m != nil {
			major, _ := strconv.Atoi(m[1])
			return major
		}
	}
	return 0
}

func TestModuleMajor(t *testing.T) {
	cases := map[string]int{
		"module github.com/datapointchris/forge/v5": 5,
		"module github.com/datapointchris/forge":    1,
		"module github.com/datapointchris/forge/v2": 2,
	}
	for goMod, want := range cases {
		if got := moduleMajor("// header\n" + goMod + "\n\ngo 1.26\n"); got != want {
			t.Errorf("moduleMajor(%q) = %d, want %d", goMod, got, want)
		}
	}
}

func TestLatestReleaseMajor(t *testing.T) {
	if got := latestReleaseMajor([]string{"v5.0.0", "v4.2.0"}); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
	// A prerelease is not something the proxy serves as the major.
	if got := latestReleaseMajor([]string{"v6.0.0-rc.1", "v5.0.0"}); got != 5 {
		t.Errorf("got %d, want 5 — a prerelease must not count", got)
	}
	if got := latestReleaseMajor(nil); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// The failure this exists to catch, stated as a case: a v5 tag against a /v4
// go.mod is exactly what shipped four times.
func TestMismatchIsDetected(t *testing.T) {
	declared := moduleMajor("module github.com/datapointchris/forge/v4\n")
	released := latestReleaseMajor([]string{"v5.0.0"})
	if declared == released {
		t.Fatal("a v5 tag against a /v4 module path must not compare equal")
	}
}

// A major release tags vN.0.0, but nothing bumps go.mod's module path to match.
// The proxy then refuses to serve the new major under the old path, so
// `go install .../vN-1@latest` keeps resolving to the previous major and every
// machine silently installs a stale binary.
//
// It fires on the first commit after a bad release rather than during it,
// because the tag does not exist until the release job has already run.
func TestModulePathMatchesLatestReleaseTag(t *testing.T) {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("reading go.mod: %s", err)
	}

	out, err := exec.Command("git", "tag", "--sort=-v:refname").Output()
	if err != nil {
		t.Skipf("no git tags available: %s", err)
	}

	released := latestReleaseMajor(strings.Fields(string(out)))
	if released == 0 {
		t.Skip("no release tags yet")
	}

	if declared := moduleMajor(string(goMod)); declared != released {
		t.Errorf("go.mod declares major v%d but the latest release tag is v%d.x — "+
			"bump the module path and every import, or the proxy will not serve v%d",
			declared, released, released)
	}
}
