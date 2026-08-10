package dies

import (
	"strings"
	"testing"

	"github.com/datapointchris/forge/v7/reconcile"
)

func TestMarkdownlintignoreCreatesTheFileWhenAbsent(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"CHANGELOG.md": "# Changelog\n"})

	applyAll(t, target, Markdownlintignore{})

	if !has(lines(t, target.Path(".markdownlintignore")), "CHANGELOG.md") {
		t.Error("a repo with no .markdownlintignore did not get one")
	}
}

func TestMarkdownlintignoreCreatedFileHasNoLeadingBlankLine(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, Markdownlintignore{})

	content := readFile(t, target.Path(".markdownlintignore"))
	if strings.HasPrefix(content, "\n") {
		t.Errorf("created file opens with a blank line:\n%q", content)
	}
}

func TestMarkdownlintignorePreservesProjectEntries(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".markdownlintignore": "# our own analysis docs\nanalysis/\nPLANNING.md\n",
		"CHANGELOG.md":        "# Changelog\n",
	})

	applyAll(t, target, Markdownlintignore{})

	present := lines(t, target.Path(".markdownlintignore"))
	for _, entry := range []string{"analysis/", "PLANNING.md", "# our own analysis docs"} {
		if !has(present, entry) {
			t.Errorf("append-only lost the repo's own line: %s", entry)
		}
	}
}

// A bare filename gives a reader no way to tell a deliberate exclusion from a
// forgotten one, so the die writes the reason above the entry it adds.
func TestMarkdownlintignoreExplainsTheEntryItAdds(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"CHANGELOG.md": "# Changelog\n"})

	applyAll(t, target, Markdownlintignore{})

	content := readFile(t, target.Path(".markdownlintignore"))
	if !strings.Contains(content, "# ") {
		t.Errorf("the entry landed with no explanation:\n%s", content)
	}
	if !strings.Contains(content, "semantic-release") {
		t.Errorf("the comment does not say why the entry is there:\n%s", content)
	}
}

func TestMarkdownlintignoreLeavesAnExistingEntryUncommented(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".markdownlintignore": "CHANGELOG.md\n",
		"CHANGELOG.md":        "# Changelog\n",
	})

	measured := reconcile.Assess(target, Markdownlintignore{})

	if len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none — the entry is already there", measured.Changes)
	}
}

func TestMarkdownlintignoreRequiresAWholeLineMatch(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".markdownlintignore": "docs/CHANGELOG.md\n",
		"CHANGELOG.md":        "# Changelog\n",
	})

	measured := reconcile.Assess(target, Markdownlintignore{})

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1 — a substring match is not the entry", measured.Changes)
	}
}

func TestMarkdownlintignoreHandlesMissingTrailingNewline(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".markdownlintignore": "analysis/"})

	applyAll(t, target, Markdownlintignore{})

	present := lines(t, target.Path(".markdownlintignore"))
	if !has(present, "analysis/") {
		t.Errorf("the last line was mangled: %v", present)
	}
	if !has(present, "CHANGELOG.md") {
		t.Errorf("the new entry did not land on its own line: %v", present)
	}
}

func TestMarkdownlintignoreIsIdempotent(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{"CHANGELOG.md": "# Changelog\n"})

	applyAll(t, target, Markdownlintignore{})
	first := readFile(t, target.Path(".markdownlintignore"))

	applyAll(t, target, Markdownlintignore{})
	if second := readFile(t, target.Path(".markdownlintignore")); first != second {
		t.Errorf("a second apply changed the file:\n%q\n%q", first, second)
	}
}

// The two dies this replaces disagreed: the sync seeded every repo, the check
// deliberately passed a repo with no CHANGELOG.md rather than train skimming.
// Both are right about their own verb, and Repair is what lets each have it.
func TestMarkdownlintignoreSeedsWithoutAChangelogButReportsNothingWrong(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	measured := reconcile.Assess(target, Markdownlintignore{})

	plan := measured.Fold(reconcile.LensPlan)
	if plan.Status != reconcile.Drift {
		t.Errorf("plan status = %q, want drift — apply would add the entry", plan.Status)
	}

	check := measured.Fold(reconcile.LensCheck)
	if check.Status != reconcile.Converged {
		t.Errorf("check status = %q, want converged — nothing is wrong with this repo", check.Status)
	}
}

func TestMarkdownlintignoreReportsAnUnignoredChangelogInThePlan(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".markdownlintignore": "analysis/\n",
		"CHANGELOG.md":        "# Changelog\n",
	})

	plan := reconcile.Assess(target, Markdownlintignore{}).Fold(reconcile.LensPlan)

	if plan.Status != reconcile.Drift || plan.Pending != 1 {
		t.Errorf("status = %q pending = %d, want drift with 1 pending", plan.Status, plan.Pending)
	}
}
