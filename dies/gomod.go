package dies

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/datapointchris/forge/reconcile"
)

// GoMod pins the Go toolchain each module builds with, without touching the
// `go` directive that says which Go can consume it.
//
// The two look like one setting and are not. The `go` directive is a floor a
// consumer has to clear; the `toolchain` directive is what this build switches
// up to. Raising the floor is the obvious way to take a fixed standard library
// and it has a measured cost: `go install <tool>@latest` prefers a release the
// installing machine's toolchain can build, so a repo floored above the Go on a
// machine is skipped there — silently, returning 0 and leaving the old binary
// while the installer reports the machine converged.
//
// Measured 2026-08-14 on archlinux with system go1.26.5. todoui was floored at
// 1.26.6 to clear five standard-library advisories; `go install @latest`
// returned 0 in 0.25s and left v1.11.1 in place. fleet took a toolchain
// directive against the same advisories instead: govulncheck came back clean,
// `go list` still reported the module at 1.26.5, `GOTOOLCHAIN=local go build`
// still worked, and the release installed on that same machine.
//
// So this die never writes the `go` line. Raising a floor is a compatibility
// decision about who may consume the module, and it belongs to whoever owns
// the module rather than to a fleet-wide sweep.
type GoMod struct{}

func (GoMod) Name() string { return "gomod" }

func (GoMod) Description() string {
	return "Pin the Go toolchain each module builds with, from the manifest's runtime version. Never touches the `go` directive, which is a consumer's floor rather than this build's toolchain."
}

func (GoMod) Tags() []string {
	return []string{"go", "toolchain", "govulncheck", "standardization", "golden-path"}
}

var (
	goDirectiveRE        = regexp.MustCompile(`(?m)^go\s+(\S+)\s*$`)
	toolchainDirectiveRE = regexp.MustCompile(`(?m)^toolchain\s+(\S+)\s*$`)
)

// goModule is one go.mod under a declared Go component.
type goModule struct {
	// rel is the component directory, repo-relative, and is what the change is
	// itemized by — a triad repo has three modules and a row naming only
	// "go.mod" could not say which.
	rel       string
	exists    bool
	goVersion string
	toolchain string
}

type gomodState struct {
	modules []goModule
	// pinned is the manifest's Go runtime, empty when it pins none. A die with
	// nothing to assert reports converged rather than inventing a version.
	pinned string
}

func (s gomodState) Summary() string {
	if s.pinned == "" {
		return "no Go runtime pinned in the manifest"
	}
	if len(s.modules) == 0 {
		return "no Go modules declared"
	}
	return fmt.Sprintf("toolchain current (%s)", plural(len(s.modules), "module", "modules"))
}

func (GoMod) Observe(t reconcile.Target) (reconcile.Observation, error) {
	state := gomodState{}
	if t.Assets.Manifest != nil {
		if version, managed := t.Assets.Manifest.RuntimeVersion("go"); managed {
			state.pinned = version
		}
	}
	if t.Repo.Toolchain == nil {
		return state, nil
	}

	seen := map[string]bool{}
	for _, component := range t.Repo.Toolchain.Components {
		if component.Stack != "go" {
			continue
		}
		rel := component.Dir
		if rel == "" {
			rel = "."
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true

		module := goModule{rel: rel}
		body, err := os.ReadFile(t.Path(rel, "go.mod"))
		switch {
		case err == nil:
			module.exists = true
			if m := goDirectiveRE.FindStringSubmatch(string(body)); m != nil {
				module.goVersion = m[1]
			}
			if m := toolchainDirectiveRE.FindStringSubmatch(string(body)); m != nil {
				module.toolchain = m[1]
			}
		case os.IsNotExist(err):
			// A declared component with no go.mod is the registry and the repo
			// disagreeing, which is not this die's to settle.
		default:
			return nil, err
		}
		state.modules = append(state.modules, module)
	}
	return state, nil
}

func (GoMod) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(gomodState)
	if !ok {
		return nil, fmt.Errorf("gomod: unexpected observation %T", observed)
	}
	if state.pinned == "" {
		return nil, nil
	}
	want := "go" + state.pinned

	var changes []reconcile.Change
	for _, module := range state.modules {
		if !module.exists {
			continue
		}
		item := filepath.Join(module.rel, "go.mod")

		// A module already floored at or above the pin builds with a fixed
		// standard library without a toolchain line, and adding one would be a
		// second place for the same fact to live.
		if meetsFloor(module.goVersion, state.pinned) {
			if module.toolchain != "" && !meetsFloor(strings.TrimPrefix(module.toolchain, "go"), state.pinned) {
				changes = append(changes, reconcile.Change{
					Item:     item,
					Verdict:  reconcile.Stale,
					Repair:   reconcile.Automatic,
					Detail:   fmt.Sprintf("toolchain is below the `go` directive (%s)", module.goVersion),
					Observed: "toolchain " + module.toolchain,
				})
			}
			continue
		}

		switch {
		case module.toolchain == "":
			changes = append(changes, reconcile.Change{
				Item:     item,
				Verdict:  reconcile.Missing,
				Repair:   reconcile.Automatic,
				Detail:   fmt.Sprintf("builds with the %s standard library; the manifest pins %s", module.goVersion, state.pinned),
				Observed: "go " + module.goVersion,
			})
		case module.toolchain != want:
			changes = append(changes, reconcile.Change{
				Item:     item,
				Verdict:  reconcile.Stale,
				Repair:   reconcile.Automatic,
				Detail:   "the manifest pins " + state.pinned,
				Observed: "toolchain " + module.toolchain,
			})
		}
	}
	return changes, nil
}

func (g GoMod) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := g.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(gomodState)
	if state.pinned == "" {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "the manifest pins no Go runtime"}, nil
	}

	path := t.Path(change.Item)
	body, err := os.ReadFile(path)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	updated, changed := setToolchain(string(body), "go"+state.pinned)
	if !changed {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already pinned"}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "pinned toolchain " + state.pinned}, nil
}

// setToolchain rewrites an existing toolchain directive or inserts one below the
// `go` directive, which is where the go tool itself writes it.
//
// Written rather than shelled out to `go mod edit`, because a go command reads
// the very directive being set and may download a toolchain to run at all —
// which turns applying a pin into a network fetch, inside a die whose whole job
// is one line of text.
func setToolchain(body, want string) (string, bool) {
	if m := toolchainDirectiveRE.FindStringSubmatch(body); m != nil {
		if m[1] == want {
			return body, false
		}
		return toolchainDirectiveRE.ReplaceAllString(body, "toolchain "+want), true
	}

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !goDirectiveRE.MatchString(line) {
			continue
		}
		rest := append([]string{"", "toolchain " + want}, lines[i+1:]...)
		return strings.Join(append(lines[:i+1:i+1], rest...), "\n"), true
	}
	return body, false
}

// meetsFloor reports whether version is at or above floor, compared by numeric
// part rather than as text — go1.26.10 is above go1.26.6 and a string compare
// says otherwise. A version this
// cannot parse answers false, so an unrecognized go.mod gets the directive
// rather than being silently judged current.
func meetsFloor(version, floor string) bool {
	have, ok := goVersionParts(version)
	if !ok {
		return false
	}
	want, ok := goVersionParts(floor)
	if !ok {
		return false
	}
	for i := range want {
		switch {
		case have[i] > want[i]:
			return true
		case have[i] < want[i]:
			return false
		}
	}
	return true
}

func goVersionParts(version string) ([3]int, bool) {
	var parts [3]int
	fields := strings.Split(strings.TrimPrefix(version, "go"), ".")
	if len(fields) == 0 || len(fields) > 3 {
		return parts, false
	}
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}
