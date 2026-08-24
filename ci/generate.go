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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/toolchain"
)

// WorkflowPath is where the generated workflow lands. Deliberately not ci.yml:
// several repos hand-wrote a ci.yml long before this existed, and generating
// over one would destroy work nothing could recover.
const WorkflowPath = ".github/workflows/validate.yml"

// workflowsDir is read, never written apart from WorkflowPath — every other
// workflow in it is per-repo and deliberately not generated.
const workflowsDir = ".github/workflows"

// ActionlintConfigPath is where actionlint reads its own configuration, and the
// only spelling forge writes.
const ActionlintConfigPath = ".github/actionlint.yaml"

// ActionlintConfigAltPath is the other spelling actionlint accepts. It is read,
// never written: actionlint prefers ActionlintConfigPath when both are present,
// so a file forge wrote would silently override one a person did.
const ActionlintConfigAltPath = ".github/actionlint.yml"

// ActionlintHookRepo is the pre-commit repo pinning actionlint. The die that
// predicts what a repo's own actionlint hook will say resolves the pinned
// version through this, so the prediction and the hook cannot be two different
// binaries without it being reported.
const ActionlintHookRepo = "https://github.com/rhysd/actionlint"

// RunnerLabel is the pool every self-hosted runner is registered with. It names
// the pool rather than the box, so a second runner joining the pool needs no
// generated workflow to change.
const RunnerLabel = "private-ci"

// Runner is the runs-on value every job in a generated workflow carries.
//
// A named type rather than a bool beside the release gate: two adjacent
// booleans at a call site are swappable, and swapping these two points a public
// repo at a private network.
type Runner string

const (
	// Hosted is GitHub's own image. Actions is unmetered on a public repo, so
	// the minutes it spends cost nothing.
	Hosted Runner = "ubuntu-latest"
	// SelfHosted is a runner inside a private network. GitHub bills Actions
	// minutes for hosted runners only, so a private repo has no other way to
	// run CI at all.
	//
	// Linux and X64 are added by GitHub and are not written here. The
	// self-hosted label is, and it is the first token of this value.
	SelfHosted Runner = "[self-hosted, " + RunnerLabel + "]"
)

// RunnerFor picks the runner a repo's generated workflows may name.
//
// Anything not positively declared private takes the hosted image, and the
// direction of that default is the whole safety property. A fork's pull request
// on a public repo runs the fork's own code on whatever runner it lands on, and
// a self-hosted runner sits inside a private network. A repo the registry does
// not describe therefore stays hosted.
func RunnerFor(private bool) Runner {
	if private {
		return SelfHosted
	}
	return Hosted
}

// ActionlintConfig declares the self-hosted label to actionlint.
//
// actionlint discovers GitHub's hosted labels and nothing else, so a runs-on
// naming the pool is reported as an unknown label and the repo's actionlint
// hook fails on the next commit.
//
// Written only into repos whose workflows name the label. Declaring it in a
// public repo would retire the one check that catches a hand-written workflow
// there reaching the self-hosted runner.
func ActionlintConfig(stampVersion int) string {
	return fmt.Sprintf(`# forge-toolchain: %d
# Labels actionlint cannot discover, because they belong to a self-hosted
# runner rather than to one of GitHub's hosted images. Without this every
# runs-on naming one is reported as a typo and the actionlint hook fails.
self-hosted-runner:
  labels:
    - %s
`, stampVersion, RunnerLabel)
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
// Generate's signature, and a call site reading `nil, false, Hosted` puts a
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
// workflow is named for the artifact it ships: learning and nomad release a
// nested CLI from release-cli.yml, and matching one filename missed both. They
// each ran the full suite twice per push for the whole of August as a result.
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
	entries, err := os.ReadDir(filepath.Join(root, workflowsDir))
	if err != nil {
		return Ungated
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ext := filepath.Ext(name); ext != ".yml" && ext != ".yaml" {
			continue
		}
		// The generated workflow does not gate itself, and skipping it keeps a
		// documentation example inside its own comments from matching.
		if filepath.Join(workflowsDir, name) == WorkflowPath {
			continue
		}

		data, err := os.ReadFile(filepath.Join(root, workflowsDir, name))
		if err != nil {
			continue
		}
		if releaseGateRef.Match(data) {
			return Gated
		}
	}

	return Ungated
}

// Generate composes the workflow from the repo's declared components.
//
// Each component becomes its own job with a working-directory, because a repo
// can hold several of the same stack in different places — nomad's api/ and
// cli/ are both Go modules, deliberately isolated. One serial job would hide
// which one failed and force them to share a setup step.
func Generate(
	blocksFS fs.FS,
	manifest *toolchain.Toolchain,
	components []config.Component,
	customSections map[string]string,
	releaseGated ReleaseGating,
	runner Runner,
) (string, error) {
	// The zero value would emit a bare `runs-on:`, which is an invalid workflow
	// GitHub rejects at dispatch rather than at lint. Folded in here so no
	// caller can reach that state by leaving the argument off.
	if runner == "" {
		runner = Hosted
	}

	shared, err := loadBlock(blocksFS, "checkout")
	if err != nil {
		return "", err
	}

	var lines []string
	lines = append(
		lines,
		fmt.Sprintf("# forge-toolchain: %d", manifest.Version),
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

	lines = append(
		lines,
		"  pull_request:",
		"  workflow_call:",
		"",
		"permissions:",
		"  contents: read",
		"",
		"jobs:",
	)

	jobs := 0
	for _, component := range components {
		block, err := loadBlock(blocksFS, component.Stack)
		if err != nil {
			return "", err
		}
		// A declared stack forge has no CI block for yet — docker and terraform
		// are pre-commit concerns today. Silently skipping keeps the map free to
		// declare more than CI currently knows how to build.
		if block == "" {
			continue
		}
		jobs++

		name := JobName(component)
		lines = append(lines, "", fmt.Sprintf("  %s:", name), fmt.Sprintf("    runs-on: %s", runner))
		if component.Dir != "" && component.Dir != "." {
			lines = append(lines, "    defaults:", "      run:", fmt.Sprintf("        working-directory: %s", component.Dir))
		}
		lines = append(lines, "    steps:")

		// Checkout comes first, then before:<stack>. "Before" means before the
		// stack's own steps, not before the repo exists: ichrisbirch decrypts
		// its test secrets out of secrets/ in one of these, which cannot work
		// against a workspace that has not been checked out. Nothing has wanted
		// to run ahead of checkout.
		lines = append(lines, "", applyDir(indentComment(stripDescription(manifest.ApplyAll(shared))), component.Dir))
		if section, ok := customSections["before:"+name]; ok {
			lines = append(lines, "", section)
		}
		lines = append(lines, "", fmt.Sprintf("      # generated:%s", name), applyDir(indentComment(stripDescription(manifest.ApplyAll(block))), component.Dir))
		if section, ok := customSections["after:"+name]; ok {
			lines = append(lines, "", section)
		}
	}

	if jobs == 0 {
		return "", fmt.Errorf("no components with a CI block: nothing to generate")
	}

	if section, ok := customSections["after:all"]; ok {
		lines = append(lines, "", section)
	}

	lines = append(lines, "")
	return strings.Join(lines, "\n"), nil
}

// jobHeader matches a workflow job key: two spaces, a name, a colon, nothing
// after it. Job keys are the only thing at that indent in a generated workflow.
var jobHeader = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)

// ForeignRunners names every job in a generated workflow whose runs-on is not
// the runner the repo was generated for.
//
// The generator writes one runner into every job it emits, so a different value
// can only have come from a custom section — a block preserved verbatim and
// never rewritten, which is the whole contract of a custom section.
//
// That job is invisible on a private repo. GitHub refuses a hosted job before
// any step runs, so it reports zero steps and no failing step, while the rest
// of the workflow is green. homelab's pyinfra suite sat in exactly that state
// and it gates what reaches every container's authorized_keys.
func ForeignRunners(workflow string, runner Runner) []string {
	var (
		foreign []string
		job     string
	)
	for _, line := range strings.Split(workflow, "\n") {
		if match := jobHeader.FindStringSubmatch(line); match != nil {
			job = match[1]
			continue
		}
		value, ok := strings.CutPrefix(line, "    runs-on: ")
		if !ok || strings.TrimSpace(value) == string(runner) {
			continue
		}
		if job == "" {
			job = "(unnamed job)"
		}
		foreign = append(foreign, job+" on "+strings.TrimSpace(value))
	}
	return foreign
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
