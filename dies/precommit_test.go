package dies

import (
	"os"
	"slices"
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

// The `# forge-toolchain:` prefix is the whole of what this fixture pins,
// because it is the whole of what maintained reads. The digit after it is inert
// — any number leaves every test in this file passing.
const strandedStamp = "# forge-toolchain: 11\nfail_fast: true\ndefault_stages: [pre-commit]\nrepos: []\n"

func TestPrecommitMaintainsARepoItStampedButDoesNotTrack(t *testing.T) {
	target := fixture(t, nil, map[string]string{preCommitConfigPath: strandedStamp})

	measured := reconcile.Assess(target, PreCommit{})

	if len(measured.Changes) == 0 {
		t.Fatalf("summary = %q, want the stale files reported", measured.Summary)
	}
	var items []string
	for _, c := range measured.Changes {
		items = append(items, c.Item)
	}
	// The three generic tool configs plus the config itself. A stack the registry
	// does not name is not guessed at, so nothing go- or vue-shaped appears.
	for _, want := range []string{preCommitConfigPath, ".editorconfig", ".shellcheckrc", ".markdownlint.json"} {
		if !slices.Contains(items, want) {
			t.Errorf("no change for %s; got %v", want, items)
		}
	}
	for _, unwanted := range []string{".golangci.yml", ".prettierrc.json"} {
		if slices.Contains(items, unwanted) {
			t.Errorf("%s was generated for a repo declaring no components: %v", unwanted, items)
		}
	}
}

// Without a stamp there is no file forge wrote, so there is nothing to maintain
// and first deployment stays gated on the declaration. This is what keeps
// runner.ActiveRepos' reasoning intact: naming an untouched repo with -F still
// writes nothing into it.
func TestPrecommitLeavesARepoItNeverWroteTo(t *testing.T) {
	target := fixture(t, nil, nil)

	measured := reconcile.Assess(target, PreCommit{})

	if len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none for a repo forge has never written to", measured.Changes)
	}
	if measured.Summary != unmaintained.reason() {
		t.Errorf("summary = %q, want it to say why", measured.Summary)
	}
}

// The two not-applicable states have opposite remedies, so they cannot share a
// sentence. Declaring the first repo deploys the standard files. Declaring the
// second deploys nothing and surfaces its unmarked hooks instead.
func TestPrecommitSaysWhichOfTheTwoReasonsItIs(t *testing.T) {
	never := reconcile.Assess(fixture(t, nil, nil), PreCommit{})
	unstamped := reconcile.Assess(
		fixture(t, nil, map[string]string{preCommitConfigPath: "repos:\n  - repo: local\n    hooks: []\n"}),
		PreCommit{})

	if never.Summary == unstamped.Summary {
		t.Fatalf("both states report %q, so a reader cannot tell which remedy applies", never.Summary)
	}
	if !strings.Contains(unstamped.Summary, toolchainStamp) {
		t.Errorf("summary = %q, want it to name the missing stamp", unstamped.Summary)
	}
}

// A hand-written config is not forge's to maintain, whatever else is true. The
// stamp is the only thing separating the two, so its absence has to be enough
// on its own.
func TestPrecommitLeavesAnUnstampedConfigAlone(t *testing.T) {
	handWritten := "repos:\n  - repo: local\n    hooks: []\n"
	target := fixture(t, nil, map[string]string{preCommitConfigPath: handWritten})

	measured := reconcile.Assess(target, PreCommit{})

	if len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none for a config forge did not write", measured.Changes)
	}
}

// A stamp says forge wrote these files. It says nothing about hooks forge never
// installed, and installing them would put a commit gate on a repo nobody works
// in and build every hook environment its frozen config names.
func TestPrecommitDoesNotInstallHooksInARepoItOnlyStamped(t *testing.T) {
	target := fixture(t, nil, map[string]string{preCommitConfigPath: strandedStamp})
	for _, stage := range hookStages {
		if err := os.Remove(target.Path(".git", "hooks", stage)); err != nil {
			t.Fatalf("remove hook %s: %s", stage, err)
		}
	}

	measured := reconcile.Assess(target, PreCommit{})

	// The positive half: the die did run, so "no install offered" is not "no
	// run".
	if len(measured.Changes) == 0 {
		t.Fatalf("the die reported nothing at all: %q", measured.Summary)
	}
	for _, c := range measured.Changes {
		if strings.HasPrefix(c.Item, ".git/hooks/") && c.Actionable() {
			t.Errorf("would install %s into a repo it only stamped", c.Item)
		}
	}
}

// The same fixture with a declaration still reports the missing hooks, which is
// what makes the assertion above a narrowing rather than a removal.
func TestPrecommitStillInstallsHooksWhereTheRegistryDeclaresTheRepo(t *testing.T) {
	target := fixture(t, stacks("shell"), map[string]string{preCommitConfigPath: strandedStamp})
	for _, stage := range hookStages {
		if err := os.Remove(target.Path(".git", "hooks", stage)); err != nil {
			t.Fatalf("remove hook %s: %s", stage, err)
		}
	}

	measured := reconcile.Assess(target, PreCommit{})

	var hooks int
	for _, c := range measured.Changes {
		if strings.HasPrefix(c.Item, ".git/hooks/") {
			hooks++
		}
	}
	if hooks == 0 {
		t.Errorf("a declared repo lost its hook installation: %v", measured.Changes)
	}
}

// A stamped repo's config names hook stages, and forge does not install them
// there. Dropping the finding as well let apply write that config and plan then
// report converged over a state the die's own hookStages comment defines as
// broken — a commit gate declared in the config and absent from .git/hooks.
func TestPrecommitReportsUninstalledHooksItMayNotInstallInAStampedRepo(t *testing.T) {
	target := fixture(t, nil, map[string]string{preCommitConfigPath: strandedStamp})
	for _, stage := range hookStages {
		if err := os.Remove(target.Path(".git", "hooks", stage)); err != nil {
			t.Fatalf("remove hook %s: %s", stage, err)
		}
	}

	measured := reconcile.Assess(target, PreCommit{})

	var reported []string
	for _, c := range measured.Changes {
		if !strings.HasPrefix(c.Item, ".git/hooks/") {
			continue
		}
		reported = append(reported, c.Item)
		if c.Actionable() {
			t.Errorf("%s is actionable, so apply would install a hook the stamp does not authorize", c.Item)
		}
		if c.Repair != reconcile.ByHand {
			t.Errorf("%s repair = %q, want by_hand", c.Item, c.Repair)
		}
	}
	if len(reported) == 0 {
		t.Fatalf("no uninstalled hook was reported at all: %v", measured.Changes)
	}

	// check is the verb that has to carry it. plan must stay silent, because
	// apply has nothing it can do here.
	if got := measured.Fold(reconcile.LensCheck).Changes; len(got) == 0 {
		t.Error("check reported nothing, so the state is invisible at exit 3")
	}
	for _, c := range measured.Fold(reconcile.LensPlan).Changes {
		if strings.HasPrefix(c.Item, ".git/hooks/") {
			t.Errorf("plan offers to install %s", c.Item)
		}
	}
}

// The stamp authorizing four files lives in one of them. Deleting that one
// returned the other three to unreachable-and-converged, which is the state this
// die exists to end.
func TestPrecommitReportsToolConfigsStrandedByADeletedConfig(t *testing.T) {
	generic := map[string]string{
		".editorconfig":      "root = true\n",
		".shellcheckrc":      "disable=SC1091\n",
		".markdownlint.json": "{}\n",
	}
	target := fixture(t, nil, generic)

	measured := reconcile.Assess(target, PreCommit{})

	reported := map[string]reconcile.Change{}
	for _, c := range measured.Changes {
		reported[c.Item] = c
	}
	for rel := range generic {
		change, ok := reported[rel]
		if !ok {
			t.Errorf("%s sits stale and unreported: %v", rel, measured.Changes)
			continue
		}
		if change.Actionable() {
			t.Errorf("%s is actionable, so apply would take a file forge cannot prove it wrote", rel)
		}
	}

	// check is where it has to land, and apply must still have nothing to do.
	if len(measured.Fold(reconcile.LensCheck).Changes) == 0 {
		t.Error("check reported nothing, so the state stays invisible at exit 3")
	}
	if got := measured.Fold(reconcile.LensPlan).Changes; len(got) != 0 {
		t.Errorf("plan offers %v, but forge may not touch these", got)
	}

	// Nothing was written, and nothing was removed.
	for rel, want := range generic {
		data, err := os.ReadFile(target.Path(rel))
		if err != nil || string(data) != want {
			t.Errorf("%s was modified or removed by a read verb", rel)
		}
	}
}

// A repo keeping its own .editorconfig is not one forge deployed to. All three
// or nothing is the discriminator, so a partial set says nothing.
func TestPrecommitStaysQuietAboutAnEditorconfigItDidNotDeploy(t *testing.T) {
	target := fixture(t, nil, map[string]string{".editorconfig": "root = true\n"})

	measured := reconcile.Assess(target, PreCommit{})

	if len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none for one file forge cannot claim", measured.Changes)
	}
}
