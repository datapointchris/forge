package dies

import (
	"strings"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

// spacedWorkflow separates its top-level keys with a blank line, the way most of
// the portfolio's release workflows are written.
const spacedWorkflow = `name: Release

on:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  version:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
`

// compactWorkflow runs its top-level keys together, which several repos do.
const compactWorkflow = `name: Release
on:
  push:
    branches: [main]
permissions:
  contents: write
jobs:
  release:
    runs-on: ubuntu-latest
`

func releaseFixture(t *testing.T, workflow string) reconcile.Target {
	t.Helper()
	if workflow == "" {
		return fixture(t, stacks("go"), nil)
	}
	return fixture(t, stacks("go"), map[string]string{releaseWorkflowPath: workflow})
}

func TestReleaseAddsTheConcurrencyGroupAboveJobs(t *testing.T) {
	target := releaseFixture(t, spacedWorkflow)

	applyAll(t, target, Release{})

	written := readFile(t, target.Path(releaseWorkflowPath))
	if !strings.Contains(written, "concurrency:\n  group: release-${{ github.ref }}\n  cancel-in-progress: false") {
		t.Errorf("the block did not land intact:\n%s", written)
	}
	body := splitLines(written)
	if indexOf(body, "concurrency:") > indexOf(body, "jobs:") {
		t.Error("the group landed below jobs, where it is a job key rather than a workflow one")
	}
}

// The whole point of asserting presence rather than generating the file: what
// the repo already wrote has to come through untouched.
func TestReleaseLeavesTheRestOfTheWorkflowAlone(t *testing.T) {
	target := releaseFixture(t, spacedWorkflow)

	applyAll(t, target, Release{})

	written := readFile(t, target.Path(releaseWorkflowPath))
	for _, line := range splitLines(strings.TrimRight(spacedWorkflow, "\n")) {
		if !strings.Contains(written, line) {
			t.Errorf("lost a line the repo wrote: %q", line)
		}
	}
}

// Half the portfolio spaces its top-level keys and half does not. A die that
// imposed its own would show a whitespace change in every diff.
func TestReleaseMatchesTheFilesOwnSpacing(t *testing.T) {
	spaced := releaseFixture(t, spacedWorkflow)
	compact := releaseFixture(t, compactWorkflow)

	applyAll(t, spaced, Release{})
	applyAll(t, compact, Release{})

	spacedBody := splitLines(readFile(t, spaced.Path(releaseWorkflowPath)))
	if spacedBody[indexOf(spacedBody, "jobs:")-1] != "" {
		t.Error("a spaced workflow lost the blank line above jobs")
	}
	compactBody := splitLines(readFile(t, compact.Path(releaseWorkflowPath)))
	if compactBody[indexOf(compactBody, "jobs:")-1] == "" {
		t.Error("a compact workflow gained a blank line it does not use elsewhere")
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	target := releaseFixture(t, spacedWorkflow)

	applyAll(t, target, Release{})
	once := readFile(t, target.Path(releaseWorkflowPath))
	outcomes := applyAll(t, target, Release{})

	if readFile(t, target.Path(releaseWorkflowPath)) != once {
		t.Error("a second apply changed the file again")
	}
	for _, outcome := range outcomes {
		if outcome.Status == reconcile.Done {
			t.Errorf("a second apply still had work to do: %s", outcome.Change.Item)
		}
	}
}

// `on` is a YAML 1.1 boolean and yaml.v3 resolves it as one, so a decode into a
// map loses every trigger. Walking nodes reads the literal key instead.
func TestReleaseReadsTheOnKeyThatYAMLResolvesToTrue(t *testing.T) {
	tagged := `name: Release
on:
  push:
    tags:
      - "v*"
jobs:
  release:
    runs-on: ubuntu-latest
`
	changes := assess(t, releaseFixture(t, tagged))

	if !hasChangeFor(changes, "on.push.tags") {
		t.Error("a workflow triggered on a pushed tag was not reported")
	}
}

// Inverting a trigger changes when the whole workflow runs, and a repo tagging
// by hand may have a build step that only makes sense once the tag exists.
func TestReleaseReportsATagTriggerRatherThanRewritingIt(t *testing.T) {
	tagged := `name: Release
on:
  push:
    tags:
      - "v*"
jobs:
  release:
    runs-on: ubuntu-latest
`
	target := releaseFixture(t, tagged)
	before := readFile(t, target.Path(releaseWorkflowPath))

	for _, change := range assess(t, target) {
		if change.Item == "on.push.tags" && change.Repair != reconcile.ByHand {
			t.Errorf("a tag trigger was marked %s, so apply would rewrite it", change.Repair)
		}
	}
	applyAll(t, target, Release{})

	if !strings.Contains(readFile(t, target.Path(releaseWorkflowPath)), `tags:`) {
		t.Errorf("apply removed the trigger:\n%s", before)
	}
}

// A branch-triggered workflow must not be reported as tag-triggered, or every
// repo in the portfolio gains a finding only a person can clear.
func TestReleaseDoesNotReadBranchesAsTags(t *testing.T) {
	if hasChangeFor(assess(t, releaseFixture(t, spacedWorkflow)), "on.push.tags") {
		t.Error("a branch trigger was reported as a tag trigger")
	}
}

// Whether a repo releases at all is the repo's to say, and inventing a workflow
// would tag something nobody asked to publish.
func TestReleaseSaysNothingAboutARepoWithNoWorkflow(t *testing.T) {
	target := releaseFixture(t, "")

	if changes := assess(t, target); len(changes) != 0 {
		t.Errorf("a repo with no release workflow drew %d changes", len(changes))
	}
	if _, err := readLineFile(target.Path(releaseWorkflowPath)); err != nil {
		t.Fatalf("reading back: %s", err)
	}
	if readLineFileExists(t, target.Path(releaseWorkflowPath)) {
		t.Error("the die created a release workflow")
	}
}

// Unverified is not permission: a workflow that cannot be parsed has not been
// measured, so it must not fall through into "nothing to do".
func TestReleaseReportsAnUnreadableWorkflowAsUnknown(t *testing.T) {
	changes := assess(t, releaseFixture(t, "name: Release\n  bad: [indent\n"))

	if len(changes) != 1 || changes[0].Verdict != reconcile.Unknown {
		t.Errorf("expected one unknown, got %+v", changes)
	}
	if changes[0].Repair != reconcile.NoRepair {
		t.Errorf("an unreadable workflow was marked repairable: %s", changes[0].Repair)
	}
}

// Appending would land the key inside whatever block ends the file, where it is
// a step or a job setting rather than a workflow one.
func TestReleaseRefusesWhenThereIsNoTopLevelJobsKey(t *testing.T) {
	changes := assess(t, releaseFixture(t, "name: Release\non:\n  push:\n    branches: [main]\n"))

	if len(changes) != 1 || changes[0].Repair != reconcile.ByHand {
		t.Errorf("expected one by-hand change, got %+v", changes)
	}
}

func TestReleasePlanCarriesThePatchItWouldApply(t *testing.T) {
	changes := assess(t, releaseFixture(t, spacedWorkflow))

	if len(changes) != 1 {
		t.Fatalf("expected one change, got %+v", changes)
	}
	if !strings.Contains(changes[0].Patch, "+concurrency:") {
		t.Errorf("the plan did not show the added key:\n%s", changes[0].Patch)
	}
}

func assess(t *testing.T, target reconcile.Target) []reconcile.Change {
	t.Helper()
	result := reconcile.Assess(target, Release{})
	return result.Changes
}

func hasChangeFor(changes []reconcile.Change, item string) bool {
	for _, change := range changes {
		if change.Item == item {
			return true
		}
	}
	return false
}

func indexOf(body []string, want string) int {
	for i, line := range body {
		if line == want {
			return i
		}
	}
	return -1
}

func readLineFileExists(t *testing.T, path string) bool {
	t.Helper()
	file, err := readLineFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}
	return file.exists
}
