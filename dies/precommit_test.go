package dies

import (
	"os"
	"strings"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

// plannedItems is what a plan would change, by item, for readable assertions.
func plannedItems(measured reconcile.Measurement) []string {
	var items []string
	for _, change := range measured.Fold(reconcile.LensPlan).Changes {
		items = append(items, change.Item)
	}
	return items
}

func TestPreCommitDeploysTheConfigsItsDeclaredStacksNeed(t *testing.T) {
	cases := map[string]struct {
		stacks  []string
		want    []string
		notWant []string
	}{
		"go":      {stacks: []string{"go"}, want: []string{".golangci.yml"}, notWant: []string{".prettierrc.json"}},
		"vue":     {stacks: []string{"vue"}, want: []string{".prettierrc.json"}, notWant: []string{".golangci.yml"}},
		"generic": {stacks: []string{"python"}, want: []string{".markdownlint.json", ".editorconfig", ".shellcheckrc"}, notWant: []string{".golangci.yml", ".prettierrc.json"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			target := fixture(t, stacks(tc.stacks...), nil)

			items := plannedItems(reconcile.Assess(target, PreCommit{}))

			for _, want := range tc.want {
				if !has(items, want) {
					t.Errorf("plan does not deploy %s: %v", want, items)
				}
			}
			for _, notWant := range tc.notWant {
				if has(items, notWant) {
					t.Errorf("plan deploys %s to a repo that declares no such stack", notWant)
				}
			}
		})
	}
}

func TestPreCommitApplyThenPlanIsClean(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	applyAll(t, target, PreCommit{})

	items := plannedItems(reconcile.Assess(target, PreCommit{}))
	// The git hooks stay pending: the fixture is not a git repo, so there is
	// nothing to install into and nothing is reported for them either.
	if len(items) != 0 {
		t.Errorf("a second look would still change %v", items)
	}
}

// Unmarked hooks abort a real sync rather than being destroyed. The fix is
// adding markers, not letting the sync take them.
func TestPreCommitReportsUnmarkedHooksAsByHandAndOffersNoWrite(t *testing.T) {
	target := fixture(t, stacks("go"), map[string]string{
		".pre-commit-config.yaml": strings.Join([]string{
			"# forge-toolchain: 1",
			"repos:",
			"  - repo: local",
			"    hooks:",
			"      - id: my-bespoke-check",
			"        name: my bespoke check",
			"        entry: ./check.sh",
			"        language: system",
			"",
		}, "\n"),
	})

	measured := reconcile.Assess(target, PreCommit{})

	var found bool
	for _, change := range measured.Changes {
		if change.Item == preCommitConfigPath {
			found = true
			if change.Repair != reconcile.ByHand {
				t.Errorf("repair = %q, want by_hand — a sync must not delete it", change.Repair)
			}
		}
	}
	if !found {
		t.Fatalf("an unmarked hook was not reported: %v", measured.Changes)
	}

	// And the config must not also be offered as an automatic rewrite, or the
	// plan promises a write Perform would refuse.
	for _, change := range measured.Fold(reconcile.LensPlan).Changes {
		if change.Item == preCommitConfigPath {
			t.Error("plan offers to rewrite a config whose custom hooks would be lost")
		}
	}
}

func TestPreCommitPreservesMarkedCustomHooks(t *testing.T) {
	custom := "# > custom:after:all - our own check\n" +
		"  - repo: local\n    hooks:\n      - id: my-bespoke-check\n" +
		"        name: my bespoke check\n        entry: ./check.sh\n        language: system\n"

	target := fixture(t, stacks("go"), map[string]string{
		".pre-commit-config.yaml": "# forge-toolchain: 1\nrepos:\n" + custom,
	})

	applyAll(t, target, PreCommit{})

	if got := readFile(t, target.Path(preCommitConfigPath)); !strings.Contains(got, "my-bespoke-check") {
		t.Errorf("the marked custom hook was destroyed:\n%s", got)
	}
}

// A repo's shellcheck exception is declared in the registry with its reason,
// and lands below the shared config rather than editing it.
func TestPreCommitAppendsDeclaredShellcheckExceptions(t *testing.T) {
	target := fixture(t, stacks("go"), nil)
	target.Repo.Toolchain.ShellcheckDisable = append(target.Repo.Toolchain.ShellcheckDisable,
		shellcheckException("SC2029", "every deploy script here builds a remote command from local variables"))

	applyAll(t, target, PreCommit{})

	got := readFile(t, target.Path(".shellcheckrc"))
	if !strings.Contains(got, "disable=SC2029") {
		t.Errorf("the declared exception is missing:\n%s", got)
	}
	if !strings.Contains(got, "remote command from local variables") {
		t.Errorf("the exception landed without its reason:\n%s", got)
	}
}

// A config can be schema-valid and still fail on the first commit.
func TestPreCommitReportsAnNpmScriptTheHookWouldCall(t *testing.T) {
	target := fixture(t, stacks("vue"), map[string]string{
		"package.json": `{"name":"fixture","scripts":{}}`,
	})

	measured := reconcile.Assess(target, PreCommit{})

	var found bool
	for _, change := range measured.Changes {
		if strings.Contains(change.Detail, "would fail on first use") {
			found = true
			if change.Repair != reconcile.ByHand {
				t.Errorf("repair = %q, want by_hand", change.Repair)
			}
		}
	}
	if !found {
		t.Errorf("a hook calling a script package.json does not declare was not reported: %v", measured.Changes)
	}
}

func TestPreCommitReadsWithoutWriting(t *testing.T) {
	target := fixture(t, stacks("go", "python"), map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n"})
	before := snapshot(t, sandbox(target))

	reconcile.Assess(target, PreCommit{})

	if after := snapshot(t, sandbox(target)); len(before) != len(after) {
		t.Errorf("observe created files: %v", keysOf(after))
	}
}

func TestPreCommitIgnoresARepoDeclaringNoToolchain(t *testing.T) {
	target := fixture(t, nil, nil)

	if measured := reconcile.Assess(target, PreCommit{}); len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
}

func TestDeclaredStagesParsesEachListEntry(t *testing.T) {
	// commit-msg is a substring of prepare-commit-msg, which is why this is
	// parsed rather than substring-matched.
	got := declaredStages("default_stages: [pre-commit]\n  stages: [prepare-commit-msg]\n")

	if has(got, "commit-msg") {
		t.Errorf("commit-msg matched inside prepare-commit-msg: %v", got)
	}
	if !has(got, "prepare-commit-msg") || !has(got, "pre-commit") {
		t.Errorf("stages = %v, want both declared entries", got)
	}
}

func TestUnexpandedPlaceholderIgnoresActionsSyntax(t *testing.T) {
	if unexpandedPlaceholder("run: echo ${{ github.sha }}") {
		t.Error("GitHub Actions syntax read as a failed substitution")
	}
	if !unexpandedPlaceholder("working-directory: {{dir}}") {
		t.Error("a real generator placeholder was not caught")
	}
}

func TestUnifiedDiffShowsOnlyTheChangedHunk(t *testing.T) {
	have := strings.Repeat("same\n", 50) + "old\n" + strings.Repeat("same\n", 50)
	want := strings.Repeat("same\n", 50) + "new\n" + strings.Repeat("same\n", 50)

	patch := unifiedDiff(".pre-commit-config.yaml", have, want)

	if !strings.Contains(patch, "-old") || !strings.Contains(patch, "+new") {
		t.Errorf("the change is missing from the diff:\n%s", patch)
	}
	if lineCount := strings.Count(patch, "\n"); lineCount > 12 {
		t.Errorf("the diff printed %d lines for a one-line change:\n%s", lineCount, patch)
	}
}

func TestPreCommitPerformRefusesAnItemNoLongerInTheStandard(t *testing.T) {
	target := fixture(t, stacks("go"), nil)

	outcome, err := PreCommit{}.Perform(target, reconcile.Change{
		Item: ".retired-config", Verdict: reconcile.Missing, Repair: reconcile.Automatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != reconcile.Refused {
		t.Errorf("status = %q, want refused", outcome.Status)
	}
	if _, err := os.Stat(target.Path(".retired-config")); err == nil {
		t.Error("a refused change still wrote a file")
	}
}
