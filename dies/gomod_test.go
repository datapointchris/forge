package dies

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
	"github.com/datapointchris/forge/toolchain"
)

// goTarget builds a repo with one Go component per directory and a manifest
// pinning the given runtime. An empty pin means the manifest declares none.
func goTarget(t *testing.T, pin string, mods map[string]string) reconcile.Target {
	t.Helper()
	dir := t.TempDir()

	var components []config.Component
	for rel, body := range mods {
		components = append(components, config.Component{Stack: "go", Dir: rel})
		at := filepath.Join(dir, rel)
		if err := os.MkdirAll(at, 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(at, "go.mod"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifest := &toolchain.Toolchain{Version: 15}
	if pin != "" {
		manifest.Runtimes = []toolchain.Runtime{{Name: "go", Version: pin}}
	}
	return reconcile.Target{
		Repo: config.Repo{
			Name:      "sample",
			Path:      dir,
			Toolchain: &config.Toolchain{Components: components},
		},
		Assets: reconcile.Assets{Manifest: manifest},
	}
}

func gomodChanges(t *testing.T, target reconcile.Target) []reconcile.Change {
	t.Helper()
	observed, err := GoMod{}.Observe(target)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := GoMod{}.Diff(target, observed)
	if err != nil {
		t.Fatal(err)
	}
	return changes
}

func TestAModuleBelowThePinGetsAToolchainDirective(t *testing.T) {
	target := goTarget(t, "1.26.6", map[string]string{".": "module x\n\ngo 1.26.5\n"})
	changes := gomodChanges(t, target)
	if len(changes) != 1 || changes[0].Verdict != reconcile.Missing {
		t.Fatalf("changes = %+v", changes)
	}

	if _, err := (GoMod{}).Perform(target, changes[0]); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(target.Repo.Path, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "module x\n\ngo 1.26.5\n\ntoolchain go1.26.6\n"; got != want {
		t.Errorf("go.mod =\n%q\nwant\n%q", got, want)
	}
}

func TestTheGoDirectiveIsNeverRewritten(t *testing.T) {
	// The whole reason this die exists. Raising the floor is what makes
	// `go install <tool>@latest` skip a release on a machine running older Go.
	target := goTarget(t, "1.26.6", map[string]string{".": "module x\n\ngo 1.26.5\n"})
	changes := gomodChanges(t, target)
	if _, err := (GoMod{}).Perform(target, changes[0]); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(target.Repo.Path, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "go 1.26.5") {
		t.Errorf("the go directive moved:\n%s", body)
	}
}

func TestAModuleAlreadyFlooredAtThePinIsLeftAlone(t *testing.T) {
	// It already builds with the fixed standard library, so a toolchain line
	// would be a second place for the same fact to live.
	target := goTarget(t, "1.26.6", map[string]string{".": "module x\n\ngo 1.26.6\n"})
	if changes := gomodChanges(t, target); len(changes) != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestAModuleFlooredAboveThePinIsLeftAlone(t *testing.T) {
	target := goTarget(t, "1.26.6", map[string]string{".": "module x\n\ngo 1.27.0\n"})
	if changes := gomodChanges(t, target); len(changes) != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestAToolchainBehindThePinIsRaised(t *testing.T) {
	target := goTarget(t, "1.26.6", map[string]string{
		".": "module x\n\ngo 1.26.5\n\ntoolchain go1.26.5\n",
	})
	changes := gomodChanges(t, target)
	if len(changes) != 1 || changes[0].Verdict != reconcile.Stale {
		t.Fatalf("changes = %+v", changes)
	}
	if _, err := (GoMod{}).Perform(target, changes[0]); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(target.Repo.Path, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "toolchain go1.26.6") {
		t.Errorf("toolchain not raised:\n%s", body)
	}
	if strings.Count(string(body), "toolchain") != 1 {
		t.Errorf("directive duplicated:\n%s", body)
	}
}

func TestATriadRepoIsItemizedPerModule(t *testing.T) {
	// One row saying "go.mod" could not say which of three modules drifted.
	target := goTarget(t, "1.26.6", map[string]string{
		"api": "module x/api\n\ngo 1.26.5\n",
		"cli": "module x/cli\n\ngo 1.26.5\n",
	})
	changes := gomodChanges(t, target)
	if len(changes) != 2 {
		t.Fatalf("changes = %+v", changes)
	}
	items := map[string]bool{}
	for _, c := range changes {
		items[c.Item] = true
	}
	for _, want := range []string{"api/go.mod", "cli/go.mod"} {
		if !items[want] {
			t.Errorf("no change for %s: %+v", want, changes)
		}
	}
}

func TestAManifestPinningNoGoRuntimeAssertsNothing(t *testing.T) {
	// A die with nothing to assert reports converged rather than inventing a
	// version to write.
	target := goTarget(t, "", map[string]string{".": "module x\n\ngo 1.26.5\n"})
	if changes := gomodChanges(t, target); len(changes) != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestADeclaredComponentWithNoGoModIsNotThisDiesToSettle(t *testing.T) {
	target := goTarget(t, "1.26.6", map[string]string{"api": ""})
	if changes := gomodChanges(t, target); len(changes) != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestARepoDeclaringNoGoComponentIsUntouched(t *testing.T) {
	target := goTarget(t, "1.26.6", nil)
	target.Repo.Toolchain = &config.Toolchain{
		Components: []config.Component{{Stack: "python", Dir: "."}},
	}
	if changes := gomodChanges(t, target); len(changes) != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestApplyingTwiceIsIdempotent(t *testing.T) {
	target := goTarget(t, "1.26.6", map[string]string{".": "module x\n\ngo 1.26.5\n"})
	changes := gomodChanges(t, target)
	if _, err := (GoMod{}).Perform(target, changes[0]); err != nil {
		t.Fatal(err)
	}
	if again := gomodChanges(t, target); len(again) != 0 {
		t.Errorf("second pass still sees drift: %+v", again)
	}
}

func TestAnUnparsableGoVersionGetsTheDirectiveRatherThanAPass(t *testing.T) {
	// Judging a go.mod current because its version could not be read is the
	// failure mode worth avoiding; writing a directive is recoverable.
	if meetsFloor("weird", "1.26.6") {
		t.Error("an unparsable version read as current")
	}
}

func TestVersionsCompareNumericallyRatherThanAsText(t *testing.T) {
	// go1.26.10 is above go1.26.6, and a string comparison says otherwise.
	if !meetsFloor("1.26.10", "1.26.6") {
		t.Error("1.26.10 read as below 1.26.6")
	}
	if meetsFloor("1.9.0", "1.26.0") {
		t.Error("1.9.0 read as above 1.26.0")
	}
}
