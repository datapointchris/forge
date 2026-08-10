package dies

import (
	"maps"
	"testing"

	"github.com/datapointchris/forge/v7/reconcile"
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

// driftedDirectory is the same drift in a target git does not version, so the
// properties below cover the guarded path as well as the guarded-against one.
// A die that returns early must leave the sandbox exactly as untouched as one
// that measured it.
func driftedDirectory(t *testing.T) reconcile.Target {
	t.Helper()
	return unversionedFixture(t, stacks("python"), map[string]string{
		"README.md":      "# fixture\n",
		"pyproject.toml": "[project]\nname = \"fixture\"\n",
	})
}

// shapes are the two kinds of target forge reconciles. Every property holds for
// both, and the anti-vacuity check is asserted per shape rather than across
// them, so a shape where nothing is measured cannot hide behind the other.
func shapes() map[string]func(*testing.T) reconcile.Target {
	return map[string]func(*testing.T) reconcile.Target{
		"repo":      driftedFixture,
		"directory": driftedDirectory,
	}
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
	for shape, build := range shapes() {
		t.Run(shape, func(t *testing.T) {
			var found int

			for _, die := range Builtin() {
				t.Run(die.Name(), func(t *testing.T) {
					target := build(t)
					// The whole sandbox, not just the repo: planning writes into the
					// sync base, so a property scoped to the repo would miss it.
					before := snapshot(t, sandbox(target))

					measured := reconcile.Assess(target, die)
					found += len(measured.Changes)

					if after := snapshot(t, sandbox(target)); !maps.Equal(before, after) {
						t.Errorf("%s wrote to the repo during a read — the exact failure this package exists to prevent:\nbefore %v\nafter  %v",
							die.Name(), keysOf(before), keysOf(after))
					}
				})
			}

			// Without this the suite passes just as well on a fixture nothing looks
			// at, and the property above would be asserting nothing.
			if found == 0 {
				t.Errorf("no die found anything in the drifted %s — the property above is vacuous", shape)
			}
		})
	}
}

// TestEveryChangeIsClassified guards the fold: Sift routes on Verdict and
// Repair, so a Change left at its zero value is neither pending, attention nor
// unmeasured. It would vanish from plan and check both, which is the one way a
// die can report drift that nothing ever shows.
func TestEveryChangeIsClassified(t *testing.T) {
	for shape, build := range shapes() {
		t.Run(shape, func(t *testing.T) {
			for _, die := range Builtin() {
				t.Run(die.Name(), func(t *testing.T) {
					for _, change := range reconcile.Assess(build(t), die).Changes {
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
		})
	}
}

// TestGitDiesFindNothingInAnUnversionedDirectory is the guard on the guards.
//
// Each of these asserts something only a git repo can have. ci is the one that
// would do damage rather than waste a subprocess: a directory declaring python
// and shell would otherwise grow a .github/workflows/validate.yml that nothing
// will ever run. markdownlintignore is here for a subtler reason — its only
// entry protects a file semantic-release regenerates, and a target with no
// commits has no release to be seeded before.
func TestGitDiesFindNothingInAnUnversionedDirectory(t *testing.T) {
	gitOnly := []string{
		"ci", "planning", "branch-protection", "default-branch", "merge-settings",
		"markdownlintignore",
	}

	for _, name := range gitOnly {
		t.Run(name, func(t *testing.T) {
			die, err := Named(name)
			if err != nil {
				t.Fatalf("%s is not registered: %s", name, err)
			}

			measured := reconcile.Assess(driftedDirectory(t), die)
			if measured.Refusal != "" {
				t.Fatalf("%s refused rather than reporting not-applicable: %s", name, measured.Refusal)
			}
			if len(measured.Changes) != 0 {
				t.Errorf("%s found %d changes in a directory git does not version: %+v",
					name, len(measured.Changes), measured.Changes)
			}
			// The row has to say why, or a skipped die is indistinguishable from
			// a converged one.
			if measured.Summary == "" {
				t.Errorf("%s reported no reason for finding nothing", name)
			}
		})
	}
}

// The same fixture must still be measured by the dies that do apply, or the
// test above is passing because nothing ran at all.
func TestFilesystemDiesStillMeasureAnUnversionedDirectory(t *testing.T) {
	for _, name := range []string{"gitignore", "claude-md", "pyproject", "precommit"} {
		t.Run(name, func(t *testing.T) {
			if name == "pyproject" {
				requireUV(t)
			}
			die, err := Named(name)
			if err != nil {
				t.Fatalf("%s is not registered: %s", name, err)
			}
			measured := reconcile.Assess(driftedDirectory(t), die)
			// Asserted before the count, because a die that could not run
			// reports zero changes and would otherwise read as one that ran and
			// found nothing — the wrong diagnosis, on a machine missing a tool.
			if measured.Refusal != "" {
				t.Fatalf("%s could not measure the directory: %s", name, measured.Refusal)
			}
			if len(measured.Changes) == 0 {
				t.Errorf("%s found nothing in a drifted directory, so it is not covering one", name)
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
