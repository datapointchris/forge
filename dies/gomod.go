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
// Measured against a system Go one patch below the pin. A module floored above
// it to clear standard-library advisories was skipped: `go install @latest`
// returned 0 in a quarter of a second and left the old binary in place. A module
// taking a toolchain directive against the same advisories worked — govulncheck
// came back clean,
// `go list` still reported the module at 1.26.5, `GOTOOLCHAIN=local go build`
// still worked, and the release installed on that same machine.
//
// So this die never writes the `go` line. Raising a floor is a compatibility
// decision about who may consume the module, and it belongs to whoever owns
// the module rather than to a fleet-wide sweep.
//
// Each `FROM golang:` line in a Dockerfile beside a go.mod takes the release
// CI tests. The official image sets GOTOOLCHAIN=local and ignores go.mod's
// toolchain line, so the tag alone decides what compiles the shipped binary.
// A floating golang:alpine follows Go's newest release, which no CI run tested.
type GoMod struct{}

func (GoMod) Name() string { return "gomod" }

func (GoMod) Description() string {
	return "Pin the Go toolchain each module builds with, from the manifest's runtime version, in its go.mod and in the golang image its Dockerfiles build from. Never touches the `go` directive, which is a consumer's floor rather than this build's toolchain."
}

func (GoMod) Tags() []string {
	return []string{"go", "toolchain", "docker", "govulncheck", "standardization", "golden-path"}
}

var (
	// Horizontal whitespace only. `\s*$` is greedy and `\s` includes a newline,
	// so a ReplaceAllString with it swallows the line ending and silently
	// strips the file's trailing newline.
	goDirectiveRE        = regexp.MustCompile(`(?m)^go[ \t]+(\S+)[ \t]*$`)
	toolchainDirectiveRE = regexp.MustCompile(`(?m)^toolchain[ \t]+(\S+)[ \t]*$`)

	// golangFromRE is a FROM naming the official golang image. The groups are
	// everything up to the image name, the tag, a digest, and the rest of the
	// line. golangci/golangci-lint, and any image whose name only starts with
	// golang, fails at the character after it.
	golangFromRE = regexp.MustCompile(`(?im)^([ \t]*FROM[ \t]+(?:--platform=\S+[ \t]+)?(?:docker\.io/)?(?:library/)?golang)(?::([^\s@]+))?(@\S+)?((?:[ \t].*)?)$`)

	// goImageReleaseRE is the Go release at the front of a golang tag, ahead of
	// the variant: 1.26.9 in 1.26.9-alpine, 1.26 in 1.26-alpine3.22.
	goImageReleaseRE = regexp.MustCompile(`^\d+(?:\.\d+)*(?:rc\d+)?`)
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
	// dockerfiles holds the Dockerfiles in the component directory that build
	// from golang. A Dockerfile elsewhere in the repo that builds this module
	// is not read.
	dockerfiles []dockerfile
}

type dockerfile struct {
	rel  string
	body string
}

type gomodState struct {
	modules []goModule
	// floor and pinned are the declaration's two Go numbers. Empty means the
	// declaration says nothing, and a die with nothing to assert reports
	// converged rather than inventing a version.
	floor  string
	pinned string
}

func (s gomodState) Summary() string {
	if s.pinned == "" {
		return "no Go runtime pinned in the manifest"
	}
	if len(s.modules) == 0 {
		return "no Go modules declared"
	}
	var images int
	for _, module := range s.modules {
		images += len(module.dockerfiles)
	}
	if images == 0 {
		return fmt.Sprintf("toolchain current (%s)", plural(len(s.modules), "module", "modules"))
	}
	return fmt.Sprintf("toolchain current (%s, %s)", plural(len(s.modules), "module", "modules"), plural(images, "Dockerfile", "Dockerfiles"))
}

func (GoMod) Observe(t reconcile.Target) (reconcile.Observation, error) {
	state := gomodState{}
	if m := t.Assets.Manifest; m != nil {
		if lang, declared := m.LanguageFor("go"); declared {
			state.floor, state.pinned = lang.Floor, lang.Toolchain
		}
		// A manifest declaring no languages — the YAML test fixture — carries
		// runtimes alone, so it still pins a toolchain and asserts no floor.
		if state.pinned == "" {
			if version, managed := m.RuntimeVersion("go"); managed {
				state.pinned = version
			}
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
		if module.exists {
			if module.dockerfiles, err = readDockerfiles(t, rel); err != nil {
				return nil, err
			}
		}
		state.modules = append(state.modules, module)
	}
	return state, nil
}

// readDockerfiles returns the Dockerfiles in one component directory that have
// a golang FROM line. Any other Dockerfile is left out, so the summary does not
// count it.
func readDockerfiles(t reconcile.Target, rel string) ([]dockerfile, error) {
	entries, err := os.ReadDir(t.Path(rel))
	if err != nil {
		return nil, err
	}
	var found []dockerfile
	for _, entry := range entries {
		if entry.IsDir() || !isDockerfileName(entry.Name()) {
			continue
		}
		item := filepath.Join(rel, entry.Name())
		body, err := os.ReadFile(t.Path(item))
		if err != nil {
			return nil, err
		}
		if golangFromRE.Match(body) {
			found = append(found, dockerfile{rel: item, body: string(body)})
		}
	}
	return found, nil
}

// isDockerfileName matches the three spellings docker build takes by
// convention: Dockerfile, Dockerfile.<name> and <name>.Dockerfile.
// Dockerfile.dockerignore fits the second and is BuildKit's ignore file, so it
// is excluded.
func isDockerfileName(name string) bool {
	if strings.HasSuffix(name, ".dockerignore") {
		return false
	}
	return name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") || strings.HasSuffix(name, ".Dockerfile")
}

func (GoMod) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(gomodState)
	if !ok {
		return nil, fmt.Errorf("gomod: unexpected observation %T", observed)
	}
	if state.floor == "" && state.pinned == "" {
		return nil, nil
	}

	var changes []reconcile.Change
	for _, module := range state.modules {
		if !module.exists {
			continue
		}
		changes = append(changes, moduleChanges(state, module)...)
		changes = append(changes, imageChanges(state, module)...)
	}
	return changes, nil
}

// convergedFloor is the `go` directive a module holds once this die has run.
func convergedFloor(state gomodState, goVersion string) string {
	if state.floor != "" {
		return state.floor
	}
	return goVersion
}

// owedToolchain is the toolchain directive this die writes into a module, or ""
// where the converged floor already reaches the pin. moduleChanges, Perform and
// buildGo all ask it, so go.mod and the image cannot disagree.
func owedToolchain(state gomodState, goVersion string) string {
	if state.pinned == "" || meetsFloor(convergedFloor(state, goVersion), state.pinned) {
		return ""
	}
	return "go" + state.pinned
}

// buildGo is the Go release a module's CI tests once its go.mod has converged,
// and so the release its image builds with. setup-go installs the toolchain
// directive where go.mod has one and the `go` directive otherwise. This reads
// the converged go.mod the same way.
func buildGo(state gomodState, module goModule) string {
	toolchain := owedToolchain(state, module.goVersion)
	if toolchain == "" {
		// A toolchain line the floor has overtaken is reported, never removed,
		// and setup-go reads it while it is there.
		toolchain = module.toolchain
	}
	if toolchain != "" {
		return strings.TrimPrefix(toolchain, "go")
	}
	return convergedFloor(state, module.goVersion)
}

// moduleChanges decides one go.mod, floor first and toolchain second, because
// the toolchain is only wanted where the floor leaves the build on a standard
// library the pin has moved past.
func moduleChanges(state gomodState, module goModule) []reconcile.Change {
	item := filepath.Join(module.rel, "go.mod")
	var changes []reconcile.Change

	if state.floor != "" && module.goVersion != state.floor {
		// Both directions are drift. A repo above the floor is the case that
		// strands a release: `go install <tool>@latest` prefers something the
		// installing machine can build, so a floor nobody asked for is skipped
		// there without a word.
		detail := "the declaration floors Go at " + state.floor
		if meetsFloor(module.goVersion, state.floor) {
			detail += ", and a floor above it excludes consumers for nothing"
		}
		changes = append(changes, reconcile.Change{
			Item:     item,
			Verdict:  reconcile.Stale,
			Repair:   reconcile.Automatic,
			Detail:   detail,
			Observed: "go " + module.goVersion,
		})
	}

	if state.pinned == "" {
		return changes
	}
	floor := convergedFloor(state, module.goVersion)
	want := owedToolchain(state, module.goVersion)

	switch {
	case want == "":
		// Already building against the pinned standard library, so a toolchain
		// line would be a second copy of the same fact.
		if module.toolchain != "" {
			changes = append(changes, reconcile.Change{
				Item:     item,
				Verdict:  reconcile.Undeclared,
				Repair:   reconcile.NoRepair,
				Detail:   fmt.Sprintf("the floor %s already reaches the pinned toolchain", floor),
				Observed: "toolchain " + module.toolchain,
			})
		}
	case module.toolchain == "":
		changes = append(changes, reconcile.Change{
			Item:     item,
			Verdict:  reconcile.Missing,
			Repair:   reconcile.Automatic,
			Detail:   fmt.Sprintf("builds with the %s standard library; the declaration pins %s", floor, state.pinned),
			Observed: "go " + module.goVersion,
		})
	case module.toolchain != want:
		changes = append(changes, reconcile.Change{
			Item:     item,
			Verdict:  reconcile.Stale,
			Repair:   reconcile.Automatic,
			Detail:   "the declaration pins " + state.pinned,
			Observed: "toolchain " + module.toolchain,
		})
	}
	return changes
}

// imageChanges decides each Dockerfile beside one go.mod. A change names a
// file, so a multi-stage build naming golang twice is one change.
func imageChanges(state gomodState, module goModule) []reconcile.Change {
	want := buildGo(state, module)
	if want == "" {
		return nil
	}
	var changes []reconcile.Change
	for _, image := range module.dockerfiles {
		if change, drifted := imageChange(image, want); drifted {
			changes = append(changes, change)
		}
	}
	return changes
}

func imageChange(image dockerfile, want string) (reconcile.Change, bool) {
	var stale string
	for _, m := range golangFromRE.FindAllStringSubmatch(image.body, -1) {
		line, tag, digest := strings.TrimSpace(m[0]), m[2], m[3]
		next, published := goImageTag(tag, want)
		if next == tag {
			continue
		}
		// A digest outranks the tag beside it, and whoever runs the build fills
		// a build argument. Rewriting the tag reaches neither. goImageTag says
		// why a distro-release variant is left alone too.
		var reason string
		switch {
		case digest != "":
			reason = "pinned by a digest whose Go cannot be read offline"
		case strings.Contains(tag, "$"):
			reason = "takes its tag from a build argument"
		case !published:
			reason = "names a distro release, which may have no " + next + " tag"
		}
		if reason != "" {
			return reconcile.Change{
				Item: image.rel, Verdict: reconcile.Stale, Repair: reconcile.ByHand,
				Detail: reason + "; CI tests Go " + want, Observed: line,
			}, true
		}
		if stale == "" {
			stale = line
		}
	}
	if stale == "" {
		return reconcile.Change{}, false
	}
	return reconcile.Change{
		Item:     image.rel,
		Verdict:  reconcile.Stale,
		Repair:   reconcile.Automatic,
		Detail:   "CI tests Go " + want + ", and the official image ignores go.mod's toolchain line",
		Observed: stale,
	}, true
}

// pinGolangImages sets the Go release in every golang FROM line except one with
// a digest, a build argument or a distro-release variant. The platform flag and
// the stage name are kept.
func pinGolangImages(body, want string) (string, bool) {
	changed := false
	updated := golangFromRE.ReplaceAllStringFunc(body, func(line string) string {
		m := golangFromRE.FindStringSubmatch(line)
		prefix, tag, digest, rest := m[1], m[2], m[3], m[4]
		next, published := goImageTag(tag, want)
		if digest != "" || strings.Contains(tag, "$") || !published || next == tag {
			return line
		}
		changed = true
		return prefix + ":" + next + rest
	})
	return updated, changed
}

// goImageTag is tag with its Go release set to want, and whether that tag is
// sure to exist. alpine becomes 1.26.9-alpine, and a bare release stays bare.
// The official image publishes a new Go release only on its newest distro
// releases, so a variant naming a distro release, such as alpine3.22 or
// bookworm, may have no tag for want.
func goImageTag(tag, want string) (string, bool) {
	if tag == "" || tag == "latest" {
		return want, true
	}
	release := goImageReleaseRE.FindString(tag)
	switch variant := strings.TrimPrefix(tag[len(release):], "-"); variant {
	case "":
		return want, true
	case "alpine":
		return want + "-alpine", true
	default:
		return want + "-" + variant, false
	}
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
	if state.floor == "" && state.pinned == "" {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "the declaration pins no Go version"}, nil
	}
	if filepath.Base(change.Item) != "go.mod" {
		return performImage(t, state, change)
	}

	// Converges the whole module rather than the one directive this change
	// names. A Change carries the file, not which line of it, and a module can
	// drift on both at once — so the second change for one go.mod arrives here
	// after the first already settled it and reports Skipped.
	path := t.Path(change.Item)
	body, err := os.ReadFile(path)
	if err != nil {
		return reconcile.Outcome{}, err
	}

	updated := body
	var did []string
	if state.floor != "" {
		if next, changed := setGoDirective(string(updated), state.floor); changed {
			updated, did = []byte(next), append(did, "floored at "+state.floor)
		}
	}
	// A floor that already reaches the pin needs no toolchain line; where it
	// does not, that line is what carries the fixed standard library.
	if owed := owedToolchain(state, goDirectiveOf(string(updated))); owed != "" {
		if next, changed := setToolchain(string(updated), owed); changed {
			updated, did = []byte(next), append(did, "pinned toolchain "+state.pinned)
		}
	}

	if len(did) == 0 {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already converged"}, nil
	}
	if err := rewrite(path, updated); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: strings.Join(did, ", ")}, nil
}

// performImage rewrites one Dockerfile's golang tags to the Go its module's CI
// tests. buildGo reads only the go.mod values Perform never rewrites: the `go`
// line where no floor is declared, and a toolchain line no pin owes. So the
// image and go.mod agree whichever change applies first.
func performImage(t reconcile.Target, state gomodState, change reconcile.Change) (reconcile.Outcome, error) {
	for _, module := range state.modules {
		for _, image := range module.dockerfiles {
			if image.rel != change.Item {
				continue
			}
			want := buildGo(state, module)
			pinned, changed := pinGolangImages(image.body, want)
			if !changed {
				return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already converged"}, nil
			}
			if err := rewrite(t.Path(image.rel), []byte(pinned)); err != nil {
				return reconcile.Outcome{}, err
			}
			return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "builds with Go " + want}, nil
		}
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "builds from no golang image"}, nil
}

func rewrite(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, info.Mode().Perm())
}

// setGoDirective rewrites the `go` line, in either direction. Lowering a floor
// is safe for every consumer and raising one excludes them, which is why the
// declaration decides it rather than each repo.
func setGoDirective(body, want string) (string, bool) {
	m := goDirectiveRE.FindStringSubmatch(body)
	if len(m) < 2 || m[1] == want {
		return body, false
	}
	return goDirectiveRE.ReplaceAllString(body, "go "+want), true
}

func goDirectiveOf(body string) string {
	if m := goDirectiveRE.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
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
