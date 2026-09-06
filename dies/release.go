package dies

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/datapointchris/forge/reconcile"
)

// releaseWorkflowPath is the file this die asserts facts about. It is not
// generated here, unlike the validate.yml the ci die owns.
const releaseWorkflowPath = ".github/workflows/release.yml"

// Release asserts the baseline a release workflow needs, inside a file it does
// not write.
//
// Present-not-generated, the same shape as the gitignore die and for the same
// reason. A release workflow is legitimately per-repo: five stacks reach the tag
// five ways, one repo tags a nested module with its own prefix, and two build
// container images alongside. Generating the file would either flatten that or
// need a template per shape plus an escape hatch for each exception. So this
// asserts what every one of them needs and leaves the rest alone.
//
// A repo with no release workflow is not drift. Whether a repo releases at all
// is the repo's to say, and inventing one would tag something nobody asked to
// publish.
type Release struct{}

func (Release) Name() string { return "release" }

func (Release) Description() string {
	return "Assert the concurrency group a release workflow needs so two pushes to main queue rather than race, and report one triggered on a tag."
}

func (Release) Tags() []string {
	return []string{"release", "workflow", "actions", "concurrency", "standardization", "golden-path"}
}

// concurrencyBlock is what a workflow missing the group gains.
//
// cancel-in-progress stays false. Canceling a release mid-flight can leave the
// tag pushed and the release never created, which is worse than the queue: the
// Go module proxy serves a tag the moment it is asked for and caches it, so a
// half-finished release is not retractable.
//
// The group keys on the ref rather than the workflow, so a release on a branch
// queues against that branch and not against main.
var concurrencyBlock = []string{
	"# Two pushes to main inside one run's window would otherwise race, and the later",
	"# run fails pushing its version bump onto a ref that has already moved.",
	"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
	"concurrency:",
	"  group: release-${{ github.ref }}",
	"  cancel-in-progress: false",
}

type releaseState struct {
	exists bool
	lines  []string
	// parseErr is carried rather than returned, because a workflow this die
	// cannot read is an Unknown to report and not a failure of the walk.
	parseErr error

	hasConcurrency bool
	tagTriggered   bool
	// jobsIndex is where the block goes, 0-based. -1 when there is no top-level
	// jobs key to insert before.
	jobsIndex int
}

func (s releaseState) Summary() string {
	if !s.exists {
		return "no release workflow"
	}
	if s.parseErr != nil {
		return "release workflow unreadable"
	}
	return fmt.Sprintf("release workflow current (%s)", plural(len(s.lines), "line", "lines"))
}

func (Release) Observe(t reconcile.Target) (reconcile.Observation, error) {
	file, err := readLineFile(t.Path(releaseWorkflowPath))
	if err != nil {
		return nil, err
	}
	if !file.exists {
		return releaseState{jobsIndex: -1}, nil
	}

	state := releaseState{exists: true, lines: file.lines, jobsIndex: -1}

	var document yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(file.lines, "\n")), &document); err != nil {
		state.parseErr = err
		return state, nil
	}
	root := documentRoot(&document)
	if root == nil {
		state.parseErr = fmt.Errorf("no top-level mapping")
		return state, nil
	}

	// Walking nodes rather than decoding into a map, because `on` is a YAML 1.1
	// boolean and yaml.v3 still resolves it as one — decoded, the trigger key
	// arrives as `true` and the workflow reads as having no triggers at all. A
	// node's Value is the literal text, which is not resolved.
	if concurrency := mappingValue(root, "concurrency"); concurrency != nil {
		state.hasConcurrency = true
	}
	if jobs := mappingKey(root, "jobs"); jobs != nil {
		state.jobsIndex = jobs.Line - 1
	}
	if push := mappingValue(mappingValue(root, "on"), "push"); push != nil {
		state.tagTriggered = mappingValue(push, "tags") != nil
	}

	return state, nil
}

func (Release) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(releaseState)
	if !ok {
		return nil, fmt.Errorf("release: unexpected observation %T", observed)
	}
	if !state.exists {
		return nil, nil
	}
	if state.parseErr != nil {
		return []reconcile.Change{{
			Item:     releaseWorkflowPath,
			Verdict:  reconcile.Unknown,
			Repair:   reconcile.NoRepair,
			Detail:   "could not be read as YAML, so nothing about it was measured",
			Observed: state.parseErr.Error(),
		}}, nil
	}

	var changes []reconcile.Change
	if !state.hasConcurrency {
		change := reconcile.Change{
			Item:    "concurrency",
			Verdict: reconcile.Missing,
			Detail:  "two pushes to main inside one run's window race, and the later run fails on a non-fast-forward",
		}
		if state.jobsIndex < 0 {
			// Nowhere to put it that is certainly top level. Appending would land
			// the key inside whatever block ends the file.
			change.Repair = reconcile.ByHand
			change.Observed = "no top-level jobs key to insert above"
		} else {
			change.Repair = reconcile.Automatic
			change.Patch = unifiedDiff(
				releaseWorkflowPath,
				joinLines(state.lines),
				joinLines(withConcurrency(state.lines, state.jobsIndex)),
			)
		}
		changes = append(changes, change)
	}
	if state.tagTriggered {
		// Reported, never rewritten. Inverting a trigger changes when the whole
		// workflow runs, and a repo tagging by hand may have a build step that
		// only makes sense after the tag exists.
		changes = append(changes, reconcile.Change{
			Item:     "on.push.tags",
			Verdict:  reconcile.Stale,
			Repair:   reconcile.ByHand,
			Detail:   "the release workflow decides the version and pushes the tag, so triggering on one validates nothing that can still be stopped",
			Observed: "triggers on a pushed tag",
		})
	}
	return changes, nil
}

func (r Release) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{
			Change:  change,
			Status:  reconcile.Refused,
			Message: "only a person can settle this one",
		}, nil
	}

	// Re-read rather than trust the plan: the plan may have been on screen for a
	// while, and the line the block goes above moves whenever the file does.
	observed, err := r.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state, ok := observed.(releaseState)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("release: unexpected observation %T", observed)
	}
	if !state.exists || state.parseErr != nil || state.jobsIndex < 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the workflow no longer reads the way the plan measured it"}, nil
	}
	if state.hasConcurrency {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already present"}, nil
	}

	updated := joinLines(withConcurrency(state.lines, state.jobsIndex))
	if err := os.WriteFile(t.Path(releaseWorkflowPath), []byte(updated), 0o644); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "added"}, nil
}

// withConcurrency returns the file's lines with the block above the jobs key.
//
// The blank line is matched to the file rather than always emitted. Half the
// portfolio separates top-level keys with one and half runs them together, and a
// die that imposes its own spacing shows a whitespace change in a diff that is
// supposed to read as one added key.
func withConcurrency(lines []string, jobsIndex int) []string {
	block := concurrencyBlock
	if jobsIndex > 0 && strings.TrimSpace(lines[jobsIndex-1]) == "" {
		block = append(append([]string{}, block...), "")
	}

	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:jobsIndex]...)
	out = append(out, block...)
	return append(out, lines[jobsIndex:]...)
}

// joinLines renders lines back to file text, with the trailing newline
// readLineFile trimmed. Without it every applied file loses its last newline.
func joinLines(lines []string) string { return strings.Join(lines, "\n") + "\n" }

// documentRoot unwraps the document node yaml.Unmarshal produces, returning the
// mapping inside it or nil for anything else.
func documentRoot(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	return node
}

// mappingKey returns the key node for a name, which is the one carrying the line
// number the key was written on.
func mappingKey(mapping *yaml.Node, name string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i]
		}
	}
	return nil
}

// mappingValue returns the value node for a name, and nil when the mapping does
// not carry it. Nil-tolerant on the way in so a lookup can be chained.
func mappingValue(mapping *yaml.Node, name string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i+1]
		}
	}
	return nil
}
