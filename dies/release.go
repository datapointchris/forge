package dies

import (
	"fmt"
	"os"
	"slices"
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
	return "Assert the concurrency group a release workflow needs so two pushes to main never race, with no queue key so a newer push replaces a waiting run, keep the comment it wrote above the group current, and report one triggered on a tag."
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
// The block sets no queue key. A group then holds one pending run, and a newer
// push replaces it. Only main's tip can cut a version, so a burst of pushes
// releases once, from its last commit. queue: max keeps every run waiting oldest
// first, which puts the one run able to release behind all the rest.
var concurrencyBlock = append(slices.Clone(concurrencyComment),
	"concurrency:",
	"  group: release-${{ github.ref }}",
	"  cancel-in-progress: false",
)

// concurrencyComment opens the block. It names no tag push or version commit,
// because which of those a release writes differs from repo to repo.
var concurrencyComment = []string{
	"# Two pushes to main inside one run's window would otherwise race, because",
	"# both runs resolve the same next version from the same newest tag.",
	"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
	"# A newer push replaces a waiting run, so a burst of pushes releases once.",
}

// earlierConcurrencyComments are the comments this die wrote above the block
// before concurrencyComment. A workflow carrying one gets concurrencyComment in
// its place. A comment a person wrote matches none, so it is left alone.
var earlierConcurrencyComments = [][]string{
	{
		"# Two pushes to main inside one run's window would otherwise race, and the later",
		"# run fails pushing its version bump onto a ref that has already moved.",
		"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
	},
	{
		"# Two pushes to main inside one run's window would otherwise race. Both runs",
		"# resolve the same version, and the later one fails pushing its tag or its",
		"# version commit.",
		"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
	},
	{
		"# Two pushes to main inside one run's window would otherwise race. Both runs",
		"# resolve the same version, and the later one fails pushing its tag or its",
		"# version commit.",
		"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
		"# queue: max keeps every waiting run, so no push's release replaces another's.",
	},
	{
		"# Two pushes to main inside one run's window would otherwise race. Both runs",
		"# resolve the same version, and the later one fails pushing its tag or its",
		"# version commit.",
		"# Never cancel-in-progress: a canceled release can leave a tag with no release.",
		"# A newer push replaces a waiting run, so a burst of pushes releases once.",
	},
}

type releaseState struct {
	exists bool
	lines  []string
	// parseErr is carried rather than returned, because a workflow this die
	// cannot read is an Unknown to report and not a failure of the walk.
	parseErr error

	hasConcurrency bool
	// commentFrom and commentTo bound, 0-based and end exclusive, the comment
	// lines directly above the concurrency key. earlierComment says they are one
	// of earlierConcurrencyComments.
	commentFrom, commentTo int
	earlierComment         bool
	// queue is the concurrency block's queue value, "" where it sets none, and
	// queueIndex its 0-based line. queueInFlow says the block is written on one
	// line, so removing that line would remove the whole block.
	queue        string
	queueIndex   int
	queueInFlow  bool
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
		observeComment(&state, mappingKey(root, "concurrency").Line-1)
	}
	if jobs := mappingKey(root, "jobs"); jobs != nil {
		state.jobsIndex = jobs.Line - 1
	}
	if push := mappingValue(mappingValue(root, "on"), "push"); push != nil {
		state.tagTriggered = mappingValue(push, "tags") != nil
	}

	return state, nil
}

// observeQueue reads the concurrency block's queue key and the line it sits on.
func observeQueue(state *releaseState, concurrency *yaml.Node) {
	key := mappingKey(concurrency, "queue")
	if key == nil {
		return
	}
	state.queue = mappingValue(concurrency, "queue").Value
	state.queueIndex = key.Line - 1
	state.queueInFlow = concurrency.Style&yaml.FlowStyle != 0
}

// observeComment reads the comment lines directly above the concurrency key at keyIndex.
func observeComment(state *releaseState, keyIndex int) {
	from := keyIndex
	for from > 0 && strings.HasPrefix(state.lines[from-1], "#") {
		from--
	}
	state.commentFrom, state.commentTo = from, keyIndex
	comment := state.lines[from:keyIndex]
	state.earlierComment = slices.ContainsFunc(earlierConcurrencyComments, func(earlier []string) bool {
		return slices.Equal(earlier, comment)
	})
}

// commentChange is the finding for a block under a comment this die wrote earlier.
func commentChange(state releaseState) reconcile.Change {
	return reconcile.Change{
		Item:     "concurrency.comment",
		Verdict:  reconcile.Stale,
		Repair:   reconcile.Automatic,
		Detail:   fmt.Sprintf("%s carries an earlier comment this die wrote above its concurrency block; expected the current one", releaseWorkflowPath),
		Observed: strings.Join(state.lines[state.commentFrom:state.commentTo], "\n"),
		Patch:    unifiedDiff(releaseWorkflowPath, joinLines(state.lines), joinLines(withCurrentComment(state))),
	}
}

// withCurrentComment returns the file's lines with concurrencyComment in place of
// the comment above the concurrency key.
func withCurrentComment(state releaseState) []string {
	return slices.Replace(slices.Clone(state.lines), state.commentFrom, state.commentTo, concurrencyComment...)
}

// queueChange is the finding for a concurrency block that sets a queue key.
func queueChange(state releaseState) reconcile.Change {
	change := reconcile.Change{
		Item:     "concurrency.queue",
		Verdict:  reconcile.Stale,
		Detail:   fmt.Sprintf("%s has queue: %s; expected no queue key, so a newer push replaces a waiting run", releaseWorkflowPath, state.queue),
		Observed: "queue: " + state.queue,
	}
	if state.queueInFlow {
		change.Repair = reconcile.ByHand
		change.Advice = "remove queue from the concurrency block of " + releaseWorkflowPath
		return change
	}
	change.Repair = reconcile.Automatic
	change.Patch = unifiedDiff(releaseWorkflowPath, joinLines(state.lines), joinLines(withoutQueue(state)))
	return change
}

// withoutQueue returns the file's lines with the queue key's line removed.
func withoutQueue(state releaseState) []string {
	return slices.Delete(slices.Clone(state.lines), state.queueIndex, state.queueIndex+1)
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
	if state.earlierComment {
		changes = append(changes, commentChange(state))
	}
	if state.queue != "" {
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
	message := "added"
	switch {
	case change.Item == "concurrency.queue" && state.queue == "":
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already absent"}, nil
	case change.Item == "concurrency.queue":
		if state.queueInFlow {
			return changed, nil
		}
		updated, message = joinLines(withoutQueue(state)), "removed"
	case change.Item == "concurrency.comment" && !state.earlierComment:
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	case change.Item == "concurrency.comment":
		updated, message = joinLines(withCurrentComment(state)), "rewritten"
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
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: message}, nil
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
