package cliaudit

import (
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/clisurface"
)

func node(path ...string) *clisurface.Node {
	return &clisurface.Node{Path: path, Name: path[len(path)-1]}
}

func tool(binary string, children ...*clisurface.Node) *clisurface.Tool {
	return &clisurface.Tool{
		Binary:    binary,
		Framework: clisurface.FrameworkCobra,
		Root:      &clisurface.Node{Name: binary, Children: children},
	}
}

func TestSelfUpdateIsNotAnEditSpelling(t *testing.T) {
	rep := Analyze([]*clisurface.Tool{tool("demo", node("update"), node("books", "edit"))}, nil)
	for _, u := range rep.Verbs["edit"] {
		if slices.Contains(u.Spelling, "update") {
			t.Error("root `update` counted as an edit verb; it is self-update, a different job")
		}
	}
}

func TestNestedUpdateIsAnEditSpelling(t *testing.T) {
	books := node("books")
	books.Children = []*clisurface.Node{{Path: []string{"books", "update"}, Name: "update"}}
	rep := Analyze([]*clisurface.Tool{tool("demo", books)}, nil)

	var found bool
	for _, u := range rep.Verbs["edit"] {
		if slices.Contains(u.Spelling, "update") {
			found = true
		}
	}
	if !found {
		t.Error("`books update` should count as an edit spelling")
	}
}

func TestCompoundIsReportedOnceNotAlsoAsABareNoun(t *testing.T) {
	sections := node("sections")
	sections.Children = []*clisurface.Node{{Path: []string{"sections", "add-items"}, Name: "add-items"}}
	rep := Analyze([]*clisurface.Tool{tool("demo", sections)}, nil)

	var compound, bare int
	for _, f := range rep.Findings {
		switch f.Kind {
		case "hyphen-compound":
			compound++
			if f.Detail != "sections items add" {
				t.Errorf("detail = %q, want the namespaced shape", f.Detail)
			}
		case "bare-noun":
			bare++
		}
	}
	if compound != 1 {
		t.Errorf("compound findings = %d, want 1", compound)
	}
	if bare != 0 {
		t.Errorf("bare-noun findings = %d, want 0 — add-items is already reported as a compound", bare)
	}
}

func TestQueryNamesAreNotBareNouns(t *testing.T) {
	rep := Analyze([]*clisurface.Tool{tool("demo", node("status"), node("stats"), node("repos"))}, nil)
	var flagged []string
	for _, f := range rep.Findings {
		if f.Kind == "bare-noun" {
			flagged = append(flagged, f.Command)
		}
	}
	if len(flagged) != 1 || flagged[0] != "repos" {
		t.Errorf("bare nouns = %v, want only [repos]; status and stats name a question, not a resource", flagged)
	}
}

func TestUnreadableToolIsReported(t *testing.T) {
	rep := Analyze([]*clisurface.Tool{{Binary: "quiet", Framework: clisurface.FrameworkRich, Root: &clisurface.Node{Name: "quiet"}}}, nil)
	var found bool
	for _, f := range rep.Findings {
		if f.Kind == "unreadable" {
			found = true
		}
	}
	if !found {
		t.Error("a tool whose help lists no commands should be reported, not silently counted as clean")
	}
}

func TestTextReportShowsTheDistribution(t *testing.T) {
	tools := []*clisurface.Tool{
		tool("a", node("books", "show")),
		tool("b", node("books", "show")),
		tool("c", node("books", "view")),
	}
	var sb strings.Builder
	if err := WriteText(&sb, Analyze(tools, nil)); err != nil {
		t.Fatal(err)
	}
	out := sb.String()

	if !strings.Contains(out, "show x2") {
		t.Errorf("majority spelling and count missing from:\n%s", out)
	}
	// A lone straggler is named, because which tool differs is the whole
	// signal; a spelling used by many is only counted.
	if !strings.Contains(out, "view x1 c") {
		t.Errorf("the single differing tool should be named:\n%s", out)
	}
}

func TestUnresolvedReposAreListed(t *testing.T) {
	var sb strings.Builder
	if err := WriteText(&sb, Analyze(nil, []Unresolved{{Repo: "widget", Tried: []string{"widget"}}})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "widget") {
		t.Error("a repo whose binary was not found must appear; a silent skip reads as a clean sweep")
	}
}
