package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
		comps("go", "api", "go", "cli", "vue", "web"), nil, Ungated, SelfHosted)
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
// request on a public repo runs the fork's own code on whatever runner it lands
// on, and a self-hosted runner sits inside a private network.
//
// The label is searched for across the whole workflow rather than on the
// runs-on lines alone, because a block that named it in a step would reach the
// runner just as well.
func TestAPublicRepoNeverNamesTheSelfHostedPool(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t),
		comps("go", "api", "go", "cli", "vue", "web"), nil, Ungated, Hosted)
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
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, Ungated, Runner(""))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, value := range runsOnLines(workflow) {
		if value != string(Hosted) {
			t.Errorf("runs-on = %q, want the hosted image", value)
		}
	}
}

// The lint config has to be a document actionlint reads, not merely a file
// containing the label.
//
// Both the workflow and the config render RunnerLabel, so comparing the two
// strings compares a constant with itself — rewriting RunnerLabel leaves that
// comparison passing. What can actually diverge is the structure around the
// label: rename the self-hosted-runner key or reshape the nesting and the file
// still contains the right word while declaring nothing to actionlint, which is
// silent until it fails a private repo's hook on the next commit.
func TestTheLintConfigDeclaresTheLabelWhereActionlintReadsIt(t *testing.T) {
	workflow, err := Generate(os.DirFS("blocks"), testManifest(t), comps("go", "."), nil, Ungated, SelfHosted)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	values := runsOnLines(workflow)
	if len(values) == 0 {
		t.Fatal("the workflow names no runner at all")
	}
	label := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(values[0], "[self-hosted,"), "]"))

	var parsed struct {
		SelfHostedRunner struct {
			Labels []string `yaml:"labels"`
		} `yaml:"self-hosted-runner"`
	}
	if err := yaml.Unmarshal([]byte(ActionlintConfig(1)), &parsed); err != nil {
		t.Fatalf("the lint config is not valid YAML: %v", err)
	}

	if !slices.Contains(parsed.SelfHostedRunner.Labels, label) {
		t.Errorf("the workflow runs on %q and self-hosted-runner.labels is %v:\n%s",
			label, parsed.SelfHostedRunner.Labels, ActionlintConfig(1))
	}
}

// Every production caller outside this package takes its runner from RunnerFor
// rather than naming SelfHosted itself.
//
// Before the runner was a parameter, runs-on was the literal ubuntu-latest and
// a public repo naming the pool was unreachable by construction. It is now a
// caller's promise and SelfHosted is exported, so the guarantee is worth only
// as much as every call site — and a new one hardcoding it fails nothing else
// here. Asserted against the tree rather than against the one caller that
// exists, because the caller that breaks this has not been written yet.
//
// Test files are exempt on purpose: the die's own lint test passes SelfHosted
// directly, to measure what actionlint does with a workflow naming the pool.
// Comparing against SelfHosted is fine and the die does it twice; what no
// caller may do is hand Generate a runner it chose itself. So the check reads
// the argument rather than the token, which a grep cannot tell apart.
func TestNoProductionCallerOutsideThisPackageNamesTheSelfHostedRunner(t *testing.T) {
	var offenders []string

	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		// Names bound to a RunnerFor result, so the common shape of assigning it
		// to a local and passing that local is accepted. Anything else — a
		// constant, a field, a value from elsewhere — is a caller choosing the
		// runner itself, which is the thing being ruled out.
		derived := map[string]bool{}
		ast.Inspect(parsed, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, rhs := range assign.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || !isSelector(call.Fun, "ci", "RunnerFor") || i >= len(assign.Lhs) {
					continue
				}
				if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
					derived[ident.Name] = true
				}
			}
			return true
		})

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isSelector(call.Fun, "ci", "Generate") || len(call.Args) == 0 {
				return true
			}
			runner := call.Args[len(call.Args)-1]
			switch arg := runner.(type) {
			case *ast.CallExpr:
				if isSelector(arg.Fun, "ci", "RunnerFor") {
					return true
				}
			case *ast.Ident:
				if derived[arg.Name] {
					return true
				}
			}
			offenders = append(offenders, fmt.Sprintf("%s:%d", path, fset.Position(runner.Pos()).Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these hand ci.Generate a runner not taken from ci.RunnerFor: %v", offenders)
	}
}

// isSelector reports whether an expression is the qualified name pkg.name.
func isSelector(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// The measured case. A provisioning suite lived in a custom section with its
// own runs-on, which the generator preserves verbatim and never rewrites. It
// stayed on the hosted image while the three generated jobs moved, and a
// refused job reports zero steps rather than a failure — so the run read green
// apart from one job nobody looks at.
func TestACustomSectionOnAnotherRunnerIsNamedWithItsJob(t *testing.T) {
	workflow := "jobs:\n\n  go:\n    runs-on: [self-hosted, private-ci]\n" +
		"    steps:\n      - run: go test ./...\n\n" +
		"# > custom:after:all - the deploy suite\n  pyinfra:\n    runs-on: ubuntu-latest\n" +
		"    steps:\n      - run: pytest\n"

	foreign := ForeignRunners(workflow, SelfHosted)
	if len(foreign) != 1 {
		t.Fatalf("foreign = %v, want just the custom job", foreign)
	}
	if !strings.Contains(foreign[0], "pyinfra") || !strings.Contains(foreign[0], "ubuntu-latest") {
		t.Errorf("foreign = %q, want the job name and the runner it names", foreign[0])
	}
}

// The generated jobs are the ones the runner argument wrote, so none of them
// can be foreign. A finding on every job would be noise on every repo.
func TestAWorkflowEntirelyOnItsOwnRunnerNamesNothing(t *testing.T) {
	for _, runner := range []Runner{Hosted, SelfHosted} {
		workflow, err := Generate(os.DirFS("blocks"), testManifest(t),
			comps("go", "api", "vue", "web"), nil, false, runner)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if foreign := ForeignRunners(workflow, runner); len(foreign) != 0 {
			t.Errorf("%s: foreign = %v, want none", runner, foreign)
		}
	}
}

// The stamp is what answers "is this repo current", and a generated file
// without one reads as hand-written to every die that checks.
func TestTheLintConfigCarriesTheGeneratedStamp(t *testing.T) {
	if !strings.HasPrefix(ActionlintConfig(42), "# forge-toolchain: 42\n") {
		t.Errorf("stamp missing or wrong: %q", strings.SplitN(ActionlintConfig(42), "\n", 2)[0])
	}
}
