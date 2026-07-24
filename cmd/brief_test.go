package cmd

import (
	"encoding/json"
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
