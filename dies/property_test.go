package dies

import (
	"maps"
	"testing"

	"github.com/datapointchris/forge/v5/reconcile"
)

// driftedFixture is a repo missing everything the standards assert, so every
// die has something to find and the no-writes property is tested against a die
// that is actually doing work rather than one bailing out early.
func driftedFixture(t *testing.T) reconcile.Target {
	t.Helper()
	return fixture(t, stacks("python"), map[string]string{
		"README.md":      "# fixture\n",
		"pyproject.toml": "[project]\nname = \"fixture\"\n",
	})
}

// TestReadVerbsWriteNothing is the property that justifies this package.
//
// The old mechanism opted in per die with `supports_check: true` and verified
// nothing, so a die that ignored FORGE_CHECK would write to every repo while
// the operator believed they were previewing. Observe and Diff are the whole of
// plan and check, and this asserts across every registered die that neither
// touches the repo — which makes a new die safe by default rather than safe by
// declaration.
func TestReadVerbsWriteNothing(t *testing.T) {
	var found int

	for _, die := range Builtin() {
		t.Run(die.Name(), func(t *testing.T) {
			target := driftedFixture(t)
			before := snapshot(t, target.Repo.Path)

			measured := reconcile.Assess(target, die)
			found += len(measured.Changes)

			if after := snapshot(t, target.Repo.Path); !maps.Equal(before, after) {
				t.Errorf("%s wrote to the repo during a read — the exact failure this package exists to prevent:\nbefore %v\nafter  %v",
					die.Name(), keysOf(before), keysOf(after))
			}
		})
	}

	// Without this the suite passes just as well on a fixture nothing looks at,
	// and the property above would be asserting nothing.
	if found == 0 {
		t.Error("no die found anything in the drifted fixture — the property above is vacuous")
	}
}

// TestEveryChangeIsClassified guards the fold: Sift routes on Verdict and
// Repair, so a Change left at its zero value is neither pending, attention nor
// unmeasured. It would vanish from plan and check both, which is the one way a
// die can report drift that nothing ever shows.
func TestEveryChangeIsClassified(t *testing.T) {
	for _, die := range Builtin() {
		t.Run(die.Name(), func(t *testing.T) {
			for _, change := range reconcile.Assess(driftedFixture(t), die).Changes {
				if change.Item == "" {
					t.Errorf("%s emitted a change with no item: %+v", die.Name(), change)
				}
				if change.Verdict == "" {
					t.Errorf("%s: change %q has no verdict", die.Name(), change.Item)
				}
				if change.Repair == "" {
					t.Errorf("%s: change %q has no repair", die.Name(), change.Item)
				}
			}
		})
	}
}

// TestEveryDieIsAddressable keeps the registry and the CLI in step: a die that
// Named cannot resolve is unreachable from every verb that takes one.
func TestEveryDieIsAddressable(t *testing.T) {
	for _, die := range Builtin() {
		found, err := Named(die.Name())
		if err != nil {
			t.Errorf("%s is registered but not addressable: %s", die.Name(), err)
			continue
		}
		if found.Name() != die.Name() {
			t.Errorf("Named(%q) returned %q", die.Name(), found.Name())
		}
		if die.Description() == "" {
			t.Errorf("%s has no description, so `forge dies list` shows a blank row", die.Name())
		}
	}
}

func keysOf(tree map[string]string) []string {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	return names
}
