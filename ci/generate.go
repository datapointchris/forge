// Package ci generates a baseline validation workflow from per-stack blocks,
// reusing the pre-commit system's block composition and custom-section markers.
//
// One job per declared component, so a repo's separate modules validate in
// parallel and a failure names which one broke. Repos with elaborate pipelines
// (matrix builds, deploys, image publishing) keep those as their own workflow
// files — this one is additive, and callable via workflow_call so a release
// workflow can gate on it.
package ci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/toolchain"
)

// ErrNoJobs is Generate's answer for a repo with no job to run: none of its
// components has a CI block, and it has no pre-commit config of forge's for
// HooksJob to run. That repo is owed no workflow, which is not a failure to
// generate one.
var ErrNoJobs = errors.New("no component has a CI block and the committed pre-commit config names no hook to run: nothing to generate")

// WorkflowPath is where the generated workflow lands. Deliberately not ci.yml:
// ci.yml is the name a hand-written pipeline takes by default, and generating
// over one would destroy work nothing could recover.
const WorkflowPath = ".github/workflows/validate.yml"

// workflowsDir is read, never written apart from WorkflowPath — every other
// workflow in it is per-repo and deliberately not generated.
const workflowsDir = ".github/workflows"

// NodeVersionFile is the file at the repo root the vue setup hands setup-node.
// The ci die refuses to write the workflow where it is missing.
const NodeVersionFile = ".nvmrc"

// ActionlintConfigPath is where jactionlint reads the configuration file it
// keeps from actionlint, the project it forks, and the only spelling forge
// writes.
const ActionlintConfigPath = ".github/actionlint.yaml"

// ActionlintConfigAltPath is the other spelling jactionlint accepts. It is
// read, never written: jactionlint prefers ActionlintConfigPath when both are
// present, so a file forge wrote would silently override one a person did.
const ActionlintConfigAltPath = ".github/actionlint.yml"

// ActionlintHookRepo is the pre-commit repo pinning jactionlint. The die that
// predicts what a repo's own jactionlint hook will say resolves the pinned
// version through this, so the prediction and the hook cannot be two different
// binaries without it being reported.
const ActionlintHookRepo = "https://github.com/jdx/jactionlint"

// RunnerLabel is the pool every self-hosted runner is registered with. It names
// the pool rather than the box, so a second runner joining the pool needs no
// generated workflow to change.
const RunnerLabel = "private-ci"

// DockerLabel is carried by every self-hosted runner with a Docker daemon. A
// job that starts a container asks for it, so a runner with no daemon never
// takes that job.
const DockerLabel = "docker"

// poolLabels are the labels a self-hosted runner may carry beside RunnerLabel.
// A repo's declared runner labels must come from here. GitHub holds a job
// whose labels no runner carries in the queue for a day before failing it, and
// says nothing in that time.
var poolLabels = []string{DockerLabel}

// DaemonFreeSetPrefix opens the name of a scale set with no Docker daemon. A
// scale set always carries its own name as a label, so a job naming the set
// reaches it alone, where the pool's labels also match the repo's Docker set.
const DaemonFreeSetPrefix = "ci-runners-"

// Runner is the runs-on value every job in a generated workflow carries.
//
// A named type rather than a bool beside the release gate: two adjacent
// booleans at a call site are swappable, and swapping these two points a public
// repo at a private network.
type Runner string

// SelfHosted is a runner inside a private network. GitHub bills Actions
// minutes for hosted runners only, so a private repo has no other way to run CI
// at all.
//
// Linux and X64 are added by GitHub and are not written here. The self-hosted
// label is, and it is the first token of this value.
const SelfHosted Runner = "[self-hosted, " + RunnerLabel + "]"

// Hosted is GitHub's own image, at the release the declaration pins. Actions is
// unmetered on a public repo, so the minutes it spends cost nothing.
func Hosted(manifest *toolchain.Toolchain) Runner {
	return Runner(manifest.HostedRunner)
}

// RunnerFor picks the runner a repo's generated workflows may name.
//
// Anything not positively declared private takes the hosted image, and the
// direction of that default is the whole safety property. A fork's pull request
// on a public repo runs the fork's own code on whatever runner it lands on, and
// a self-hosted runner sits inside a private network. A repo the registry does
// not describe therefore stays hosted.
//
// labels are the repo's declared runner labels, added to the pool on every
// generated job. They are refused on a repo that is not private, because a
// hosted image carries no custom label and every job would queue. A label no
// self-hosted runner carries is refused for the same reason.
func RunnerFor(private bool, labels []string, manifest *toolchain.Toolchain) (Runner, error) {
	for _, label := range labels {
		if !slices.Contains(poolLabels, label) {
			return "", fmt.Errorf("runner label %q is carried by no self-hosted runner; expected one of %s",
				label, strings.Join(poolLabels, ", "))
		}
	}
	if !private {
		if len(labels) > 0 {
			return "", fmt.Errorf("runner labels %s are declared on a repo not declared private; a hosted image carries none of them",
				strings.Join(labels, ", "))
		}
		return Hosted(manifest), nil
	}
	names := []string{"self-hosted", RunnerLabel}
	for _, label := range poolLabels {
		if slices.Contains(labels, label) {
			names = append(names, label)
		}
	}
	return Runner("[" + strings.Join(names, ", ") + "]"), nil
}

// Runners says where each kind of generated job runs. The hooks job starts no
// container even where a component's job does, so it can take a set with no
// daemon.
type Runners struct {
	Jobs  Runner
	Hooks Runner
	// Release is the runner a release workflow keeps for itself, "" where the
	// repo keeps none. Where set, every generated job takes a runs-on input,
	// so a release calling this workflow runs it there.
	Release Runner
}

// WithRelease adds the runner a release workflow keeps for itself, named by its
// one custom label. A hosted image carries no custom label, so the label is
// refused off the self-hosted pool.
func (r Runners) WithRelease(label string) (Runners, error) {
	if label == "" {
		return r, nil
	}
	if !r.Jobs.IsSelfHosted() {
		return Runners{}, fmt.Errorf("release runner label %q is declared on a repo not declared private; a hosted image carries no custom label", label)
	}
	r.Release = Runner("[self-hosted, " + label + "]")
	return r, nil
}

// runsOn is what a generated job's runs-on says: its own runner, or, where the
// repo keeps a release runner, whatever runner a caller passes, falling back to
// its own.
func (r Runners) runsOn(own Runner) Runner {
	if r.Release == "" {
		return own
	}
	return Runner(fmt.Sprintf("${{ inputs.runs-on && fromJSON(inputs.runs-on) || fromJSON('%s') }}", own.JSON()))
}

// overridableRE matches the runs-on runsOn writes, capturing the job's own
// runner as JSON.
var overridableRE = regexp.MustCompile(`^\$\{\{ inputs\.runs-on && fromJSON\(inputs\.runs-on\) \|\| fromJSON\('(.+)'\) \}\}$`)

// ownRunner is the runner a runs-on falls back to when it is the one runsOn
// writes, and false for any other value.
func ownRunner(value string) (Runner, bool) {
	match := overridableRE.FindStringSubmatch(value)
	if match == nil {
		return "", false
	}
	var labels []string
	if err := json.Unmarshal([]byte(match[1]), &labels); err != nil {
		return "", false
	}
	return Runner("[" + strings.Join(labels, ", ") + "]"), true
}

// JSON is the runner as GitHub's fromJSON reads it: a list of labels for the
// pool, a string for a hosted image.
func (r Runner) JSON() string {
	value := any(string(r))
	if inner, ok := strings.CutPrefix(string(r), "["); ok {
		labels := strings.Split(strings.TrimSuffix(inner, "]"), ",")
		for i := range labels {
			labels[i] = strings.TrimSpace(labels[i])
		}
		value = labels
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// Everywhere puts every generated job on one runner.
func Everywhere(runner Runner) Runners {
	return Runners{Jobs: runner, Hooks: runner}
}

// RunnersFor picks the runners a repo's generated workflows may name:
// RunnerFor's for every job, and for the hooks job the daemon-free scale set
// hooksSet names, where it names one. A set is refused off the pool, because a
// hosted image carries no such label and the job would queue for a day.
func RunnersFor(private bool, labels []string, hooksSet string, manifest *toolchain.Toolchain) (Runners, error) {
	runner, err := RunnerFor(private, labels, manifest)
	if err != nil {
		return Runners{}, err
	}
	if hooksSet == "" {
		return Everywhere(runner), nil
	}
	if !runner.IsSelfHosted() {
		return Runners{}, fmt.Errorf("hooks runner set %q is declared on a repo not declared private; a hosted image carries no scale set's name", hooksSet)
	}
	if !strings.HasPrefix(hooksSet, DaemonFreeSetPrefix) {
		return Runners{}, fmt.Errorf("hooks runner set %q names no daemon-free scale set; expected %s<repo>", hooksSet, DaemonFreeSetPrefix)
	}
	return Runners{Jobs: runner, Hooks: Runner("[self-hosted, " + RunnerLabel + ", " + hooksSet + "]")}, nil
}

// IsSelfHosted reports whether this runner is the self-hosted pool, with or
// without labels beside it.
func (r Runner) IsSelfHosted() bool {
	return onThePool(string(r))
}

// onThePool reports whether a runs-on value names the self-hosted pool, with
// any of the labels its runners carry. A job asking for one of them reaches a
// runner, whatever the repo's generated jobs ask for.
func onThePool(value string) bool {
	inner, ok := strings.CutPrefix(value, "[")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, "]")
	if !ok {
		return false
	}
	labels := strings.Split(inner, ",")
	for i := range labels {
		labels[i] = strings.TrimSpace(labels[i])
	}
	if len(labels) < 2 || labels[0] != "self-hosted" || labels[1] != RunnerLabel {
		return false
	}
	for _, label := range labels[2:] {
		if !slices.Contains(poolLabels, label) && !strings.HasPrefix(label, DaemonFreeSetPrefix) {
			return false
		}
	}
	return true
}

// ActionlintConfig declares to jactionlint the runner labels it cannot
// discover.
//
// jactionlint knows the hosted images that existed at its release, so a pinned
// image newer than the pinned jactionlint is reported as an unknown label. The
// self-hosted pool is on no list at all. Either one fails the repo's
// jactionlint hook on the next commit.
//
// The pool is declared only where the repo takes the self-hosted runner.
// Declaring it in a public repo would retire the one check that catches a
// hand-written workflow there reaching the self-hosted runner.
//
// A private repo gets every label the pool's runners carry, whatever its
// generated jobs ask for. A hand-written job starting a container asks for
// DockerLabel in a repo whose generated jobs need no daemon. extra are the
// labels only this repo's jobs name: the scale set its hooks job takes and the
// runner its release keeps, each "" where it names none.
func ActionlintConfig(manifest *toolchain.Toolchain, runner Runner, extra ...string) string {
	labels := "    - " + manifest.HostedRunner + "\n"
	if runner.IsSelfHosted() {
		for _, label := range append([]string{RunnerLabel}, poolLabels...) {
			labels += "    - " + label + "\n"
		}
		for _, label := range extra {
			if label != "" {
				labels += "    - " + label + "\n"
			}
		}
	}
	return fmt.Sprintf(`%s
# Labels jactionlint cannot discover. Its list of GitHub's hosted images is the
# one its release shipped with, and a self-hosted runner's labels are on no
# list. Without this every runs-on naming one is reported as a typo and the
# jactionlint hook fails.
self-hosted-runner:
  labels:
%s`, toolchain.StampFor(manifest.Version), labels)
}

// releaseGateRef matches a reusable-workflow call naming this workflow, the
// shape a release uses to gate on it:
//
//	validate:
//	  uses: ./.github/workflows/validate.yml
var releaseGateRef = regexp.MustCompile(`uses:\s*\./` + regexp.QuoteMeta(WorkflowPath))

// ReleaseGating says whether a release workflow already runs this one as a job.
//
// A named type for the same reason Runner is one. The two sit adjacent in
// Generate's signature, and a call site reading `nil, false, SelfHosted` puts a
// bare literal beside a self-naming constant — where the literal decides
// whether main is validated at all.
type ReleaseGating bool

const (
	// Gated means a release workflow calls this one, so emitting push here
	// would run every job twice for one commit.
	Gated ReleaseGating = true
	// Ungated means nothing else covers main, so this workflow needs push or it
	// validates almost nothing.
	Ungated ReleaseGating = false
)

// ReleaseGatesOnValidate reports whether any of the repo's other workflows runs
// this one as a job.
//
// Every workflow is scanned rather than release.yml alone, because the gating
// workflow is named for the artifact it ships. A repo releasing a nested CLI
// gates from release-cli.yml, so matching release.yml alone missed every one of
// them and they each ran the full suite twice per push for a month.
//
// Detected rather than declared, which is the opposite of how components work.
// A registry flag would be a second place to remember, and the failure it
// guards against is precisely a thing nobody remembers: add a release gate,
// forget the flag, and the duplicate run comes back silently. Reading the files
// cannot drift from the files.
//
// Every unknown answers false, which reproduces the old behavior — an extra
// run, never a missing one.
//
// Takes the repo root rather than reading the working directory: the dies walk
// many repos in one process, where a relative path answers about whichever repo
// the process happens to be standing in.
func ReleaseGatesOnValidate(root string) ReleaseGating {
	if gated, _ := handWrittenWorkflowsMatch(root, releaseGateRef); gated {
		return Gated
	}
	return Ungated
}

// goSemanticReleaseRef matches a step or job invoking go-semantic-release, by its
// action or by the shared reusable workflow. Matching `uses:` is what tells an
// invocation from a comment explaining why a repo does not use it.
var goSemanticReleaseRef = regexp.MustCompile(`uses:\s*\S*go-semantic-release(/action|\.yml)@`)

// InvokesGoSemanticRelease reports whether a workflow cuts the repo's releases
// with go-semantic-release, whose analyzer majors a release on a marker in any
// commit message.
//
// A workflow it cannot read answers true, the opposite of ReleaseGatesOnValidate.
// What it gates is a hook refusing that marker: a spurious one refuses a commit,
// and a missing one lets the marker through to a Go module, where a major
// strands every install.
func InvokesGoSemanticRelease(root string) bool {
	invokes, err := handWrittenWorkflowsMatch(root, goSemanticReleaseRef)
	return invokes || err != nil
}

// handWrittenWorkflowsMatch reports whether any workflow but the generated one
// matches ref, with the first error reading one. A repo with no workflows
// directory has none to read, which is no error.
//
// The generated workflow is skipped so a documentation example inside its own
// comments cannot match.
func handWrittenWorkflowsMatch(root string, ref *regexp.Regexp) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(root, workflowsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var unread error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ext := filepath.Ext(name); ext != ".yml" && ext != ".yaml" {
			continue
		}
		if filepath.Join(workflowsDir, name) == WorkflowPath {
			continue
		}

		data, err := os.ReadFile(filepath.Join(root, workflowsDir, name))
		if err != nil {
			if unread == nil {
				unread = err
			}
			continue
		}
		if ref.Match(data) {
			return true, nil
		}
	}
	return false, unread
}

// Generate composes the workflow from the repo's declared components.
//
// Each component becomes its own job with a working-directory, because a repo
// can hold several of the same stack in different places — an api/ and a cli/
// that are both Go modules, deliberately isolated. One serial job would hide
// which one failed and force them to share a setup step.
//
// A stack job runs only what no hook runs, such as a whole test suite or an
// advisory scan. preCommitConfig is the repo's committed pre-commit config, or
// "" where forge maintains none, and HooksJob runs its hooks.
//
// A stack's setup block puts on PATH what its hooks call, so it opens both the
// stack's own job and HooksJob. A stack with no checks of its own gets no job.
//
// pythonFloors is PythonFloors for the repo. The workflow's UV_PYTHON is the
// highest of them, so a custom section's job runs on a Python every component
// admits. A python job whose own floor is lower runs on that one.
func Generate(
	blocksFS fs.FS,
	manifest *toolchain.Toolchain,
	components []config.Component,
	preCommitConfig string,
	customSections map[string]string,
	releaseGated ReleaseGating,
	pythonFloors map[string]string,
	runners Runners,
) (string, error) {
	checkout, err := loadBlock(blocksFS, "checkout")
	if err != nil {
		return "", err
	}
	shared := stripDescription(checkout)

	var lines []string
	lines = append(
		lines,
		manifest.Stamp(),
		"name: CI",
		"",
		"on:",
	)

	// Development here is trunk-based: work lands on main directly and there are
	// rarely pull requests, so pull_request alone validates almost nothing. A
	// repo with no release pipeline therefore needs push, or the workflow exists
	// and never runs.
	//
	// Where a release does gate on this, push is the *duplicate* rather than the
	// safety net: release.yml fires on the same push and calls this workflow, so
	// emitting push here runs every job twice for one commit. That went unnoticed
	// across fourteen repos because both runs are green — the cost is only ever a
	// confusing history. workflow_call still covers main, so nothing is lost.
	if releaseGated == Ungated {
		lines = append(lines,
			"  push:",
			"    branches: [main]",
		)
	}

	lines = append(lines, "  pull_request:", "  workflow_call:")
	if runners.Release != "" {
		lines = append(lines,
			"    inputs:",
			"      # The runner every job takes, as JSON. The release passes the one it",
			"      # keeps for itself. Unset, as on a pull request, each job takes its own.",
			"      runs-on:",
			"        type: string",
			`        default: ""`,
		)
	}
	lines = append(
		lines,
		"",
		"permissions:",
		"  contents: read",
		"",
	)
	highest := highestFloor(pythonFloors)
	if highest != "" {
		lines = append(lines,
			"# uv runs on this Python rather than whatever the runner happens to",
			"# have. It is the highest requires-python floor among the components.",
			"env:",
			fmt.Sprintf("  UV_PYTHON: %q", highest),
			"",
		)
	}
	lines = append(lines, "jobs:")

	jobs := 0
	var hookSetups, jobCategories []string
	for _, component := range components {
		// By the category that lints the stack, as its pre-commit hooks are: a
		// node component carries the vue block's hooks, and needs node for them.
		category := precommit.StackToCategory(component.Stack)
		setup, err := loadBlock(blocksFS, category+SetupSuffix)
		if err != nil {
			return "", err
		}
		checks, err := loadBlock(blocksFS, category)
		if err != nil {
			return "", err
		}
		// Rendered for its directory here, because the hooks job carries one
		// for every component and has no directory of its own. Two components
		// rendering the same setup need it once.
		if setup != "" {
			if rendered := applyDir(stripDescription(setup), component.Dir); !slices.Contains(hookSetups, rendered) {
				hookSetups = append(hookSetups, rendered)
			}
		}
		// A stack whose every check is a hook, such as shell, leaves them all to
		// the hooks job. So does a declared stack with no CI block, such as
		// docker, which keeps the map free to declare more than CI builds.
		if checks == "" {
			continue
		}
		jobs++
		jobCategories = append(jobCategories, category)
		job := workflowJob{name: JobName(component), dir: component.Dir, checkout: shared, block: joinSteps(stripDescription(setup), stripDescription(checks))}
		if floor, ok := pythonFloors[component.Dir]; ok && category == "python" && floor != highest {
			job.uvPython = floor
		}
		lines = append(lines, job.render(manifest, customSections, runners.Jobs, runners)...)
	}

	hooks := slices.DeleteFunc(HooksToRun(preCommitConfig), func(id string) bool {
		return slices.ContainsFunc(jobCategories, func(category string) bool {
			return slices.Contains(runInFullByStackJob[category], id)
		})
	})
	if len(hooks) > 0 {
		block, err := loadBlock(blocksFS, HooksJob)
		if err != nil {
			return "", err
		}
		if block == "" {
			return "", fmt.Errorf("no %s block to run %s in", HooksJob, strings.Join(hooks, ", "))
		}
		jobs++
		steps := append(slices.Clone(hookSetups), applyHooks(stripDescription(block), hooks))
		job := workflowJob{name: HooksJob, checkout: shared, block: joinSteps(steps...)}
		lines = append(lines, job.render(manifest, customSections, runners.Hooks, runners)...)
	}

	if jobs == 0 {
		return "", ErrNoJobs
	}

	// A custom section is the repo's own apart from its pins, which a version
	// bump would otherwise leave behind in every one naming a declared action.
	if section, ok := customSections["after:all"]; ok {
		lines = append(lines, "", manifest.ApplyWorkflowPins(section))
	}

	lines = append(lines, "")
	workflow := strings.Join(lines, "\n")
	if missing := toolchain.Unpinned(workflow); len(missing) > 0 {
		return "", fmt.Errorf("the versions file pins nothing for %s", strings.Join(missing, ", "))
	}
	return workflow, nil
}

// ForeignRunners names every job in a generated workflow whose runs-on is not
// the runner the repo was generated for.
//
// The generator writes one runner into every job it emits, so a different value
// can only have come from a custom section, which regeneration preserves as
// written apart from its pins.
//
// That job is invisible on a private repo. GitHub refuses a hosted job before
// any step runs, so it reports zero steps and no failing step, while the rest
// of the workflow is green. A provisioning suite sat in exactly that state,
// and it gated what reaches every container's authorized_keys.
//
// On the self-hosted runner, a job asking for the pool with any labels its
// runners carry is not foreign. A custom section starting a container asks
// for DockerLabel where the generated jobs do not.
func ForeignRunners(workflow string, runner Runner) []string {
	var (
		foreign []string
		job     string
	)
	for _, line := range strings.Split(workflow, "\n") {
		if match := precommit.JobHeader.FindStringSubmatch(line); match != nil {
			job = match[1]
			continue
		}
		value, ok := strings.CutPrefix(line, "    runs-on: ")
		value = strings.TrimSpace(value)
		if own, overridable := ownRunner(value); overridable {
			value = string(own)
		}
		if !ok || value == string(runner) || (runner.IsSelfHosted() && onThePool(value)) {
			continue
		}
		if job == "" {
			job = "(unnamed job)"
		}
		foreign = append(foreign, job+" on "+value)
	}
	return foreign
}

// OrphanedSections names each custom section keyed to a job the workflow does
// not have, sorted. Generate renders a section beside its job, so one whose job
// is gone would be dropped by the next regeneration, with nothing said.
func OrphanedSections(customSections map[string]string, workflow string) []string {
	jobs := make(map[string]bool)
	for _, line := range strings.Split(workflow, "\n") {
		if match := precommit.JobHeader.FindStringSubmatch(line); match != nil {
			jobs[match[1]] = true
		}
	}
	var orphaned []string
	for key := range customSections {
		if _, job, _ := strings.Cut(key, ":"); job != "all" && !jobs[job] {
			orphaned = append(orphaned, key)
		}
	}
	slices.Sort(orphaned)
	return orphaned
}

// workflowJob is one job's worth of workflow: the shared checkout, then a
// block's steps, each without the description line its file opens with.
type workflowJob struct {
	name     string
	dir      string
	checkout string
	block    string
	// uvPython overrides the workflow's UV_PYTHON, for a component whose
	// floor is below the highest.
	uvPython string
}

// render is the job's lines, custom sections included. runner is the job's own
// runner, which its placeholders read, and runs-on carries runners.runsOn of it.
func (j workflowJob) render(manifest *toolchain.Toolchain, customSections map[string]string, runner Runner, runners Runners) []string {
	lines := []string{"", fmt.Sprintf("  %s:", j.name), fmt.Sprintf("    runs-on: %s", runners.runsOn(runner))}
	if j.dir != "" && j.dir != "." {
		lines = append(lines, "    defaults:", "      run:", fmt.Sprintf("        working-directory: %s", j.dir))
	}
	if j.uvPython != "" {
		lines = append(lines, "    env:", fmt.Sprintf("      UV_PYTHON: %q", j.uvPython))
	}
	lines = append(lines, "    steps:")

	// Checkout comes first, then before:<job>. "Before" means before the job's
	// own steps, not before the repo exists: a repo can decrypt its test
	// secrets out of secrets/ in one of these, which cannot work against a
	// workspace that has not been checked out. Nothing has wanted to run ahead
	// of checkout.
	lines = append(lines, "", applyPlaceholders(indentComment(manifest.ApplyAll(j.checkout)), j.dir, runner))
	if section, ok := customSections["before:"+j.name]; ok {
		lines = append(lines, "", manifest.ApplyWorkflowPins(section))
	}
	lines = append(lines, "", fmt.Sprintf("      # generated:%s", j.name), applyPlaceholders(indentComment(manifest.ApplyAll(j.block)), j.dir, runner))
	if section, ok := customSections["after:"+j.name]; ok {
		lines = append(lines, "", manifest.ApplyWorkflowPins(section))
	}
	return lines
}

// HooksJob names the job running the repo's pre-commit hooks, and the block it
// is built from.
const HooksJob = "hooks"

// SetupSuffix names a stack's setup block after its category: go-setup is the
// go stack's.
const SetupSuffix = "-setup"

// runInFullByStackJob names, per category, the hooks its own job already runs
// in full, which the hooks job leaves out. The go job's `go test ./...` holds
// every test the go-test hook's -short run does.
var runInFullByStackJob = map[string][]string{"go": {"go-test-repo-mod"}}

// HooksToRun lists every hook a standard block put in the config, by id, at the
// pre-commit stage. The hooks job runs these less runInFullByStackJob's.
//
// A hook in a custom section is the repo's own and stays out. Some need a
// workstation, such as one that drives an editor or a local stack, and a repo
// runs the rest in a custom step of its own. A hook off the pre-commit stage
// grades something a pushed tree does not carry, such as a commit message.
//
// By id rather than by Selector, because `pre-commit run <id>` also runs every
// hook carrying that id under an alias, and all of them are wanted.
func HooksToRun(preCommitConfig string) []string {
	var hooks []string
	for _, hook := range precommit.GeneratedHooks(preCommitConfig) {
		if len(hook.Stages) > 0 && !slices.Contains(hook.Stages, "pre-commit") {
			continue
		}
		if !slices.Contains(hooks, hook.ID) {
			hooks = append(hooks, hook.ID)
		}
	}
	return hooks
}

// joinSteps is one job's steps from several blocks, a blank line apart.
func joinSteps(blocks ...string) string {
	return strings.Join(slices.DeleteFunc(blocks, func(block string) bool { return block == "" }), "\n\n")
}

// hooksLineRE is the line in the hooks block that becomes one line per hook.
var hooksLineRE = regexp.MustCompile(`(?m)^([ \t]*)\{\{hooks\}\}$`)

func applyHooks(block string, hooks []string) string {
	return hooksLineRE.ReplaceAllStringFunc(block, func(line string) string {
		indent := hooksLineRE.FindStringSubmatch(line)[1]
		return indent + strings.Join(hooks, "\n"+indent)
	})
}

// JobName is the workflow job id for a component: the stack, plus the directory
// when the repo holds more than one component of that stack.
func JobName(component config.Component) string {
	if component.Dir == "" || component.Dir == "." {
		return component.Stack
	}
	return component.Stack + "-" + strings.NewReplacer("/", "-", "_", "-").Replace(component.Dir)
}

// loadBlock returns the block whose name matches, or "" when none does.
func loadBlock(blocksFS fs.FS, name string) (string, error) {
	var found string
	err := fs.WalkDir(blocksFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || precommit.BlockName(path) != name {
			return nil
		}
		data, readErr := fs.ReadFile(blocksFS, path)
		if readErr != nil {
			return readErr
		}
		found = strings.TrimRight(string(data), "\n")
		return nil
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

// stripDescription drops a block's leading description comment, which the
// generated: marker replaces.
func stripDescription(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		return strings.Join(lines[1:], "\n")
	}
	return content
}

// indentComment aligns a block's own comments with the steps they annotate.
// Column-zero comments are valid YAML but read as if they escaped the job.
func indentComment(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			lines[i] = "      " + line
		}
	}
	return strings.Join(lines, "\n")
}

// applyPlaceholders resolves the placeholders a block carries: the component's
// directory, and whether setup-go restores a dependency cache.
func applyPlaceholders(content, dir string, runner Runner) string {
	return applyGoCache(applyDir(content, dir), runner)
}

// applyDir resolves the {{dir}} placeholder to the component's directory.
// Action inputs (go-version-file, cache-dependency-path) resolve from the
// workspace root regardless of the job's working-directory, so any path in one
// has to carry the component path explicitly.
func applyDir(content, dir string) string {
	if dir == "" {
		dir = "."
	}
	return strings.ReplaceAll(content, "{{dir}}", dir)
}

// applyGoCache resolves the {{gocache}} placeholder to whether setup-go should
// restore a dependency cache.
//
// A hosted runner starts empty every time, so there the cache is the whole
// point. A self-hosted runner keeps GOMODCACHE and GOCACHE between jobs, so it
// has nothing to gain — and restoring one costs it: the archive untars over a
// module cache that already holds every file, tar fails on each and exits 2,
// and the step writes a "Cannot open: File exists" line per module into the run
// log before reporting the cache as not found. One such step reached 153k lines
// and 29 MB, which is then a log the ingest webhook downloads and parses.
func applyGoCache(content string, runner Runner) string {
	return strings.ReplaceAll(content, "{{gocache}}", strconv.FormatBool(!runner.IsSelfHosted()))
}
