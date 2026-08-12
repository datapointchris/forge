package cliaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

func snap(version int, tools ...*Tool) *Snapshot {
	return &Snapshot{Version: version, Taken: testTime, Tools: tools}
}

func flagged(name string, flags ...string) *Node {
	n := node(name)
	n.Flags = flags
	return n
}

func hasEntry(entries []Entry, want string) bool {
	for _, e := range entries {
		if e.String() == want {
			return true
		}
	}
	return false
}

func TestRemovedCommandIsReported(t *testing.T) {
	before := snap(1, tool("demo", node("list"), node("prune")))
	after := snap(2, tool("demo", node("list")))

	d := Compare(before, after)

	if !hasEntry(d.Removed, "demo prune") {
		t.Errorf("`demo prune` should be reported removed, got %v", strung(d.Removed))
	}
	if len(d.Added) != 0 {
		t.Errorf("nothing was added, got %v", strung(d.Added))
	}
}

func TestAddedCommandIsReported(t *testing.T) {
	before := snap(1, tool("demo", node("list")))
	after := snap(2, tool("demo", node("list"), node("show")))

	d := Compare(before, after)

	if !hasEntry(d.Added, "demo show") {
		t.Errorf("`demo show` should be reported added, got %v", strung(d.Added))
	}
	if len(d.Removed) != 0 {
		t.Errorf("nothing was removed, got %v", strung(d.Removed))
	}
}

// A rename has no dedicated shape: it is the removal and the addition together,
// which is what makes it findable at all from the outside.
func TestRenameIsOneRemovalAndOneAddition(t *testing.T) {
	before := snap(1, tool("demo", node("view")))
	after := snap(2, tool("demo", node("show")))

	d := Compare(before, after)

	if !hasEntry(d.Removed, "demo view") || !hasEntry(d.Added, "demo show") {
		t.Errorf("a rename should be view removed and show added, got removed=%v added=%v",
			strung(d.Removed), strung(d.Added))
	}
}

func TestRemovedFlagIsReported(t *testing.T) {
	before := snap(1, tool("demo", flagged("list", "--json", "--all")))
	after := snap(2, tool("demo", flagged("list", "--json")))

	d := Compare(before, after)

	if !hasEntry(d.Removed, "demo list --all") {
		t.Errorf("`--all` should be reported removed from `demo list`, got %v", strung(d.Removed))
	}
	if hasEntry(d.Removed, "demo list") {
		t.Error("the command itself survived; only its flag went")
	}
}

// The load-bearing case. A tool that was simply not installed when one side was
// read would otherwise contribute its whole tree, burying the single change a
// diff is run to find.
func TestAToolMissingOnOneSideIsReportedWholeNotPerCommand(t *testing.T) {
	before := snap(1,
		tool("demo", node("list")),
		tool("gone", node("alpha"), node("beta"), node("gamma")),
	)
	after := snap(2, tool("demo", node("list")))

	d := Compare(before, after)

	if len(d.ToolsRemoved) != 1 || d.ToolsRemoved[0] != "gone" {
		t.Errorf("`gone` should be reported as a whole tool, got %v", d.ToolsRemoved)
	}
	for _, e := range d.Removed {
		if e.Tool == "gone" {
			t.Errorf("commands of an absent tool must not be listed individually, got %q", e.String())
		}
	}
}

func TestIdenticalSurfacesAreEmpty(t *testing.T) {
	tools := []*Tool{tool("demo", flagged("list", "--json"), node("show"))}
	d := Compare(snap(1, tools...), snap(2, tools...))

	if !d.Empty() {
		t.Errorf("identical surfaces should diff to nothing, got removed=%v added=%v tools=%v/%v",
			strung(d.Removed), strung(d.Added), d.ToolsRemoved, d.ToolsAdded)
	}
}

func TestSaveAssignsTheNextVersion(t *testing.T) {
	dir := t.TempDir()

	first, err := Save(dir, []*Tool{tool("demo")}, nil, testTime)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	second, err := Save(dir, []*Tool{tool("demo")}, nil, testTime)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if first.Version != 1 || second.Version != 2 {
		t.Errorf("versions should increment from 1, got %d then %d", first.Version, second.Version)
	}
}

func TestSavedSurfaceLoadsBackTheSame(t *testing.T) {
	dir := t.TempDir()
	tools := []*Tool{tool("demo", flagged("list", "--json"))}

	saved, err := Save(dir, tools, nil, testTime)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(dir, saved.Version)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !Compare(saved, loaded).Empty() {
		t.Error("a surface should survive the round trip through disk unchanged")
	}
	if !loaded.Taken.Equal(testTime) {
		t.Errorf("taken time should round trip, got %v", loaded.Taken)
	}
}

func TestVersionsIgnoresWhatIsNotAVersion(t *testing.T) {
	dir := t.TempDir()
	if _, err := Save(dir, []*Tool{tool("demo")}, nil, testTime); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, name := range []string{"notes.json", "3.txt", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	versions, err := Versions(dir)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) != 1 || versions[0] != 1 {
		t.Errorf("only the numbered file is a version, got %v", versions)
	}
}

func TestVersionsOnAnUnwrittenDirectoryIsEmptyNotAnError(t *testing.T) {
	versions, err := Versions(filepath.Join(t.TempDir(), "never-written"))
	if err != nil {
		t.Fatalf("a missing snapshot directory is the state before the first save, not a failure: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected no versions, got %v", versions)
	}
}

func TestLiveSurfaceLabelsItselfLive(t *testing.T) {
	d := Compare(snap(3, tool("demo")), &Snapshot{Tools: []*Tool{tool("demo")}})
	if d.From != "v3 (2026-08-12)" || d.To != "live" {
		t.Errorf("header should name both sides, got %q -> %q", d.From, d.To)
	}
}

// A file copied out of the data directory keeps the version it was written
// with, so labeling by that integer printed `v1 -> v1` for a diff that had
// real findings in it.
func TestASurfaceNamedByPathLabelsItselfByPath(t *testing.T) {
	dir := t.TempDir()
	if _, err := Save(dir, []*Tool{tool("demo")}, nil, testTime); err != nil {
		t.Fatalf("save: %v", err)
	}
	copied := filepath.Join(dir, "baseline-copy.json")
	original, err := os.ReadFile(filepath.Join(dir, "1.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(copied, original, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	byPath, err := LoadFile(copied)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	byVersion, err := Load(dir, 1)
	if err != nil {
		t.Fatalf("load version: %v", err)
	}

	if byPath.Label() != copied {
		t.Errorf("a surface read by path should label itself by that path, got %q", byPath.Label())
	}
	if byVersion.Label() == byPath.Label() {
		t.Error("two sides of a diff must not print the same label")
	}
}

func TestWriteDiffSaysNoChangeRatherThanPrintingNothing(t *testing.T) {
	var b strings.Builder
	tools := []*Tool{tool("demo", node("list"))}
	if err := WriteDiff(&b, Compare(snap(1, tools...), snap(2, tools...))); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !strings.Contains(b.String(), "no change") {
		t.Errorf("an empty diff must say so, or it reads as a crash; got %q", b.String())
	}
}

func TestWriteDiffListsEachChange(t *testing.T) {
	var b strings.Builder
	before := snap(1, tool("demo", node("prune")), tool("gone", node("alpha")))
	after := snap(2, tool("demo", node("show")))
	if err := WriteDiff(&b, Compare(before, after)); err != nil {
		t.Fatalf("write: %v", err)
	}

	out := b.String()
	for _, want := range []string{"demo prune", "demo show", "gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff output should name %q, got:\n%s", want, out)
		}
	}
}
