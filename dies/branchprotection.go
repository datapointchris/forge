package dies

import (
	"fmt"
	"strings"

	"github.com/datapointchris/forge/v7/reconcile"
)

// BranchProtection makes "never force-push main" mechanical instead of a rule
// to remember.
//
// Rebasing a feature branch requires force-pushing it, so force-push cannot be
// banned outright. What must never happen is a force aimed at the default
// branch, and a server-side rule is the only thing that still holds when the
// local hook is bypassed, when the push comes from another machine, or when the
// person at the keyboard is in a hurry.
//
// Deliberately the two narrowest rules and nothing else. Required reviews
// deadlock a solo repo outright — GitHub will not let you approve your own PR —
// and requiring a PR would forbid the direct-to-main commits this workflow
// depends on for small work. Force-push and deletion are the only two
// operations that lose history rather than add to it.
//
// enforce_admins is what makes it real rather than decorative: without it the
// sole admin, which is the only account that ever pushes to these repos,
// bypasses every rule above.
type BranchProtection struct{}

func (BranchProtection) Name() string { return "branch-protection" }

func (BranchProtection) Description() string {
	return "Block force-push and deletion on the default branch, with enforce_admins so the sole admin is subject to it too. Adds no review or PR requirement."
}

func (BranchProtection) Tags() []string {
	return []string{"github", "branches", "safety", "standardization"}
}

// protectionRules is the standard, as the API spells it.
var protectionRules = []struct {
	field string
	want  string
}{
	{"allow_force_pushes", "false"},
	{"allow_deletions", "false"},
	{"enforce_admins", "true"},
}

type branchProtectionState struct {
	repo    ghRepo
	changes []reconcile.Change
	summary string
}

func (s branchProtectionState) Summary() string {
	if s.summary != "" {
		return s.summary
	}
	return "branch protection current on " + s.repo.defaultBranch
}

func (BranchProtection) Observe(t reconcile.Target) (reconcile.Observation, error) {
	repo, unknown, summary, err := observeGitHub(t, "branch protection")
	if err != nil || summary != "" || unknown != nil {
		return branchProtectionState{changes: unknown, summary: summary}, err
	}

	// Read from the API rather than assumed to be main: the default-branch die
	// exists precisely because that was not always true.
	if repo.defaultBranch == "" {
		return branchProtectionState{repo: repo, summary: "no default branch yet (empty repo)"}, nil
	}

	// A branch with no protection 404s, which is a state to fix rather than an
	// error to report.
	out, err := runIn(t.Repo.Path, "gh", "api",
		fmt.Sprintf("repos/%s/branches/%s/protection", repo.slug, repo.defaultBranch),
		"--jq", `"\(.allow_force_pushes.enabled) \(.allow_deletions.enabled) \(.enforce_admins.enabled)"`)
	if err != nil {
		return branchProtectionState{repo: repo, changes: []reconcile.Change{{
			Item:    repo.defaultBranch,
			Verdict: reconcile.Missing,
			Repair:  reconcile.Automatic,
			Detail:  "unprotected — force-push and deletion would be allowed",
		}}}, nil
	}

	fields := strings.Fields(out)
	if len(fields) != len(protectionRules) {
		return branchProtectionState{repo: repo, changes: []reconcile.Change{
			unknownChange(repo.defaultBranch, "unreadable protection for "+repo.slug),
		}}, nil
	}

	var changes []reconcile.Change
	for i, rule := range protectionRules {
		if fields[i] != rule.want {
			changes = append(changes, settingChange(rule.field, fields[i], rule.want))
		}
	}

	return branchProtectionState{repo: repo, changes: changes}, nil
}

func (BranchProtection) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(branchProtectionState)
	if !ok {
		return nil, fmt.Errorf("branch-protection: unexpected observation %T", observed)
	}
	return state.changes, nil
}

// protectionRequest is classic branch protection rather than a ruleset: the
// nulls are how the API spells "configure nothing else", where a ruleset would
// need every unwanted rule named explicitly.
const protectionRequest = `{
  "required_status_checks": null,
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}`

func (b BranchProtection) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := b.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(branchProtectionState)
	if len(state.changes) == 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already protected"}, nil
	}

	// One PUT carries the whole ruleset, so the first Change of a batch sets
	// every field and its siblings report Skipped on the re-read above.
	if err := pipeTo(t.Repo.Path, protectionRequest, "gh", "api", "-X", "PUT",
		fmt.Sprintf("repos/%s/branches/%s/protection", state.repo.slug, state.repo.defaultBranch),
		"--input", "-", "--silent"); err != nil {
		// Classic protection on a private repo needs a paid plan. That is a
		// precondition GitHub imposes, not a write that failed, so it refuses
		// rather than failing the run for every private repo in the portfolio.
		return reconcile.Outcome{
			Change:  change,
			Status:  reconcile.Refused,
			Message: "could not set protection (private repos need a paid plan)",
		}, nil
	}

	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "protected"}, nil
}
