package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// actionlint declares the self-hosted label to the repo's own actionlint
	// hook, and is wanted only where the workflow above names that label. It
	// travels with the workflow rather than with the other tool configs
	// because one die deciding both is what stops them disagreeing: a repo
	// cannot be given the label in its workflow and not in its lint config.
	actionlint generatedFile
	blockers   []reconcile.Change
}

func (s ciState) Summary() string {
	if !s.applicable {
		return s.reason
	}
	if s.actionlint.wanted() {
		return "validate.yml current, on the self-hosted runner"
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

	runner := ci.RunnerFor(t.Repo.IsPrivate())
	wanted, err := ci.Generate(blocksFS, t.Assets.Manifest, t.Repo.Toolchain.Components,
		precommit.ExtractCustomSections(existing), ci.ReleaseGatesOnValidate(root), runner)
	if err != nil {
		return ciState{reason: "no components with a CI block"}, nil
	}

	state := ciState{applicable: true, workflow: readGenerated(root, ci.WorkflowPath, wanted)}

	// The lint config is owed to exactly the repos whose workflow names the
	// pool label, so it is derived from the same runner choice rather than
	// from visibility a second time.
	var actionlintConfig string
	if runner == ci.SelfHosted {
		actionlintConfig = ci.ActionlintConfig(t.Assets.Manifest.Version)
		state.actionlint = readGenerated(root, ci.ActionlintConfigPath, actionlintConfig)
	}

	if handWritten(root, ci.WorkflowPath) {
		state.blockers = append(state.blockers, blocker(ci.WorkflowPath,
			"exists without the "+toolchainStamp+" stamp, so it was hand-written and will not be overwritten"))
	}
	if state.actionlint.wanted() && handWritten(root, ci.ActionlintConfigPath) {
		state.blockers = append(state.blockers, blocker(ci.ActionlintConfigPath,
			"exists without the "+toolchainStamp+" stamp, so it was hand-written and will not be overwritten"))
	}
	// Reported, not a problem: a bespoke pipeline is a repo's own, and the
	// generated workflow is additive beside it. Worth saying because the two
	// can duplicate jobs.
	if handWritten(root, ".github/workflows/ci.yml") {
		state.blockers = append(state.blockers, blocker(".github/workflows/ci.yml",
			"a hand-written pipeline sits beside the generated one — reconcile them before relying on either"))
	}

	// Only where the repo takes the self-hosted runner. A public repo's custom
	// section naming something else is that section's business, and a hosted
	// job there runs.
	if runner == ci.SelfHosted {
		if foreign := ci.ForeignRunners(wanted, runner); len(foreign) > 0 {
			state.blockers = append(state.blockers, blocker(ci.WorkflowPath,
				"a custom section runs on a runner this repo's jobs are refused on, and a refused job "+
					"reports zero steps rather than a failure: "+strings.Join(foreign, ", ")))
		}
	}

	if unexpandedPlaceholder(wanted) {
		state.blockers = append(state.blockers, blocker("generated workflow",
			"a generator placeholder survived rendering — forge would write an invalid workflow"))
	}
	if finding := lintWorkflow(wanted, actionlintConfig); finding != "" {
		state.blockers = append(state.blockers, blocker("generated workflow", "actionlint: "+finding))
	}

	return state, nil
}

// lintWorkflow runs actionlint against the generated content in a throwaway
// repo. A schema-valid workflow can still be rejected at runtime, and this is
// the check `pre-commit validate-config`'s equivalent cannot make.
//
// config is the repo's actionlint configuration, written into the throwaway
// repo alongside the workflow. Nothing a repo carries on disk reaches this
// directory, so without it a workflow naming the self-hosted pool is rejected
// here as an unknown label — before plan can show the diff or apply can write
// it, and for every private repo at once.
//
// Passed in rather than written unconditionally, and that is the safety half.
// A public repo hands "" and its lint keeps refusing the pool label, which is
// the check that catches a generated workflow pointing at the homelab network
// from a repo a fork can open a pull request against.
func lintWorkflow(workflow, config string) string {
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
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, ci.ActionlintConfigPath), []byte(config), 0o644); err != nil {
			return ""
		}
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
	// A hand-written file is not drift to repair — it is a file forge refuses
	// to touch — so the automatic change is suppressed while one is there, or
	// the plan would promise a write that Perform would refuse. Asked per file:
	// a hand-written actionlint config is no reason to stop regenerating the
	// workflow, and the reverse holds too.
	if !handWrittenBlocked(state.blockers, ci.WorkflowPath) {
		if change, drifted := state.workflow.change("regenerate from the standard CI blocks"); drifted {
			changes = append(changes, change)
		}
	}
	if state.actionlint.wanted() && !handWrittenBlocked(state.blockers, ci.ActionlintConfigPath) {
		if change, drifted := state.actionlint.change("declare the self-hosted runner label to actionlint"); drifted {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

func handWrittenBlocked(blockers []reconcile.Change, rel string) bool {
	for _, blocker := range blockers {
		if blocker.Item == rel {
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

	// The plan names which file this change is for, and Perform re-observes
	// rather than trusting it. Writing whichever file the die happens to hold
	// first would put the workflow at the actionlint config's path.
	var file generatedFile
	switch change.Item {
	case ci.WorkflowPath:
		file = state.workflow
	case ci.ActionlintConfigPath:
		file = state.actionlint
	default:
		return reconcile.Outcome{
			Change: change, Status: reconcile.Refused,
			Message: "the ci die writes no " + change.Item,
		}, nil
	}

	if !file.wanted() {
		return reconcile.Outcome{
			Change: change, Status: reconcile.Refused,
			Message: "the repo stopped wanting " + change.Item + " since the plan",
		}, nil
	}
	if handWrittenBlocked(state.blockers, file.rel) {
		return reconcile.Outcome{
			Change: change, Status: reconcile.Refused,
			Message: "a hand-written " + file.rel + " appeared since the plan",
		}, nil
	}
	if file.matches() {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	}

	if err := writeGenerated(t.Repo.Path, file); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "wrote " + file.rel}, nil
}
