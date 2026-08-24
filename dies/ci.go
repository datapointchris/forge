package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/reconcile"
	"github.com/datapointchris/forge/toolchain"
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

// ciFile is one file this die owns, with the sentence a plan shows for it.
//
// The reasons travel with the file rather than sitting in the branch that emits
// it, because Diff walks the files in one loop and a switch on the path to
// recover a sentence is the dispatch the loop exists to remove.
type ciFile struct {
	generatedFile
	// write is what a plan says when the file is missing or stale.
	write string
	// retract is what a plan says when forge is removing one it wrote.
	retract string
}

type ciState struct {
	applicable bool
	reason     string
	// files are every file this die owns in this repo, in the order they must
	// be written. A file the repo is not owed still appears, carrying an empty
	// want — that is what makes a retraction expressible, and what stops a path
	// going unobserved because the repo stopped qualifying for it.
	files []ciFile
	// selfHosted records which runner the workflow names, for the row a
	// converged repo shows.
	selfHosted bool
	blockers   []reconcile.Change
}

func (s ciState) Summary() string {
	if !s.applicable {
		return s.reason
	}
	if s.selfHosted {
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

	// Both paths are read whatever the runner is. Scoping the read to the
	// runner is how a repo that stopped being private kept its lint config
	// while every verb reported converged — a path nothing observes cannot
	// drift, because nothing is measuring it.
	lintConfig := lintConfigFor(runner, t.Assets.Manifest.Version)
	state := ciState{
		applicable: true,
		selfHosted: runner == ci.SelfHosted,
		files: []ciFile{
			{
				generatedFile: readGenerated(root, ci.ActionlintConfigPath, lintConfig),
				write:         "declare the self-hosted runner label to actionlint",
				retract:       "this repo is no longer private, so the self-hosted label declaration comes out",
			},
			{
				generatedFile: readGenerated(root, ci.WorkflowPath, wanted),
				write:         "regenerate from the standard CI blocks",
			},
		},
	}

	for _, file := range state.files {
		if !handWritten(root, file.rel) {
			continue
		}
		detail := "exists without the " + toolchainStamp +
			" stamp, so it was hand-written and will not be overwritten"
		if !file.wanted() {
			detail = "exists without the " + toolchainStamp +
				" stamp, so forge did not write it and will not remove it"
		}
		state.blockers = append(state.blockers, blocker(file.rel, detail))
	}

	// actionlint reads both spellings and prefers the one forge writes, so a
	// generated .yaml silently overrides a hand-written .yml rather than being
	// refused by it. The blocker is filed against the path forge would write,
	// which is what suppresses that write; the detail names the file that is
	// actually there.
	if handWritten(root, ci.ActionlintConfigAltPath) {
		state.blockers = append(state.blockers, blocker(ci.ActionlintConfigPath,
			"a hand-written "+ci.ActionlintConfigAltPath+" is already there, and actionlint prefers "+
				ci.ActionlintConfigPath+" — writing it would override that file without reporting it"))
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
	if finding := lintWorkflow(wanted, lintConfig); finding != "" {
		state.blockers = append(state.blockers, actionlintFinding(finding, t.Assets.Manifest))
	}

	return state, nil
}

// lintConfigFor is the actionlint configuration a repo on this runner is owed.
//
// A value rather than a branch inside Observe, so a test can assert the choice
// directly. Hoisting the write out of that branch used to pass the whole suite,
// because the only assertion was that lintWorkflow honors the argument it is
// handed rather than that Observe picks the right one.
func lintConfigFor(runner ci.Runner, stampVersion int) string {
	if runner != ci.SelfHosted {
		return ""
	}
	return ci.ActionlintConfig(stampVersion)
}

// pinnedActionlint is the version the declaration pins the actionlint hook to,
// or "" when the manifest does not name it.
func pinnedActionlint(manifest *toolchain.Toolchain) string {
	if manifest == nil {
		return ""
	}
	for _, hook := range manifest.Hooks {
		if hook.Repo == ci.ActionlintHookRepo {
			return hook.Rev
		}
	}
	return ""
}

// installedActionlint is the version on PATH, or "" when it cannot be read.
//
// actionlint prints `1.7.7` where the hook pins `v1.7.7`, so the leading v is
// trimmed from both sides before they are compared.
func installedActionlint() string {
	out, err := runIn(os.TempDir(), "actionlint", "--version")
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(out, "\n")
	return strings.TrimPrefix(strings.TrimSpace(line), "v")
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
// The config handed in is the one the repo is owed, so this lints what would be
// written rather than a guess. On a public repo it is "", and both halves are
// then empty: that repo's generated workflow cannot name the pool, so there is
// nothing here for a declaration to permit. The check a public repo keeps is
// the file's absence in the repo itself — with no actionlint config on disk, a
// hand-written workflow naming the pool fails that repo's own actionlint hook.
//
// The finding is returned raw. Whether forge can stand behind it depends on
// which actionlint answered, and that is the caller's decision rather than a
// property of the lint itself.
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

// actionlintFinding renders a lint finding as the Change a plan shows.
//
// A finding from an actionlint that is not the pinned one is Unknown rather
// than a blocker. This gate's whole job is to predict what a repo's own
// actionlint hook will say about one label, and a different binary answers a
// different question — so the honest verdict is that forge could not tell.
// Unknown never moves the exit code, which is what keeps an unpinned local
// install from failing `forge repos check` across the portfolio.
func actionlintFinding(finding string, manifest *toolchain.Toolchain) reconcile.Change {
	pinned := strings.TrimPrefix(pinnedActionlint(manifest), "v")
	installed := installedActionlint()
	if pinned != "" && installed != "" && installed != pinned {
		return reconcile.Change{
			Item:    "generated workflow",
			Verdict: reconcile.Unknown,
			Repair:  reconcile.NoRepair,
			Detail: "actionlint " + installed + " is installed and the declaration pins " + pinned +
				", so this is not what the repo's own hook will say: " + finding,
		}
	}
	return blocker("generated workflow", "actionlint: "+finding)
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

	// Writes first, then retractions, and within the writes the lint
	// declaration before the workflow that names the label. reconcile.Apply
	// performs them in this order, so a run interrupted between two of them has
	// to leave a repo that still builds. A declaration for a label nothing uses
	// is inert; a workflow naming a label nothing declares fails that repo's
	// actionlint hook on every commit until the second file lands.
	//
	// A hand-written file is not drift to repair — it is a file forge refuses
	// to touch — so its change is suppressed while the blocker is there, or the
	// plan would promise a write Perform would refuse. Asked per file: a
	// hand-written lint config is no reason to stop regenerating the workflow.
	for _, file := range state.files {
		if file.retracting() || hasItem(state.blockers, file.rel) {
			continue
		}
		if change, drifted := file.change(file.write); drifted {
			changes = append(changes, change)
		}
	}
	for _, file := range state.files {
		if !file.retracting() || hasItem(state.blockers, file.rel) {
			continue
		}
		if change, drifted := file.change(file.retract); drifted {
			changes = append(changes, change)
		}
	}
	return changes, nil
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
	var file ciFile
	var found bool
	for _, candidate := range state.files {
		if candidate.rel == change.Item {
			file, found = candidate, true
			break
		}
	}
	if !found {
		return reconcile.Outcome{
			Change: change, Status: reconcile.Refused,
			Message: "the ci die writes no " + change.Item,
		}, nil
	}

	if hasItem(state.blockers, file.rel) {
		return reconcile.Outcome{
			Change: change, Status: reconcile.Refused,
			Message: "a hand-written " + file.rel + " appeared since the plan",
		}, nil
	}

	// A retraction is checked before wanted(), because an empty want is exactly
	// what a retraction has. Reading it as "no longer wanted since the plan"
	// would refuse every removal this die ever emits.
	if file.retracting() {
		if err := removeGenerated(t.Repo.Path, file.generatedFile); err != nil {
			return reconcile.Outcome{}, err
		}
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "removed " + file.rel}, nil
	}
	if !file.wanted() {
		return reconcile.Outcome{
			Change: change, Status: reconcile.Refused,
			Message: "the repo stopped wanting " + change.Item + " since the plan",
		}, nil
	}
	if file.matches() {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	}

	if err := writeGenerated(t.Repo.Path, file.generatedFile); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "wrote " + file.rel}, nil
}
