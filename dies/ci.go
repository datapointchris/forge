package dies

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/reconcile"
)

// CI generates .github/workflows/validate.yml from the standard blocks.
//
// One job per declared component, so a repo's separate modules validate in
// parallel and a failure names which one broke. Repos with elaborate pipelines
// keep those as their own workflow files; this one is additive, and callable
// via workflow_call so a release workflow can gate on it.
//
// validate.yml, deliberately not ci.yml: several repos hand-wrote a ci.yml long
// before this existed, and generating over one would destroy work nothing could
// recover.
//
// Absorbs the CI half of can-generate, which existed only because the generator
// could not be run across the portfolio. It can now, so the pre-rollout gate is
// simply `forge repos check ci` — same coverage, no separate die.
type CI struct{}

func (CI) Name() string { return "ci" }

func (CI) Description() string {
	return "Generate .github/workflows/validate.yml from the standard CI blocks, one job per declared component. Reports a hand-written pipeline rather than overwriting it."
}

func (CI) Tags() []string { return []string{"ci", "actions", "standardization", "golden-path"} }

type ciState struct {
	applicable bool
	reason     string
	workflow   generatedFile
	blockers   []reconcile.Change
}

func (s ciState) Summary() string {
	if !s.applicable {
		return s.reason
	}
	return "validate.yml current"
}

func (CI) Observe(t reconcile.Target) (reconcile.Observation, error) {
	// Checked before the components are, because a maintained directory declares
	// components too. Without this, a directory declaring python and shell grows
	// a .github/workflows/validate.yml that nothing will ever run — the one
	// guard here that prevents a write rather than a wasted read.
	if !t.Versioned() {
		return ciState{reason: "not a git repo, so no workflow would run"}, nil
	}
	if t.Repo.Toolchain == nil || len(t.Repo.Toolchain.Components) == 0 {
		return ciState{reason: "declares no toolchain components"}, nil
	}

	root := t.Repo.Path
	blocksFS, err := subFS(t.Assets.CI, ".")
	if err != nil {
		return nil, err
	}

	// The existing file is read for its custom sections only when it is one of
	// ours; a hand-written file contributes nothing and is reported instead.
	var existing string
	if data, err := os.ReadFile(filepath.Join(root, ci.WorkflowPath)); err == nil && !handWritten(root, ci.WorkflowPath) {
		existing = string(data)
	}

	wanted, err := ci.Generate(blocksFS, t.Assets.Manifest, t.Repo.Toolchain.Components,
		precommit.ExtractCustomSections(existing), ci.ReleaseGatesOnValidate(root))
	if err != nil {
		return ciState{reason: "no components with a CI block"}, nil
	}

	state := ciState{applicable: true, workflow: readGenerated(root, ci.WorkflowPath, wanted)}

	if handWritten(root, ci.WorkflowPath) {
		state.blockers = append(state.blockers, blocker(ci.WorkflowPath,
			"exists without the "+toolchainStamp+" stamp, so it was hand-written and will not be overwritten"))
	}
	// Reported, not a problem: a bespoke pipeline is a repo's own, and the
	// generated workflow is additive beside it. Worth saying because the two
	// can duplicate jobs.
	if handWritten(root, ".github/workflows/ci.yml") {
		state.blockers = append(state.blockers, blocker(".github/workflows/ci.yml",
			"a hand-written pipeline sits beside the generated one — reconcile them before relying on either"))
	}

	if unexpandedPlaceholder(wanted) {
		state.blockers = append(state.blockers, blocker("generated workflow",
			"a generator placeholder survived rendering — forge would write an invalid workflow"))
	}
	if finding := lintWorkflow(wanted); finding != "" {
		state.blockers = append(state.blockers, blocker("generated workflow", "actionlint: "+finding))
	}

	return state, nil
}

// lintWorkflow runs actionlint against the generated content in a throwaway
// repo. A schema-valid workflow can still be rejected at runtime, and this is
// the check `pre-commit validate-config`'s equivalent cannot make.
func lintWorkflow(workflow string) string {
	dir, err := os.MkdirTemp("", "forge-actionlint-")
	if err != nil {
		return ""
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, ".github", "workflows", "validate.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(path, []byte(workflow), 0o644); err != nil {
		return ""
	}
	// actionlint wants a repo; without one it reports the absence rather than
	// the workflow.
	_, _ = runIn(dir, "git", "init", "-q")

	return runValidator(dir, "actionlint", ".github/workflows/validate.yml")
}

func (CI) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(ciState)
	if !ok {
		return nil, fmt.Errorf("ci: unexpected observation %T", observed)
	}
	if !state.applicable {
		return nil, nil
	}

	changes := state.blockers
	// A hand-written workflow is not drift to repair — it is a file forge
	// refuses to touch — so the automatic change is suppressed while one is
	// there, or the plan would promise a write that Perform would refuse.
	if handWrittenBlocked(state.blockers) {
		return changes, nil
	}
	if change, drifted := state.workflow.change("regenerate from the standard CI blocks"); drifted {
		changes = append(changes, change)
	}
	return changes, nil
}

func handWrittenBlocked(blockers []reconcile.Change) bool {
	for _, blocker := range blockers {
		if blocker.Item == ci.WorkflowPath {
			return true
		}
	}
	return false
}

func (c CI) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := c.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(ciState)

	if handWrittenBlocked(state.blockers) {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "a hand-written workflow appeared since the plan"}, nil
	}
	if state.workflow.matches() {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	}

	if err := writeGenerated(t.Repo.Path, state.workflow); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "wrote " + ci.WorkflowPath}, nil
}
