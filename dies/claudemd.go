package dies

import (
	"fmt"
	"os"
	"strings"

	"github.com/datapointchris/forge/v6/reconcile"
)

// ClaudeMD reports whether a repo carries the file that tells an agent how to
// work in it.
//
// A reconciler whose every finding is ByHand, which is not a degenerate case
// but the point: forge cannot author a repo's CLAUDE.md, and Repair is what
// lets it say so without either pretending it can or dropping the standard.
// Perform exists on the interface and is unreachable from any verb, because
// nothing this die reports is ever Actionable.
//
// This is has-claude-md, which exited 1 — a failure — for a repo that simply
// had not been given one yet. It is an Issue now, which is the same severity
// said honestly: something a person has to do, not something that broke.
type ClaudeMD struct{}

func (ClaudeMD) Name() string { return "claude-md" }

func (ClaudeMD) Description() string {
	return "Report a repo with no CLAUDE.md, or one thin enough to be a placeholder. Never writes: what belongs in it is the repo's to say."
}

func (ClaudeMD) Tags() []string { return []string{"claude", "docs", "scorecard"} }

// thinEnoughToBePlaceholder is where a CLAUDE.md stops being worth having.
//
// A threshold rather than a count of what repos happen to carry today: the
// number of lines in the fleet's files changes on every commit, and a rule
// written against it would be wrong immediately. Ten lines is roughly a title
// and a stub — enough to satisfy "the file exists" and not enough to answer a
// question anyone would open it with.
const thinEnoughToBePlaceholder = 10

type claudeMDState struct {
	exists bool
	lines  int
}

func (s claudeMDState) Summary() string {
	if !s.exists {
		return "no CLAUDE.md"
	}
	return fmt.Sprintf("CLAUDE.md (%s)", plural(s.lines, "line", "lines"))
}

func (ClaudeMD) Observe(t reconcile.Target) (reconcile.Observation, error) {
	data, err := os.ReadFile(t.Path("CLAUDE.md"))
	if os.IsNotExist(err) {
		return claudeMDState{}, nil
	}
	if err != nil {
		return nil, err
	}
	return claudeMDState{exists: true, lines: len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))}, nil
}

func (ClaudeMD) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(claudeMDState)
	if !ok {
		return nil, fmt.Errorf("claude-md: unexpected observation %T", observed)
	}

	switch {
	case !state.exists:
		return []reconcile.Change{{
			Item: "CLAUDE.md", Verdict: reconcile.Missing, Repair: reconcile.ByHand,
			Detail: "an agent working here has to infer the layout and the conventions",
		}}, nil

	case state.lines < thinEnoughToBePlaceholder:
		return []reconcile.Change{{
			Item: "CLAUDE.md", Verdict: reconcile.Stale, Repair: reconcile.ByHand,
			Detail: "thin enough to be a placeholder", Observed: plural(state.lines, "line", "lines"),
		}}, nil
	}

	return nil, nil
}

// Perform is unreachable: nothing this die reports is Actionable. It refuses
// rather than panicking, so a future change that made a finding automatic
// fails loudly here instead of writing something nobody designed.
func (ClaudeMD) Perform(_ reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	return reconcile.Outcome{
		Change:  change,
		Status:  reconcile.Refused,
		Message: "what belongs in a CLAUDE.md is the repo's to say",
	}, nil
}
