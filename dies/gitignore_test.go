package dies

import (
	"os"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

// applyAll runs the die end to end, the way `forge repos apply` does.
func applyAll(t *testing.T, target reconcile.Target, die reconcile.Die) []reconcile.Outcome {
	t.Helper()
	return reconcile.Apply(reconcile.Assess(target, die))
}

func TestGitignorePythonRepoGetsBytecodeCoverageAndBuildArtifacts(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{".gitignore": "bin/\n"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	for _, entry := range []string{"__pycache__/", ".coverage", "coverage.xml", "dist/", "*.egg-info/"} {
		if !has(present, entry) {
			t.Errorf("missing entry: %s", entry)
		}
	}
}

// cargo builds into target/ beside the Cargo.toml it was run against, so each
// declared Rust directory needs its own line.
func TestGitignoreRustRepoGetsCargoBuildOutput(t *testing.T) {
	declared := stacks("rust")
	declared.Components = append(declared.Components, config.Component{Stack: "rust", Dir: "crates"})
	target := fixture(t, declared, map[string]string{".gitignore": "bin/\n"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	for _, entry := range []string{"/target", "/crates/target"} {
		if !has(present, entry) {
			t.Errorf("a repo declaring rust at . and crates did not get %s", entry)
		}
	}
}

// `cargo new` writes exactly "/target". Spelling the entry any other way would
// add a second line saying the same thing, which the duplicate check cannot see
// because it compares strings.
func TestGitignoreAddsNothingToARepoCargoNewAlreadyIgnored(t *testing.T) {
	target := fixture(t, stacks("rust"), map[string]string{".gitignore": "/target\n"})

	measured := reconcile.Assess(target, Gitignore{})

	if len(measured.Changes) != 0 {
		t.Fatalf("changes = %v, want none — cargo's own entry already covers it", measured.Changes)
	}
}

func TestGitignoreGoRepoGetsNoRustOrPythonEntries(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": "bin/\n"})

	applyAll(t, target, Gitignore{})

	if has(lines(t, target.Path(".gitignore")), "/target") {
		t.Error("/target leaked into a repo declaring no rust component")
	}
}

func TestGitignoreGoRepoGetsNoPythonEntries(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": "bin/\n"})

	measured := reconcile.Assess(target, Gitignore{})

	if len(measured.Changes) != 0 {
		t.Fatalf("changes = %v, want none — a go repo has no entry to assert", measured.Changes)
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
	target := fixture(t, stacks("python"), nil)

	applyAll(t, target, Gitignore{})

	if !has(lines(t, target.Path(".gitignore")), "__pycache__/") {
		t.Error("a repo with no .gitignore did not get one")
	}
}

// Without the newline guard the first entry lands glued to whatever the last
// line was, silently changing that line's meaning.
func TestGitignoreAppendsAfterAFileWithNoTrailingNewline(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{".gitignore": "node_modules/"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	if !has(present, "node_modules/") {
		t.Errorf("the last line was mangled: %v", present)
	}
	if !has(present, "__pycache__/") {
		t.Errorf("the new entry did not land on its own line: %v", present)
	}
}

func TestGitignoreIsIdempotent(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{".gitignore": "bin/\n"})

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
// judgment about what the repo actually builds.
func TestGitignoreReportsAGeneratedFileAsByHand(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".gitignore": "# Created by https://www.toptal.com/developers/gitignore\nbin/\n",
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
	target := fixture(t, stacks("python"), map[string]string{".gitignore": "__pycache__/\nsrc/\n__pycache__/\n"})

	applyAll(t, target, Gitignore{})

	present := lines(t, target.Path(".gitignore"))
	var count int
	for _, line := range present {
		if line == "__pycache__/" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("__pycache__/ appears %d times, want 2 — removing an asserted line is never this die's to do", count)
	}
}

// Observe ran before the plan was printed, and the operator may have read it
// for a while. Perform re-verifies rather than trusting what Diff saw.
func TestGitignorePerformSkipsAChangeThatHasSinceLanded(t *testing.T) {
	target := fixture(t, stacks("python"), nil)
	measured := reconcile.Assess(target, Gitignore{})

	if err := os.WriteFile(target.Path(".gitignore"), []byte(measured.Changes[0].Item+"\n"), 0o644); err != nil {
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

func TestGitignoreRemovesARetiredEntryAndKeepsTheRest(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": ".planning\n\n# what this project builds\nbin/\n.planning\n"})

	measured := reconcile.Assess(target, Gitignore{})
	if len(measured.Changes) != 1 || measured.Changes[0].Repair != reconcile.Automatic {
		t.Fatalf("changes = %v, want one automatic removal", measured.Changes)
	}

	applyAll(t, target, Gitignore{})

	if got := readFile(t, target.Path(".gitignore")); got != "# what this project builds\nbin/\n" {
		t.Errorf(".gitignore = %q, want the repo's own lines and nothing above them", got)
	}
}

// The blank line separating a retired entry from what came before goes with it,
// so a removal leaves neither a trailing blank line nor two blank lines in a row.
func TestGitignoreRetiredEntryTakesTheBlankLineAboveIt(t *testing.T) {
	cases := map[string]struct{ before, after string }{
		"last line":   {"bin/\n\n.planning\n", "bin/\n"},
		"between two": {"bin/\n\n.planning\n\n# docs\nsite/\n", "bin/\n\n# docs\nsite/\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			target := fixture(t, stacks("go"), map[string]string{".gitignore": c.before})

			applyAll(t, target, Gitignore{})

			if got := readFile(t, target.Path(".gitignore")); got != c.after {
				t.Errorf(".gitignore = %q, want %q", got, c.after)
			}
		})
	}
}

// Only the exact line this die wrote is retired. Another spelling is a line the
// repo chose, and a match that guessed would take a repo's own entry with it.
func TestGitignoreLeavesAnotherSpellingOfARetiredEntry(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": ".planning/\n"})

	if measured := reconcile.Assess(target, Gitignore{}); len(measured.Changes) != 0 {
		t.Fatalf("changes = %v, want none", measured.Changes)
	}
}

func TestGitignoreDeletesAFileHoldingOnlyARetiredEntry(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{".gitignore": ".planning\n"})

	applyAll(t, target, Gitignore{})

	if _, err := os.Stat(target.Path(".gitignore")); !os.IsNotExist(err) {
		t.Errorf("a .gitignore left with nothing in it was kept: %v", err)
	}
}

func TestGitignoreRetiredEntryRemovalIsIdempotent(t *testing.T) {
	target := fixture(t, stacks("python"), map[string]string{".gitignore": ".planning\nbin/\n"})

	applyAll(t, target, Gitignore{})
	if measured := reconcile.Assess(target, Gitignore{}); len(measured.Changes) != 0 {
		t.Errorf("a second look found %v, want nothing", measured.Changes)
	}
	if has(lines(t, target.Path(".gitignore")), ".planning") {
		t.Error("the retired entry survived apply")
	}
}
