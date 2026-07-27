package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fixtures mirror the real `icb ... --json` payloads (icb-cli's api structs), so
// these tests guard the parsing/filtering the live icb path can't be run headless.

func TestFilterComputerTodos(t *testing.T) {
	// Shape and values taken from a real `icb tasks todo --json` response.
	const raw = `[
	  {"id": 422, "name": "Trim Dingo Nails", "notes": "", "category": "Dingo", "priority": 1},
	  {"id": 476, "name": "Dev Blog", "notes": "Handle variations", "category": "Computer", "priority": 5},
	  {"id": 474, "name": "Journal", "notes": null, "category": "Personal", "priority": 3},
	  {"id": 373, "name": "Add absolute timestamp to git graph", "notes": null, "category": "Computer", "priority": 40},
	  {"id": 468, "name": "menu cli is not that useful", "notes": "surfaces tools", "category": "Computer", "priority": 36}
	]`

	var tasks []icbTask
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		t.Fatalf("unmarshal tasks: %v", err)
	}

	got := filterComputerTodos(tasks)
	if len(got) != 3 {
		t.Fatalf("expected 3 Computer todos, got %d", len(got))
	}
	for _, task := range got {
		if task.Category != computerCategory {
			t.Errorf("non-Computer task leaked through: %+v", task)
		}
	}
	// Priority order: 5 (Dev Blog), 36 (menu cli), 40 (git graph).
	wantOrder := []int{476, 468, 373}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("position %d: want id %d, got %d", i, id, got[i].ID)
		}
	}
}

func TestFilterComputerTodosEmpty(t *testing.T) {
	if got := filterComputerTodos(nil); got != nil {
		t.Errorf("expected nil for no tasks, got %+v", got)
	}
	only := []icbTask{{ID: 1, Category: "Home"}, {ID: 2, Category: "Purchase"}}
	if got := filterComputerTodos(only); len(got) != 0 {
		t.Errorf("expected no Computer todos, got %+v", got)
	}
}

func TestOpenItemsInOrder(t *testing.T) {
	// `icb projects view <id> --json` returns {..., "items": [...]} with per-item
	// position; completed/archived items must be dropped and the rest ordered.
	const raw = `{
	  "id": "proj-1",
	  "name": "Personal OS unification",
	  "items": [
	    {"id": "c", "title": "third", "completed": false, "archived": false, "position": 3},
	    {"id": "a", "title": "first", "completed": false, "archived": false, "position": 1},
	    {"id": "done", "title": "shipped", "completed": true, "archived": false, "position": 2},
	    {"id": "arch", "title": "dropped", "completed": false, "archived": true, "position": 0},
	    {"id": "b", "title": "second", "completed": false, "archived": false, "position": 2}
	  ]
	}`

	var view icbProjectView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("unmarshal project view: %v", err)
	}

	got := openItemsInOrder(view.Items)
	wantTitles := []string{"first", "second", "third"}
	if len(got) != len(wantTitles) {
		t.Fatalf("expected %d open items, got %d", len(wantTitles), len(got))
	}
	for i, title := range wantTitles {
		if got[i].Title != title {
			t.Errorf("position %d: want %q, got %q", i, title, got[i].Title)
		}
	}
}

func TestParseProjectsList(t *testing.T) {
	// `icb projects list --json` — item_count is a nullable pointer.
	const raw = `[
	  {"id": "p1", "name": "todoui", "position": 0, "item_count": 1},
	  {"id": "p2", "name": "Mobile capture + PWA", "position": 0, "item_count": 0},
	  {"id": "p3", "name": "dotfiles", "position": 0, "item_count": null}
	]`

	var projects []icbProject
	if err := json.Unmarshal([]byte(raw), &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	if projects[0].ItemCount == nil || *projects[0].ItemCount != 1 {
		t.Errorf("todoui item_count: want 1, got %v", projects[0].ItemCount)
	}
	if projects[2].ItemCount != nil {
		t.Errorf("dotfiles item_count: want nil, got %v", *projects[2].ItemCount)
	}
}

func TestSignificantTerms(t *testing.T) {
	got := significantTerms("Add the new icb resource CLI for work")

	kept := map[string]bool{}
	for _, term := range got {
		kept[term] = true
		if titleStopWords[term] {
			t.Errorf("stop word survived: %q", term)
		}
		if len(term) < 3 {
			t.Errorf("term too short to be distinctive: %q", term)
		}
	}
	// Short domain tokens are the identifying ones and must survive.
	for _, want := range []string{"icb", "cli", "resource"} {
		if !kept[want] {
			t.Errorf("expected %q to survive, got %v", want, got)
		}
	}
	for _, unwanted := range []string{"the", "new", "add", "for", "work"} {
		if kept[unwanted] {
			t.Errorf("%q should have been filtered, got %v", unwanted, got)
		}
	}
}

func TestDetectStaleItems(t *testing.T) {
	repoName := "ichrisbirch"
	repos := []repoBrief{{statusEntry: statusEntry{
		Name: repoName,
		StatusMD: "## Current State\n\n" +
			"The `icb` Go CLI fully replaced the ichrisbirch MCP — done end-to-end and live in production.\n" +
			"## What's Next\n- OpenTelemetry tracing, not started\n",
	}}}

	t.Run("flags an open item its repo calls finished", func(t *testing.T) {
		roadmap := []roadmapProject{{Name: "Personal OS", Items: []icbProjectItem{
			{ID: "abc", Title: "icb resource CLI", Repo: &repoName},
		}}}
		stale := detectStaleItems(repos, roadmap)
		if len(stale) != 1 {
			t.Fatalf("expected 1 stale item, got %d: %+v", len(stale), stale)
		}
		if stale[0].ID != "abc" || stale[0].Repo != repoName {
			t.Errorf("wrong item flagged: %+v", stale[0])
		}
		if !strings.Contains(stale[0].Reason, "fully replaced") {
			t.Errorf("reason must quote the evidence line, got %q", stale[0].Reason)
		}
	})

	t.Run("leaves genuinely open work alone", func(t *testing.T) {
		roadmap := []roadmapProject{{Name: "Personal OS", Items: []icbProjectItem{
			{ID: "def", Title: "OpenTelemetry tracing", Repo: &repoName},
		}}}
		if stale := detectStaleItems(repos, roadmap); len(stale) != 0 {
			t.Errorf("not-started work must not be flagged: %+v", stale)
		}
	})

	t.Run("ignores items with no repo link", func(t *testing.T) {
		roadmap := []roadmapProject{{Name: "Home", Items: []icbProjectItem{
			{ID: "ghi", Title: "Microwave Kitchen Cart"},
		}}}
		if stale := detectStaleItems(repos, roadmap); len(stale) != 0 {
			t.Errorf("non-repo work must never be flagged: %+v", stale)
		}
	})

	t.Run("a completion word in the title is not evidence about itself", func(t *testing.T) {
		// "recently-done" matched every status line reporting anything as done,
		// purely on the word "done" — a real false positive from live data.
		roadmap := []roadmapProject{{Name: "Personal OS", Items: []icbProjectItem{
			{ID: "mno", Title: "Pi wall + recently-done, then mobile PWA", Repo: &repoName},
		}}}
		if stale := detectStaleItems(repos, roadmap); len(stale) != 0 {
			t.Errorf("marker vocabulary must not identify an item: %+v", stale)
		}
	})

	t.Run("one shared word is not enough evidence", func(t *testing.T) {
		// Live false positive: "mobile" alone collided with an unrelated decision
		// that happened to carry a completion phrase.
		scoped := []repoBrief{{statusEntry: statusEntry{
			Name:     repoName,
			StatusMD: "Fitness Tracker is standalone meso. No longer an initiative; the mobile-first patterns live there.\n",
		}}}
		roadmap := []roadmapProject{{Name: "Personal OS", Items: []icbProjectItem{
			{ID: "pqr", Title: "Pi wall then mobile PWA", Repo: &repoName},
		}}}
		if stale := detectStaleItems(scoped, roadmap); len(stale) != 0 {
			t.Errorf("a single common term must not count as evidence: %+v", stale)
		}
	})

	t.Run("ignores a repo with no status doc", func(t *testing.T) {
		other := "keymap-align"
		roadmap := []roadmapProject{{Name: "Tools", Items: []icbProjectItem{
			{ID: "jkl", Title: "bundled layouts", Repo: &other},
		}}}
		if stale := detectStaleItems(repos, roadmap); len(stale) != 0 {
			t.Errorf("no status doc means no evidence: %+v", stale)
		}
	})
}

func TestNotePreview(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bold run", "Manifest is at **version 6**: adds a `binaries` section.", "Manifest is at version 6: adds a `binaries` section."},
		{"italic run", "markdownlint v0.47, which *added* MD060.", "markdownlint v0.47, which added MD060."},
		{"leading blank lines", "\n\n**No repo in the fleet uses it**\nsecond line", "No repo in the fleet uses it"},
		// A lone glob must not pair with an unrelated asterisk later in the line.
		{"unpaired globs", "Triggered by `tags: [cli/v*]`; pass both `--max-*` flags.", "Triggered by `tags: [cli/v*]`; pass both `--max-*` flags."},
		// Identifiers carry underscores far more often than these notes carry italics.
		{"underscored identifiers", "`__complete` skips the pull when content_hash is unchanged.", "`__complete` skips the pull when content_hash is unchanged."},
		{"bold before a closing quote", `Standard says "**No manual tagging ever.**" (release.md).`, `Standard says "No manual tagging ever." (release.md).`},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notePreview(tc.in); got != tc.want {
				t.Errorf("notePreview(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}
