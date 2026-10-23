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
	return "Assert the concurrency group a release workflow needs so every push to main queues a release run rather than racing or replacing a waiting one, and report one triggered on a tag."
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
//
// queue: max keeps every pending run. Without it a group holds one, and a third
// push cancels the second push's waiting run, so that merge never gets a
// release run of its own.
var concurrencyBlock = []string{
	"# Two pushes to main inside one run's window would otherwise race. Both runs",
	"# resolve the same version, and the later one fails pushing its tag or its",
	"# version commit.",
	"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
	"# queue: max keeps every waiting run, so no push's release replaces another's.",
	"concurrency:",
	"  group: release-${{ github.ref }}",
	"  cancel-in-progress: false",
	"  " + queueLine,
}

// queueLine is the key a concurrency block needs so a pending run waits behind
// the one in flight instead of replacing the run already waiting.
const queueLine = "queue: max"

type releaseState struct {
	exists bool
	lines  []string
	// parseErr is carried rather than returned, because a workflow this die
	// cannot read is an Unknown to report and not a failure of the walk.
	parseErr error

	hasConcurrency bool
	// queue is the concurrency block's queue value, "" where it sets none.
	queue string
	// queueBlocked says why queue: max cannot be added automatically, "" where
	// it can: a scalar block has no mapping to add a key to, and GitHub refuses
	// queue: max beside cancel-in-progress: true.
	queueBlocked string
	// queueIndex is the 0-based line queue: max goes after, the last line of the
	// concurrency mapping, and queueIndent the indentation of its keys.
	queueIndex   int
	queueIndent  string
	tagTriggered bool
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
		observeQueue(&state, concurrency)
	}
	if jobs := mappingKey(root, "jobs"); jobs != nil {
		state.jobsIndex = jobs.Line - 1
	}
	if push := mappingValue(mappingValue(root, "on"), "push"); push != nil {
		state.tagTriggered = mappingValue(push, "tags") != nil
	}

	return state, nil
}

// observeQueue reads the concurrency block's queue key, and where queue: max
// would go after the block's last line.
func observeQueue(state *releaseState, concurrency *yaml.Node) {
	if concurrency.Kind != yaml.MappingNode || len(concurrency.Content) == 0 {
		state.queueBlocked = "concurrency names a group alone, with no mapping to add queue to"
		return
	}
	if concurrency.Style&yaml.FlowStyle != 0 {
		state.queueBlocked = "concurrency is written on one line, in flow style"
		return
	}
	if queue := mappingValue(concurrency, "queue"); queue != nil {
		state.queue = queue.Value
	}
	if cancel := mappingValue(concurrency, "cancel-in-progress"); cancel != nil && cancel.Value == "true" {
		state.queueBlocked = "cancel-in-progress is true, which GitHub refuses beside queue: max"
	}
	state.queueIndent = strings.Repeat(" ", concurrency.Content[0].Column-1)
	state.queueIndex = concurrency.Content[len(concurrency.Content)-1].Line - 1
}

// queueChange is the finding for a concurrency block that does not set queue:
// max.
func queueChange(state releaseState) reconcile.Change {
	change := reconcile.Change{
		Item:    "concurrency.queue",
		Verdict: reconcile.Missing,
		Detail:  fmt.Sprintf("missing concurrency key: queue in %s; expected %s", releaseWorkflowPath, queueLine),
		Advice:  "write the concurrency block of " + releaseWorkflowPath + " with group, cancel-in-progress: false and " + queueLine,
	}
	switch {
	case state.queue != "":
		change.Verdict = reconcile.Stale
		change.Repair = reconcile.ByHand
		change.Detail = fmt.Sprintf("%s has queue: %s; expected %s", releaseWorkflowPath, state.queue, queueLine)
		change.Observed = "queue: " + state.queue
	case state.queueBlocked != "":
		change.Repair = reconcile.ByHand
		change.Detail += "; " + state.queueBlocked
		change.Observed = state.queueBlocked
	default:
		change.Repair = reconcile.Automatic
		change.Advice = ""
		change.Patch = unifiedDiff(releaseWorkflowPath, joinLines(state.lines), joinLines(withQueue(state)))
	}
	return change
}

// withQueue returns the file's lines with queue: max after the concurrency
// block's last line, at the indentation of its keys.
func withQueue(state releaseState) []string {
	out := make([]string, 0, len(state.lines)+1)
	out = append(out, state.lines[:state.queueIndex+1]...)
	out = append(out, state.queueIndent+queueLine)
	return append(out, state.lines[state.queueIndex+1:]...)
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
			Detail:   releaseWorkflowPath + " is not valid YAML: " + state.parseErr.Error(),
			Observed: state.parseErr.Error(),
		}}, nil
	}

	var changes []reconcile.Change
	if !state.hasConcurrency {
		change := reconcile.Change{
			Item:    "concurrency",
			Verdict: reconcile.Missing,
			Detail:  fmt.Sprintf("missing concurrency block in %s; expected one that queues release runs", releaseWorkflowPath),
		}
		if state.jobsIndex < 0 {
			// Nowhere to put it that is certainly top level. Appending would land
			// the key inside whatever block ends the file.
			change.Repair = reconcile.ByHand
			change.Detail += "; the file has no top-level jobs key to insert it above"
			change.Advice = "add the concurrency block at the top level of " + releaseWorkflowPath + " by hand"
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
	if state.hasConcurrency && state.queue != "max" {
		changes = append(changes, queueChange(state))
	}
	if state.tagTriggered {
		// Reported, never rewritten. Inverting a trigger changes when the whole
		// workflow runs, and a repo tagging by hand may have a build step that
		// only makes sense after the tag exists.
		changes = append(changes, reconcile.Change{
			Item:     "on.push.tags",
			Verdict:  reconcile.Stale,
			Repair:   reconcile.ByHand,
			Detail:   fmt.Sprintf("%s triggers on a pushed tag; expected it to trigger on a push to main and cut the tag itself", releaseWorkflowPath),
			Observed: "triggers on a pushed tag",
		})
	}
	return changes, nil
}

func (r Release) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
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
	changed := reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "the workflow changed since the plan measured it"}
	if !state.exists || state.parseErr != nil {
		return changed, nil
	}

	var updated string
	switch {
	case change.Item == "concurrency.queue" && state.queue == "max":
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already present"}, nil
	case change.Item == "concurrency.queue":
		if !state.hasConcurrency || state.queue != "" || state.queueBlocked != "" {
			return changed, nil
		}
		updated = joinLines(withQueue(state))
	case state.hasConcurrency:
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already present"}, nil
	case state.jobsIndex < 0:
		return changed, nil
	default:
		updated = joinLines(withConcurrency(state.lines, state.jobsIndex))
	}
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
