package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/runner"
)

// computerCategory is the icb task category that flags dev/computer work. The
// task list is a capture inbox (things jotted down away from the computer), so
// `forge brief` surfaces only this category as an inbox to triage into the right
// project — the other categories are life admin for the web/mobile surface.
const computerCategory = "Computer"

var (
	briefFilterNames []string
	briefJSON        bool
	briefNoIssues    bool
	briefNoTasks     bool
)

var briefCmd = &cobra.Command{
	Use:   "brief",
	Short: "One-page cross-project work brief for an AI coding session",
	Long: `Compose a single, text-dense briefing of everything in flight across your
repos, meant to prime a Claude Code session.

This is the AI's dev brief across REPOS — full cross-project status and the items
that need actual computer work. It is NOT the human single pane of glass: that is
'menu dashboard', which is a glance across all life apps (tasks, habits, next book,
current learning). Two audiences, two scopes — they are not built to cover each other.

It joins three layers:

  - Planning   — each repo's .planning/status.md (Current State / In Progress /
                 What's Next / Not Doing) and design docs.
  - Roadmap    — your ordered ichrisbirch projects and their open items, via the
                 icb CLI. This is the "do the next thing, in order" queue.
  - Inbox      — Computer-category icb tasks. The task list is a capture inbox, so
                 these are surfaced to triage into the right project (they carry no
                 repo link themselves), alongside each repo's open GitHub issues.

The icb and GitHub layers degrade gracefully: if the icb CLI is missing or not
authenticated on this machine, or gh is unavailable, those layers are skipped
and noted under warnings rather than failing the brief.`,
	RunE: runBrief,
}

func init() {
	briefCmd.Flags().StringSliceVarP(&briefFilterNames, "filter", "F", nil, "comma-separated repo names to include")
	briefCmd.Flags().BoolVar(&briefJSON, "json", false, "output the full brief as JSON to stdout")
	briefCmd.Flags().BoolVar(&briefNoIssues, "no-issues", false, "skip fetching GitHub issues per repo")
	briefCmd.Flags().BoolVar(&briefNoTasks, "no-tasks", false, "skip the icb roadmap and tasks layers")
	rootCmd.AddCommand(briefCmd)
}

// ghIssue is one open GitHub issue for a repo (subset of `gh issue list --json`).
type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// icbProject mirrors the fields of `icb projects list --json` that the brief uses.
type icbProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	ItemCount *int   `json:"item_count"`
}

// icbProjectItem mirrors an item from `icb projects view <id> --json`.
type icbProjectItem struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Notes     *string `json:"notes"`
	Completed bool    `json:"completed"`
	Archived  bool    `json:"archived"`
	Position  int     `json:"position"`
}

// icbProjectView is the `icb projects view <id> --json` payload: the project
// fields plus its ordered items.
type icbProjectView struct {
	Items []icbProjectItem `json:"items"`
}

// icbTask mirrors the fields of `icb tasks todo --json` that the brief uses.
type icbTask struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Notes    *string `json:"notes"`
	Category string  `json:"category"`
	Priority int     `json:"priority"`
}

type repoBrief struct {
	statusEntry
	Issues []ghIssue `json:"issues,omitempty"`
}

type roadmapProject struct {
	Name  string           `json:"name"`
	Items []icbProjectItem `json:"items"`
}

type brief struct {
	Repos    []repoBrief      `json:"repos"`
	Roadmap  []roadmapProject `json:"roadmap,omitempty"`
	Inbox    []icbTask        `json:"inbox,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
}

func runBrief(cmd *cobra.Command, _ []string) error {
	cfg, err := loadRepos()
	if err != nil {
		return err
	}

	repos := runner.FilterRepos(runner.OwnedRepos(runner.ActiveRepos(cfg.Repos)), briefFilterNames)
	if len(repos) == 0 {
		return fmt.Errorf("no repos matched filter: %s", strings.Join(briefFilterNames, ", "))
	}

	var b brief

	// Planning layer (always available — local filesystem).
	entries := collectStatusEntries(repos, false)
	ghAvailable := !briefNoIssues && onPath("gh")
	if !briefNoIssues && !ghAvailable {
		b.Warnings = append(b.Warnings, "gh not found on PATH — GitHub issues skipped")
	}
	var unsynced []string
	for _, e := range entries {
		if e.Unsynced {
			unsynced = append(unsynced, e.Name)
		}
		rb := repoBrief{statusEntry: e}
		if ghAvailable {
			rb.Issues = fetchIssues(e.Path)
		}
		b.Repos = append(b.Repos, rb)
	}
	// A real .planning dir still renders, but Syncthing never sees it, so the
	// docs exist on one machine only. Surface it rather than silently diverging.
	if len(unsynced) > 0 {
		b.Warnings = append(b.Warnings, fmt.Sprintf(
			"%s: .planning is a real dir, not synced — run: forge dies run maintenance/sync-planning.sh -F %s",
			strings.Join(unsynced, ", "), strings.Join(unsynced, ","),
		))
	}

	// Roadmap + Computer-todo layers (icb CLI — remote, may be unauthenticated).
	if !briefNoTasks {
		if !onPath("icb") {
			b.Warnings = append(b.Warnings, "icb not found on PATH — roadmap and tasks skipped")
		} else {
			roadmap, todos, warns := fetchICB()
			b.Roadmap = roadmap
			b.Inbox = todos
			b.Warnings = append(b.Warnings, warns...)
		}
	}

	if briefJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(b)
	}

	renderBrief(b)
	return nil
}

// onPath reports whether an executable is resolvable on PATH.
func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// fetchIssues returns open GitHub issues for the repo at path. Any error (no
// remote, not authenticated, offline) yields no issues rather than failing —
// gh infers the repo from the working directory's git remote.
func fetchIssues(path string) []ghIssue {
	c := exec.Command("gh", "issue", "list", "--state", "open", "--limit", "30", "--json", "number,title")
	c.Dir = path
	out, err := c.Output()
	if err != nil {
		return nil
	}
	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil
	}
	return issues
}

// fetchICB pulls the ordered project roadmap and the Computer-category todos from
// the icb CLI. It returns any per-call failures as warnings so a missing auth or
// network blip degrades the brief instead of failing it.
func fetchICB() ([]roadmapProject, []icbTask, []string) {
	var warnings []string

	projects, err := icbProjects()
	var roadmap []roadmapProject
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("icb projects: %v", err))
	} else {
		for _, p := range projects {
			if p.ItemCount == nil || *p.ItemCount == 0 {
				continue
			}
			items, err := icbProjectItems(p.ID)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("icb projects view %s: %v", p.Name, err))
				continue
			}
			open := openItemsInOrder(items)
			if len(open) > 0 {
				roadmap = append(roadmap, roadmapProject{Name: p.Name, Items: open})
			}
		}
	}

	todos, err := icbComputerTodos()
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("icb tasks todo: %v", err))
	}

	return roadmap, todos, warnings
}

// icbRun executes an icb subcommand with --json and returns its stdout. On
// failure it surfaces the trimmed stderr, which for icb carries the actionable
// message (e.g. "not authenticated: run icb auth login").
func icbRun(args ...string) ([]byte, error) {
	c := exec.Command("icb", args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s", firstLine(msg))
		}
		return nil, err
	}
	return out, nil
}

func icbProjects() ([]icbProject, error) {
	out, err := icbRun("projects", "list", "--json")
	if err != nil {
		return nil, err
	}
	var projects []icbProject
	if err := json.Unmarshal(out, &projects); err != nil {
		return nil, fmt.Errorf("parsing projects: %w", err)
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Position < projects[j].Position })
	return projects, nil
}

func icbProjectItems(id string) ([]icbProjectItem, error) {
	out, err := icbRun("projects", "view", id, "--json")
	if err != nil {
		return nil, err
	}
	var view icbProjectView
	if err := json.Unmarshal(out, &view); err != nil {
		return nil, fmt.Errorf("parsing project items: %w", err)
	}
	return view.Items, nil
}

func icbComputerTodos() ([]icbTask, error) {
	out, err := icbRun("tasks", "todo", "--json")
	if err != nil {
		return nil, err
	}
	var tasks []icbTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("parsing tasks: %w", err)
	}
	return filterComputerTodos(tasks), nil
}

// openItemsInOrder returns the incomplete, non-archived items sorted by their
// position in the project — the hand-authored roadmap order ("do the next thing").
func openItemsInOrder(items []icbProjectItem) []icbProjectItem {
	var open []icbProjectItem
	for _, it := range items {
		if !it.Completed && !it.Archived {
			open = append(open, it)
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].Position < open[j].Position })
	return open
}

// filterComputerTodos keeps only the Computer-category tasks, in priority order.
func filterComputerTodos(tasks []icbTask) []icbTask {
	var out []icbTask
	for _, t := range tasks {
		if t.Category == computerCategory {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// renderBrief prints the human-readable brief to stdout. It writes via the
// fmt/color Print family (like runStatus) rather than an injected writer: those
// are errcheck's default-excluded stdout calls, and color auto-strips ANSI when
// stdout is not a TTY, so a piped brief is clean plain text.
func renderBrief(b brief) {
	bold := color.New(color.Bold)
	cyan := color.New(color.Bold, color.FgHiCyan)
	dim := color.New(color.Faint)
	yellow := color.New(color.FgHiYellow)

	cyan.Printf("# Forge Brief — %d repos\n", len(b.Repos))

	if len(b.Roadmap) > 0 {
		cyan.Printf("\n## Roadmap — ordered; do the next thing\n")
		for _, p := range b.Roadmap {
			bold.Printf("\n%s\n", p.Name)
			for i, it := range p.Items {
				fmt.Printf("  %d. %s\n", i+1, it.Title)
				if it.Notes != nil {
					if note := strings.TrimSpace(*it.Notes); note != "" {
						dim.Printf("     %s\n", firstLine(note))
					}
				}
			}
		}
	}

	if len(b.Inbox) > 0 {
		cyan.Printf("\n## Inbox — Computer tasks to triage into a project\n\n")
		for _, t := range b.Inbox {
			fmt.Printf("  [#%d] %s\n", t.ID, t.Name)
			if t.Notes != nil {
				if note := strings.TrimSpace(*t.Notes); note != "" {
					dim.Printf("        %s\n", firstLine(note))
				}
			}
		}
	}

	cyan.Printf("\n## Repos\n")
	for _, r := range b.Repos {
		bold.Printf("\n### %s", r.Name)
		dim.Printf(" (%s)\n", r.Path)
		if r.Description != "" {
			fmt.Printf("  %s\n", r.Description)
		}
		if r.StatusMD != "" {
			fmt.Println(r.StatusMD)
		}
		if len(r.DesignDocs) > 0 {
			dim.Printf("Design docs: %s\n", strings.Join(r.DesignDocs, ", "))
		}
		if len(r.Issues) > 0 {
			bold.Printf("Open issues (%d):\n", len(r.Issues))
			for _, is := range r.Issues {
				fmt.Printf("  #%d %s\n", is.Number, is.Title)
			}
		}
	}

	if len(b.Warnings) > 0 {
		yellow.Printf("\n⚠ %d source(s) skipped:\n", len(b.Warnings))
		for _, warn := range b.Warnings {
			yellow.Printf("  - %s\n", warn)
		}
	}
}

// firstLine returns the first non-empty line of s, trimmed — used to keep note
// previews to a single line in the human render.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
