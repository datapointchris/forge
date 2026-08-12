package cliaudit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// snapshotDir is where saved surfaces live under forge's own data directory.
const snapshotDir = "cli-surface"

// SnapshotDir resolves forge's XDG data directory for saved surfaces. A generic
// tool must not carry a fleet-specific path, so this resolves its own the same
// way DefaultReposPath does.
func SnapshotDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "forge", snapshotDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "forge", snapshotDir), nil
}

// Snapshot is one reading of the fleet's command surfaces, kept so a later
// reading can be subtracted from it.
//
// Version is a plain incrementing integer rather than a hash or a timestamp,
// because it is what gets typed: `forge cli diff 3 4`. Taken carries the
// timestamp the version number deliberately does not.
type Snapshot struct {
	Version    int          `json:"version"`
	Taken      time.Time    `json:"taken"`
	Tools      []*Tool      `json:"tools"`
	Unresolved []Unresolved `json:"unresolved,omitempty"`

	// source is the path a surface was read from, when it was named by path
	// rather than by version. A file copied out of the data directory keeps the
	// version integer it was written with, so labeling it by that integer
	// prints `v1 -> v1` for a diff with real findings in it.
	source string
}

// Label is how a snapshot names itself in a diff header.
func (s *Snapshot) Label() string {
	switch {
	case s.source != "":
		return s.source
	case s.Version == 0:
		return "live"
	default:
		return fmt.Sprintf("v%d (%s)", s.Version, s.Taken.Format(time.DateOnly))
	}
}

// Save writes the surfaces as the next version and returns what it wrote.
func Save(dir string, tools []*Tool, unresolved []Unresolved, taken time.Time) (*Snapshot, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	versions, err := Versions(dir)
	if err != nil {
		return nil, err
	}
	next := 1
	if len(versions) > 0 {
		next = versions[len(versions)-1] + 1
	}

	snap := &Snapshot{Version: next, Taken: taken, Tools: tools, Unresolved: unresolved}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := os.WriteFile(snapshotPath(dir, next), append(data, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("write snapshot: %w", err)
	}
	return snap, nil
}

// Load reads one saved version, which labels itself by that version.
func Load(dir string, version int) (*Snapshot, error) {
	snap, err := read(snapshotPath(dir, version))
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// LoadFile reads a snapshot from an explicit path, which is how a surface kept
// outside the data directory — in a repo, in CI — is compared.
func LoadFile(path string) (*Snapshot, error) {
	snap, err := read(path)
	if err != nil {
		return nil, err
	}
	snap.source = path
	return snap, nil
}

func read(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	return &snap, nil
}

// Versions lists the saved versions in ascending order.
func Versions(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot directory: %w", err)
	}
	var out []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue // a file that is not a version is not this package's business
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}

func snapshotPath(dir string, version int) string {
	return filepath.Join(dir, strconv.Itoa(version)+".json")
}

// Entry is one addressable thing in a surface: a command, or a flag on one.
type Entry struct {
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
	Flag    string `json:"flag,omitempty"`
}

func (e Entry) String() string {
	s := e.Tool
	if e.Command != "" {
		s += " " + e.Command
	}
	if e.Flag != "" {
		s += " " + e.Flag
	}
	return s
}

// Diff is what changed between two surfaces.
//
// Tools present on only one side are reported as whole tools rather than as
// every command they carry. A tool that was not installed when a snapshot was
// taken would otherwise contribute its entire tree as a change, burying the
// single renamed flag the diff exists to surface.
type Diff struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	ToolsRemoved []string `json:"tools_removed,omitempty"`
	ToolsAdded   []string `json:"tools_added,omitempty"`
	Removed      []Entry  `json:"removed,omitempty"`
	Added        []Entry  `json:"added,omitempty"`
}

// Empty reports whether the two surfaces are the same.
func (d *Diff) Empty() bool {
	return len(d.ToolsRemoved) == 0 && len(d.ToolsAdded) == 0 &&
		len(d.Removed) == 0 && len(d.Added) == 0
}

// Compare subtracts one surface from another.
func Compare(from, to *Snapshot) *Diff {
	d := &Diff{From: from.Label(), To: to.Label()}

	fromTools := byBinary(from.Tools)
	toTools := byBinary(to.Tools)

	for name := range fromTools {
		if _, ok := toTools[name]; !ok {
			d.ToolsRemoved = append(d.ToolsRemoved, name)
		}
	}
	for name := range toTools {
		if _, ok := fromTools[name]; !ok {
			d.ToolsAdded = append(d.ToolsAdded, name)
		}
	}
	sort.Strings(d.ToolsRemoved)
	sort.Strings(d.ToolsAdded)

	shared := make([]string, 0, len(fromTools))
	for name := range fromTools {
		if _, ok := toTools[name]; ok {
			shared = append(shared, name)
		}
	}
	sort.Strings(shared)

	for _, name := range shared {
		before := entries(fromTools[name])
		after := entries(toTools[name])
		d.Removed = append(d.Removed, missing(before, after)...)
		d.Added = append(d.Added, missing(after, before)...)
	}
	return d
}

func byBinary(tools []*Tool) map[string]*Tool {
	out := make(map[string]*Tool, len(tools))
	for _, t := range tools {
		if t != nil {
			out[t.Binary] = t
		}
	}
	return out
}

// entries flattens a tool into every command it presents and every flag on each,
// keyed so the two sides can be subtracted.
func entries(t *Tool) map[Entry]bool {
	out := map[Entry]bool{}
	if t == nil || t.Root == nil {
		return out
	}
	record := func(path []string, flags []string) {
		cmd := strings.Join(path, " ")
		if cmd != "" {
			out[Entry{Tool: t.Binary, Command: cmd}] = true
		}
		for _, f := range flags {
			out[Entry{Tool: t.Binary, Command: cmd, Flag: f}] = true
		}
	}
	record(nil, t.Root.Flags)
	t.Walk(func(n *Node) { record(n.Path, n.Flags) })
	return out
}

func missing(from, in map[Entry]bool) []Entry {
	var out []Entry
	for e := range from {
		if !in[e] {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// WriteDiff renders a diff for a human.
func WriteDiff(w io.Writer, d *Diff) error {
	if _, err := fmt.Fprintf(w, "%s -> %s\n", d.From, d.To); err != nil {
		return err
	}
	if d.Empty() {
		_, err := fmt.Fprintln(w, "\nno change")
		return err
	}
	sections := []struct {
		title string
		lines []string
	}{
		{"tools gone", d.ToolsRemoved},
		{"tools new", d.ToolsAdded},
		{"removed", strung(d.Removed)},
		{"added", strung(d.Added)},
	}
	for _, s := range sections {
		if len(s.lines) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n%s  %d\n", s.title, len(s.lines)); err != nil {
			return err
		}
		for _, line := range s.lines {
			if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
				return err
			}
		}
	}
	return nil
}

func strung(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.String())
	}
	return out
}
