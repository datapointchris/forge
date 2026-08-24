package ci

import (
	"os"
	"strings"
	"testing"
)

// runsOnLines returns every runs-on value in a generated workflow.
//
// Every job, not the first: the emission site is inside the per-component loop,
// so a change that reads visibility once outside it would still produce a
// correct-looking first job.
func runsOnLines(workflow string) []string {
	var values []string
	for _, line := range strings.Split(workflow, "\n") {
		if value, ok := strings.CutPrefix(line, "    runs-on: "); ok {
			values = append(values, value)
		}
	}
	return values
}

func TestAPrivateRepoTakesTheSelfHostedPoolOnEveryJob(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t),
		comps("go", "api", "go", "cli", "vue", "web"), nil, false, SelfHosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	values := runsOnLines(workflow)
	if len(values) != 3 {
		t.Fatalf("runs-on lines = %v, want one per job", values)
	}
	for _, value := range values {
		if value != string(SelfHosted) {
			t.Errorf("runs-on = %q, want %q", value, SelfHosted)
		}
	}
}

// The security property the whole visibility split exists for. A fork's pull
// request on a public repo runs the fork's code on whatever runner it reaches,
// and the self-hosted one sits on the homelab VLAN.
//
// The label is searched for across the whole workflow rather than on the
// runs-on lines alone, because a block that named it in a step would reach the
// runner just as well.
func TestAPublicRepoNeverNamesTheSelfHostedPool(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t),
		comps("go", "api", "go", "cli", "vue", "web"), nil, false, Hosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if strings.Contains(workflow, RunnerLabel) {
		t.Errorf("a public repo's workflow names %q:\n%s", RunnerLabel, workflow)
	}
	for _, value := range runsOnLines(workflow) {
		if value != string(Hosted) {
			t.Errorf("runs-on = %q, want %q", value, Hosted)
		}
	}
}

func TestRunnerForSendsOnlyAPositivelyPrivateRepoToTheRunner(t *testing.T) {
	if got := RunnerFor(true); got != SelfHosted {
		t.Errorf("RunnerFor(true) = %q, want %q", got, SelfHosted)
	}
	if got := RunnerFor(false); got != Hosted {
		t.Errorf("RunnerFor(false) = %q, want %q", got, Hosted)
	}
}

// A caller that leaves the runner off gets a workflow rather than `runs-on:`
// with nothing after it. GitHub rejects that at dispatch, where the failure is
// a queued job on a repo whose CI reads green.
func TestTheZeroRunnerFallsBackToTheHostedImage(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, false, Runner(""))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, value := range runsOnLines(workflow) {
		if value != string(Hosted) {
			t.Errorf("runs-on = %q, want the hosted image", value)
		}
	}
}

// The two halves of the label have to be the same string. A workflow naming a
// pool the lint config does not declare fails actionlint in every repo it
// reaches, and the reverse leaves a declaration for a label nothing uses.
func TestTheLintConfigDeclaresExactlyTheLabelTheWorkflowNames(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, false, SelfHosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	values := runsOnLines(workflow)
	if len(values) == 0 {
		t.Fatal("the workflow names no runner at all")
	}
	label := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(values[0], "[self-hosted,"), "]"))

	if !strings.Contains(ActionlintConfig(1), "\n    - "+label+"\n") {
		t.Errorf("the workflow runs on %q and the lint config does not declare it:\n%s",
			label, ActionlintConfig(1))
	}
}

// The stamp is what answers "is this repo current", and a generated file
// without one reads as hand-written to every die that checks.
func TestTheLintConfigCarriesTheGeneratedStamp(t *testing.T) {
	if !strings.HasPrefix(ActionlintConfig(42), "# forge-toolchain: 42\n") {
		t.Errorf("stamp missing or wrong: %q", strings.SplitN(ActionlintConfig(42), "\n", 2)[0])
	}
}
