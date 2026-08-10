package dies

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/datapointchris/forge/v6/reconcile"
)

// errNotGitHub means the repo is hosted somewhere gh cannot speak to.
//
// Not a failure and not drift. A repo on Bitbucket is not a repo that has
// fallen behind — it is one whose provider has its own settings and its own
// tool, and the remote is the only thing that actually knows which.
var errNotGitHub = errors.New("not a GitHub remote")

// unmeasurable is why a die could not tell, as opposed to what it found.
//
// The distinction matters at fleet scale: one unauthenticated gh would
// otherwise report every repo as drifted, and `apply` would then be offered as
// the fix for a login problem.
type unmeasurable struct{ reason string }

func (u unmeasurable) Error() string { return u.reason }

// unknownChange is the Change an unmeasurable observation produces: counted and
// named, in neither lens, and never in the exit code.
func unknownChange(item, reason string) reconcile.Change {
	return reconcile.Change{
		Item:    item,
		Verdict: reconcile.Unknown,
		Repair:  reconcile.NoRepair,
		Detail:  reason,
	}
}

// ghRepo identifies a repo to the GitHub API.
type ghRepo struct {
	slug          string
	defaultBranch string
}

// resolveGitHubRepo asks gh for the slug rather than parsing the remote URL: gh
// handles ssh, https and the .git suffix, and it is already the thing that has
// to agree with the API.
func resolveGitHubRepo(dir string) (ghRepo, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return ghRepo{}, unmeasurable{"gh is not installed"}
	}

	remote, err := runIn(dir, "git", "config", "--get", "remote.origin.url")
	if err != nil || remote == "" {
		return ghRepo{}, errNotGitHub
	}
	if !strings.Contains(remote, "github.com") {
		return ghRepo{}, errNotGitHub
	}

	out, err := runIn(dir, "gh", "repo", "view", "--json", "nameWithOwner,defaultBranchRef",
		"-q", `"\(.nameWithOwner) \(.defaultBranchRef.name)"`)
	if err != nil {
		return ghRepo{}, unmeasurable{"gh could not identify this repo (check `gh auth status`)"}
	}

	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ghRepo{}, unmeasurable{"gh returned no repo identity"}
	}
	repo := ghRepo{slug: fields[0]}
	if len(fields) > 1 && fields[1] != "null" {
		repo.defaultBranch = fields[1]
	}
	return repo, nil
}

// runIn runs a command in a repo directory and returns its trimmed stdout.
//
// stdout only, and the error carries stderr. gh prints API errors to stdout, so
// a combined capture would fold a 404 body into the value being parsed — the
// bug that made a repo with no branch protection look like it had some.
func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// settingChange is one repo setting that is not what the standard wants.
func settingChange(name, observed, want string) reconcile.Change {
	return reconcile.Change{
		Item:     name,
		Verdict:  reconcile.Stale,
		Repair:   reconcile.Automatic,
		Detail:   "should be " + want,
		Observed: observed,
	}
}

// observeGitHub folds the two non-answers into what each die should report:
// a non-GitHub remote is out of scope and says so, and anything else that
// could not be reached is unmeasured.
func observeGitHub(t reconcile.Target, item string) (ghRepo, []reconcile.Change, string, error) {
	repo, err := resolveGitHubRepo(t.Repo.Path)
	switch {
	case errors.Is(err, errNotGitHub):
		return ghRepo{}, nil, "not a GitHub remote", nil
	case err != nil:
		var cannot unmeasurable
		if errors.As(err, &cannot) {
			return ghRepo{}, []reconcile.Change{unknownChange(item, cannot.reason)}, "", nil
		}
		return ghRepo{}, nil, "", err
	}
	return repo, nil, "", nil
}

// pipeTo runs a command with a body on stdin, for the API calls that take a
// JSON document rather than fields.
func pipeTo(dir, body, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(body)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
