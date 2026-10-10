package dies

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/datapointchris/forge/ci"
	"github.com/datapointchris/forge/config"
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
// validate.yml, deliberately not ci.yml: ci.yml is the name a hand-written
// pipeline takes by default, and generating over one would destroy work
// nothing could recover.
//
// Absorbs the CI half of can-generate, which existed only because the generator
// could not be run across the portfolio. It can now, so the pre-rollout gate is
// simply `forge repos check ci` — same coverage, no separate die.
type CI struct{}

func (CI) Name() string { return "ci" }

func (CI) Description() string {
	return "Generate .github/workflows/validate.yml from the standard CI blocks: one job per declared component for what no hook checks, such as its tests, and one running every standard pre-commit hook. Reports a hand-written pipeline rather than overwriting it."
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
}

type ciState struct {
	applicable bool
	reason     string
	// files are every file this die owns in this repo, in the order they must
	// be written.
	files []ciFile
	// selfHosted records which runner the workflow names, for the row a
	// converged repo shows.
	selfHosted bool
	// repinned are the repo's own workflows, each wanting the declared pins
	// and nothing else changed.
	repinned []generatedFile
	blockers []reconcile.Change
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
	// Checked first, because a maintained directory declares components too.
	// Without this, a directory declaring python and shell grows a
	// .github/workflows/validate.yml that nothing will ever run.
	if !t.Versioned() {
		return ciState{reason: "not a git repo, so no workflow would run"}, nil
	}

	// A repo declaring no components still has hooks when forge maintains its
	// pre-commit config, and those are owed the hooks job like anyone's.
	// Generate answers ErrNoJobs where there is neither.
	preCommitConfig := committedPreCommit(t)
	var components []config.Component
	if t.Repo.Toolchain != nil {
		components = t.Repo.Toolchain.Components
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

	runner := ci.RunnerFor(t.Repo.IsPrivate(), t.Assets.Manifest)
	customSections := precommit.ExtractCustomSections(existing)
	wanted, err := ci.Generate(blocksFS, t.Assets.Manifest, components, preCommitConfig,
		customSections, ci.ReleaseGatesOnValidate(root), runner)
	if errors.Is(err, ci.ErrNoJobs) {
		return ciState{reason: "no component has a CI block and the committed pre-commit config names no hook to run"}, nil
	}
	if err != nil {
		return nil, err
	}

	// Its content depends on the runner, so a repo that stops being private
	// sees the pool come out of it as a change to this file.
	lintConfig := ci.ActionlintConfig(t.Assets.Manifest, runner)
	state := ciState{
		applicable: true,
		selfHosted: runner == ci.SelfHosted,
		files: []ciFile{
			{
				generatedFile: readGenerated(root, ci.ActionlintConfigPath, lintConfig),
				write:         "declare to actionlint the runner labels it cannot discover",
			},
			{
				generatedFile: readGenerated(root, ci.WorkflowPath, wanted),
				write:         "regenerate from the standard CI blocks",
			},
		},
	}

	state.repinned = repinnedWorkflows(root, t.Assets.Manifest)

	for _, file := range state.files {
		if !handWritten(root, file.rel) {
			continue
		}
		detail := "exists without the " + stampMark +
			" stamp, so it was hand-written and will not be overwritten"
		if !file.wanted() {
			detail = "exists without the " + stampMark +
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

	// The vue setup reads its Node version from this file, and setup-node fails
	// before installing anything when it is missing. A node component takes the
	// same setup. A job that can never start reports as a red run, so the
	// workflow is not written until the file is.
	if declaresCategory(components, "vue") && !fileExists(filepath.Join(root, ci.NodeVersionFile)) {
		state.blockers = append(state.blockers, blocker(ci.WorkflowPath,
			"a vue or node component is declared with no "+ci.NodeVersionFile+" at the repo root, and its "+
				"setup reads the Node version from there — add one naming the major the Dockerfile builds on"))
	}

	// A custom section is the one part of the workflow nothing else can
	// recreate, so the write waits until each one has a job to sit beside.
	if orphaned := ci.OrphanedSections(customSections, wanted); len(orphaned) > 0 {
		state.blockers = append(state.blockers, blocker(ci.WorkflowPath,
			"a custom section names a job this workflow no longer has, and regenerating would drop it: "+
				strings.Join(orphaned, ", ")+" — rename the marker to a job that exists, such as before:"+ci.HooksJob+
				" for setup the hooks need"))
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
	if finding := lintWorkflow(wanted, lintConfig); finding != "" {
		state.blockers = append(state.blockers, actionlintFinding("generated workflow", finding, t.Assets.Manifest))
	}

	// A hand-written lint config is the one the repo's own actionlint hook
	// reads, and forge never edits it, so it may not declare the hosted image.
	// A workflow it refuses fails every commit, so that write waits until the
	// repo's config accepts it. forge owns all of validate.yml, so any finding
	// holds it back. It owns only the pins of a hand-written workflow, so that
	// one is held back only when the repin is what the config refuses.
	if own, ok := ownLintConfig(root); ok {
		if finding := lintWorkflow(wanted, own); finding != "" {
			state.blockers = append(state.blockers, actionlintFinding(ci.WorkflowPath,
				"the repo's own actionlint config refuses the generated workflow: "+finding, t.Assets.Manifest))
		}
		var kept []generatedFile
		for _, file := range state.repinned {
			if !file.matches() && lintWorkflowAt(file.rel, file.have, own) == "" {
				if finding := lintWorkflowAt(file.rel, file.want, own); finding != "" {
					state.blockers = append(state.blockers, actionlintFinding(file.rel,
						"the repo's own actionlint config refuses the repinned workflow: "+finding, t.Assets.Manifest))
					continue
				}
			}
			kept = append(kept, file)
		}
		state.repinned = kept
	}

	return state, nil
}

// ownLintConfig is the hand-written actionlint config a repo carries, at the
// spelling actionlint prefers when both are present.
func ownLintConfig(root string) (string, bool) {
	for _, rel := range []string{ci.ActionlintConfigPath, ci.ActionlintConfigAltPath} {
		if !handWritten(root, rel) {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			return string(data), true
		}
	}
	return "", false
}

// declaresCategory is whether a component lints with the category's blocks,
// which a node component does with vue's.
func declaresCategory(components []config.Component, category string) bool {
	return slices.ContainsFunc(components, func(c config.Component) bool { return precommit.StackToCategory(c.Stack) == category })
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// committedPreCommit is the pre-commit config a repo carries where forge
// maintains it, and "" elsewhere.
//
// The hooks job runs this file rather than the config the precommit die would
// write. The two differ wherever that die is blocked or not yet applied, and a
// job naming a hook the file lacks fails every run with "No hook with id".
func committedPreCommit(t reconcile.Target) string {
	data, err := os.ReadFile(filepath.Join(t.Repo.Path, preCommitConfigPath))
	if err != nil {
		return ""
	}
	if _, basis := maintained(t.Repo.Toolchain, string(data)); !basis.applicable() {
		return ""
	}
	return string(data)
}

// repinnedWorkflows is every workflow forge did not write, each with the
// declared pins applied.
//
// A version bump has to reach these too, or a release job runs an older action
// than the validation it gates on. Only the pin lines change: the rest of the
// file is the repo's own, which is why a hand-written workflow is otherwise
// never touched.
func repinnedWorkflows(root string, manifest *toolchain.Toolchain) []generatedFile {
	dir := filepath.Dir(ci.WorkflowPath)
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil
	}
	var files []generatedFile
	for _, entry := range entries {
		rel := filepath.ToSlash(filepath.Join(dir, entry.Name()))
		// A hand-written file at the generated path is reported whole instead.
		if ext := filepath.Ext(rel); entry.IsDir() || (ext != ".yml" && ext != ".yaml") || rel == ci.WorkflowPath || !handWritten(root, rel) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		have := string(data)
		files = append(files, generatedFile{rel: rel, want: manifest.ApplyWorkflowPins(have), have: have, exists: true})
	}
	return files
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
// config is an actionlint configuration, written into the throwaway repo
// alongside the workflow. Nothing a repo carries on disk reaches this
// directory, so without it a workflow naming the self-hosted pool or the
// pinned image is rejected here as an unknown label — before plan can show the
// diff or apply can write it, and for every repo at once.
//
// The config handed in is either the one the repo is owed, so this lints what
// would be written rather than a guess, or the repo's own hand-written one.
//
// The finding is returned raw. Whether forge can stand behind it depends on
// which actionlint answered, and that is the caller's decision rather than a
// property of the lint itself.
func lintWorkflow(workflow, config string) string {
	return lintWorkflowAt(ci.WorkflowPath, workflow, config)
}

// lintWorkflowAt is lintWorkflow with the workflow written at rel, so a
// finding names the file it is about.
func lintWorkflowAt(rel, workflow, config string) string {
	dir, err := os.MkdirTemp("", "forge-actionlint-")
	if err != nil {
		return ""
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, filepath.FromSlash(rel))
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

	return runValidator(dir, "actionlint", rel)
}

// actionlintFinding renders a lint finding as the Change a plan shows.
//
// A finding from an actionlint that is not the pinned one is Unknown rather
// than a blocker. This gate's whole job is to predict what a repo's own
// actionlint hook will say about one label, and a different binary answers a
// different question — so the honest verdict is that forge could not tell.
// Unknown never moves the exit code, which is what keeps an unpinned local
// install from failing `forge repos check` across the portfolio.
func actionlintFinding(item, finding string, manifest *toolchain.Toolchain) reconcile.Change {
	pinned := strings.TrimPrefix(pinnedActionlint(manifest), "v")
	installed := installedActionlint()
	if pinned != "" && installed != "" && installed != pinned {
		return reconcile.Change{
			Item:    item,
			Verdict: reconcile.Unknown,
			Repair:  reconcile.NoRepair,
			Detail: "actionlint " + installed + " is installed and the declaration pins " + pinned +
				", so this is not what the repo's own hook will say: " + finding,
		}
	}
	return blocker(item, "actionlint: "+finding)
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

	// The lint declaration goes before every workflow that names its labels.
	// reconcile.Apply performs changes in this order, so a run interrupted
	// between two of them has to leave a repo that still builds. A declaration
	// for a label nothing uses is inert; a workflow naming a label nothing
	// declares fails that repo's actionlint hook on every commit until the
	// declaration lands.
	//
	// A hand-written file is not drift to repair — it is a file forge refuses
	// to touch — so its change is suppressed while the blocker is there, or the
	// plan would promise a write Perform would refuse. Asked per file: a
	// hand-written lint config holds back only a workflow it refuses, and
	// Observe files that as a blocker on the workflow's own path.
	for _, file := range state.files {
		if hasItem(state.blockers, file.rel) {
			continue
		}
		if change, drifted := file.change(file.write); drifted {
			changes = append(changes, change)
		}
	}
	// Not suppressed by a blocker on the same path. The one a hand-written
	// ci.yml draws is about it duplicating jobs, which its pins do not touch.
	// A repin the repo's own lint config refuses is already out of this list.
	for _, file := range state.repinned {
		if change, drifted := file.change(repinReason); drifted {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

// repinReason is what a plan says for a workflow forge did not write.
const repinReason = "take the declared action and tool versions and runner image; the rest of this workflow is the repo's own"

func (c CI) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := c.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(ciState)

	for _, repinned := range state.repinned {
		if repinned.rel != change.Item {
			continue
		}
		if repinned.matches() {
			return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
		}
		if err := writeGenerated(t.Repo.Path, repinned); err != nil {
			return reconcile.Outcome{}, err
		}
		return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "repinned " + repinned.rel}, nil
	}

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
