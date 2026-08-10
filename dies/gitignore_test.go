package dies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/v6/reconcile"
)

// applyAll runs the die end to end, the way `forge repos apply` does.
func applyAll(t *testing.T, target reconcile.Target, die reconcile.Die) []reconcile.Outcome {
	t.Helper()
	return reconcile.Apply(reconcile.Assess(target, die))
}

func TestGitignorePythonRepoGetsCoverageAndBuildArtifacts(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{".gitignore": ".planning\n"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	for _, entry := range []string{".coverage", "coverage.xml", "dist/", "*.egg-info/"} {
		if !has(present, entry) {
			t.Errorf("missing entry: %s", entry)
		}
	}
}

func TestGitignoreGoRepoGetsNoPythonEntries(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": ".planning\n"})

	measured := reconcile.Assess(target, Gitignore{})

	if len(measured.Changes) != 0 {
		t.Fatalf("changes = %v, want none — the only universal entry is present", measured.Changes)
	}
	if has(lines(t, target.Path(".gitignore")), ".coverage") {
		t.Error(".coverage leaked into a repo declaring no python component")
	}
}

// A repo's own entries are what append-only exists to protect.
func TestGitignoreKeepsTheReposOwnEntries(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{
		".gitignore": "# what this project builds\nbuild-cache/\nsecrets.env\n",
	})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	for _, entry := range []string{"build-cache/", "secrets.env", "# what this project builds"} {
		if !has(present, entry) {
			t.Errorf("append-only lost the repo's own line: %s", entry)
		}
	}
}

// Skipping instead made a new repo the one case the golden path did not cover,
// which is where it is needed most.
func TestGitignoreCreatesTheFileWhenAbsent(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, Gitignore{})

	if !has(lines(t, target.Path(".gitignore")), ".planning") {
		t.Error("a repo with no .gitignore did not get one")
	}
}

// Without the newline guard the first entry lands glued to whatever the last
// line was, silently changing that line's meaning.
func TestGitignoreAppendsAfterAFileWithNoTrailingNewline(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": "node_modules/"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	if !has(present, "node_modules/") {
		t.Errorf("the last line was mangled: %v", present)
	}
	if !has(present, ".planning") {
		t.Errorf("the new entry did not land on its own line: %v", present)
	}
}

func TestGitignoreIsIdempotent(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{".gitignore": ".planning\n"})

	applyAll(t, target, Gitignore{})
	first, err := os.ReadFile(target.Path(".gitignore"))
	if err != nil {
		t.Fatal(err)
	}

	measured := reconcile.Assess(target, Gitignore{})
	if len(measured.Changes) != 0 {
		t.Errorf("a second look found %v, want nothing", measured.Changes)
	}

	applyAll(t, target, Gitignore{})
	second, _ := os.ReadFile(target.Path(".gitignore"))
	if string(first) != string(second) {
		t.Error("a second apply changed the file")
	}
}

// A downloaded gitignore is drift only a person can settle: pruning one is a
// judgement about what the repo actually builds.
func TestGitignoreReportsAGeneratedFileAsByHand(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".gitignore": "# Created by https://www.toptal.com/developers/gitignore\n.planning\n",
	})

	measured := reconcile.Assess(target, Gitignore{})

	var found bool
	for _, change := range measured.Changes {
		if change.Item == ".gitignore" {
			found = true
			if change.Repair != reconcile.ByHand {
				t.Errorf("repair = %q, want by_hand — forge must not rewrite it", change.Repair)
			}
		}
	}
	if !found {
		t.Fatal("a generated gitignore was not reported")
	}

	// It belongs to check, not plan: apply cannot fix it.
	if plan := measured.Fold(reconcile.LensPlan); plan.Status != reconcile.Converged {
		t.Errorf("plan status = %q, want converged — nothing here is apply's to do", plan.Status)
	}
	if check := measured.Fold(reconcile.LensCheck); check.Status != reconcile.Issue {
		t.Errorf("check status = %q, want issue", check.Status)
	}
}

func TestGitignoreReportsDuplicatesWithoutRemovingThem(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": ".planning\nsrc/\n.planning\n"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	var count int
	for _, line := range present {
		if line == ".planning" {
			count++
		}
	}
	if count != 2 {
		t.Errorf(".planning appears %d times, want 2 — removing a line is never this die's to do", count)
	}
}

// Perform is unreachable from the read verbs, but the method exists on the
// interface, so a by-hand change reaching it must refuse rather than write.
func TestGitignorePerformRefusesAByHandChange(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": ".planning\n"})
	before, _ := os.ReadFile(target.Path(".gitignore"))

	outcome, err := Gitignore{}.Perform(target, reconcile.Change{
		Item:    ".gitignore",
		Verdict: reconcile.Stale,
		Repair:  reconcile.ByHand,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != reconcile.Refused {
		t.Errorf("status = %q, want refused", outcome.Status)
	}

	after, _ := os.ReadFile(filepath.Join(target.Repo.Path, ".gitignore"))
	if string(before) != string(after) {
		t.Error("a refused change still wrote")
	}
}

// Observe ran before the plan was printed, and the operator may have read it
// for a while. Perform re-verifies rather than trusting what Diff saw.
func TestGitignorePerformSkipsAChangeThatHasSinceLanded(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	measured := reconcile.Assess(target, Gitignore{})

	if err := os.WriteFile(target.Path(".gitignore"), []byte(".planning\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := Gitignore{}.Perform(target, measured.Changes[0])
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != reconcile.Skipped {
		t.Errorf("status = %q, want skipped", outcome.Status)
	}
	if count := len(lines(t, target.Path(".gitignore"))); count != 1 {
		t.Errorf("the entry was written twice: %d lines", count)
	}
}
