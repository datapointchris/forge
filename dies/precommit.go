package dies

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/precommit"
	"github.com/datapointchris/forge/reconcile"
)

const preCommitConfigPath = ".pre-commit-config.yaml"

// PreCommit generates .pre-commit-config.yaml and deploys the tool configs the
// generated hooks read.
//
// Which blocks apply, and where each one runs, come from the repo's declared
// components — the same source CI reads, never a filesystem probe. Detection
// was wrong in both directions: `-f frontend/package.json` missed every repo
// whose frontend is in web/ or client/, and it cannot say which directory a
// stack lives in.
//
// It does not merge pyproject.toml. That is the `pyproject` die, kept separate
// because coupling a cheap settings change to a whole toolchain rollout is what
// stops the cheap change being made.
//
// Absorbs has-pre-commit, checks/pre-commit-config and `precommit check`. The
// default_stages finding the last of those made is structural now: the
// generated config sets it, so a repo missing it is simply drift against the
// generated text.
type PreCommit struct{}

func (PreCommit) Name() string { return "precommit" }

func (PreCommit) Description() string {
	return "Generate .pre-commit-config.yaml from the standard blocks for the declared components, deploy the tool configs its hooks read, and — where the registry declares the repo — install the git hooks for every stage it uses."
}

func (PreCommit) Tags() []string {
	return []string{"pre-commit", "standardization", "golden-path"}
}

// toolConfig is one file deployed whole beside the generated config.
type toolConfig struct {
	// asset is the path within the embedded pre-commit tree.
	asset string
	// rel is where it lands in the repo.
	rel string
	// category gates it; empty means every repo.
	category string
	// supersedes are the spellings of this config that the tool reading it
	// resolves ahead of rel, nearest first, or empty where there are none.
	//
	// A list rather than one name, because these tools search an ordered list
	// of filenames rather than a single one. Any higher-ranked file left in
	// place governs, and forge's config then sits unread with every verb
	// reporting the repo converged. Two active repos hold such a file: one a
	// .markdownlint.jsonc, one a bare .prettierrc.
	supersedes []string
}

// Deployed to every repo, or gated on a declared category. The three generic
// ones are generic because the shell block is: a repo that got the hook without
// .editorconfig would be formatted at shfmt's tab default, and one without
// .shellcheckrc would follow sourced files it cannot resolve.
//
// Every one is a format with a comment syntax, so every one carries managedMark
// on its first line. markdownlint and prettier are configured in YAML rather
// than JSON for that reason alone: JSON has no comment, and a marker key is not
// a substitute — markdownlint ignores an unknown key silently but prettier
// prints `Ignored unknown option` to stderr on every run.
var toolConfigs = []toolConfig{
	{
		asset: "configs/markdownlint.yml", rel: ".markdownlint.yaml",
		supersedes: []string{".markdownlint.jsonc", ".markdownlint.json"},
	},
	{asset: "configs/editorconfig.ini", rel: ".editorconfig"},
	{asset: "configs/shellcheckrc.ini", rel: ".shellcheckrc"},
	{asset: "configs/golangci.yml", rel: ".golangci.yml", category: "go"},
	{asset: "configs/rustfmt.toml", rel: "rustfmt.toml", category: "rust"},
	{
		asset: "configs/prettierrc.yml", rel: ".prettierrc.yaml", category: "vue",
		supersedes: []string{".prettierrc", ".prettierrc.json"},
	},
	{asset: "configs/prettierignore.txt", rel: ".prettierignore", category: "vue"},
	{asset: "configs/sqlfluff.ini", rel: ".sqlfluff", category: "sql"},
}

// hookStages are the stages a generated config can use. Every one it names has
// to be installed, or those hooks silently never run — a config declaring
// commit-msg without the git hook in place looks correct and does nothing.
//
// A stage no block declares does not belong here: installHooks passes the whole
// list to `pre-commit install -t`, so an entry nothing can reach still writes a
// git hook into every repo.
var hookStages = []string{"pre-commit", "commit-msg"}

type preCommitState struct {
	basis        maintenance
	config       generatedFile
	configs      []generatedFile
	missingHooks []string
	blockers     []reconcile.Change
}

func (s preCommitState) Summary() string {
	if !s.basis.applicable() {
		// The only blockers reachable here are the stranded tool configs, and
		// the plain reason contradicts them: it says forge wrote nothing to
		// maintain while three files it deploys are sitting there.
		// Named with the verb that shows them, not with their paths. This row
		// renders under plan, which keeps Automatic repairs and so has nothing
		// to offer here — stating the fault without the way onward leaves a
		// converged row that reads as broken.
		if len(s.blockers) > 0 {
			return fmt.Sprintf("declares no toolchain, and %s forge deploys sit here with no stamped %s beside them; `check` names them",
				plural(len(s.blockers), "file", "files"), preCommitConfigPath)
		}
		return s.basis.reason()
	}
	return fmt.Sprintf("pre-commit current (%s deployed)", plural(len(s.configs)+1, "file", "files"))
}

// maintenance is why this die has something to say about a repo, or why it has
// nothing.
//
// One value rather than a run of bools. The answer it replaced returned two
// adjacent bools distinguished only by position, so transposing them at a
// return statement compiled clean and passed go vet.
type maintenance int

const (
	// unmaintained: the registry declares nothing and forge has left no config
	// here to keep right.
	unmaintained maintenance = iota

	// unstamped: a .pre-commit-config.yaml is here without forge's stamp, so
	// forge did not write it and will not take it.
	unstamped

	// byDeclaration: the registry declares the repo's components. The answer
	// for a repo somebody works in.
	byDeclaration

	// byStamp: forge wrote the config and stamped it, and the registry has gone
	// quiet about the repo. Forge wrote the file, so forge owns keeping it
	// right, and a registry silent about which stacks are there has not
	// unwritten it.
	byStamp
)

func (m maintenance) applicable() bool { return m == byDeclaration || m == byStamp }

// reason is what the row says when this die has nothing to do.
//
// Two states, not one. Declaring the repo in the first deploys the standard
// files; declaring it in the second deploys nothing and surfaces the unmarked
// hooks instead, so one sentence covering both sends half the readers at the
// wrong remedy.
func (m maintenance) reason() string {
	switch m {
	case unmaintained:
		return "declares no toolchain, and forge has written nothing here to maintain"
	case unstamped:
		return "declares no toolchain, and the " + preCommitConfigPath + " here carries no " +
			toolchainStamp + " stamp, so forge did not write it"
	case byDeclaration, byStamp:
	}
	return ""
}

// maintained decides whether this die has anything to say about a repo, and
// what it should generate if so.
//
// Asking the declaration alone would strand every repo forge has written to but
// does not track, holding whatever it last generated, with every verb reporting
// converged because nothing looked.
//
// An undeclared repo is generated as if it declared no components, which is the
// existing spelling a declaration of no components already has. That is not the
// same as "the generic blocks only": Generate seeds the git block from
// versioned and the python-scripts block from the shebang scan, neither of
// which is gated on a component. A stack the registry stopped naming is not
// guessed at from what happens to be on disk.
//
// First deployment is untouched. Without a stamp there is no file, so a repo
// forge has never written to stays untouched however it is reached — which is
// what keeps the reasoning in runner.ActiveRepos intact.
//
// byStamp is what the caller narrows on. A stamp says forge wrote these files;
// it says nothing about hooks forge never installed, and installing them would
// put a commit gate on a repo nobody works in and build every hook environment
// the config forge last wrote there names.
func maintained(declared *config.Toolchain, existing string) (*config.Toolchain, maintenance) {
	if declared != nil {
		return declared, byDeclaration
	}
	if existing == "" {
		return nil, unmaintained
	}
	if !strings.HasPrefix(existing, toolchainStamp) {
		return nil, unstamped
	}
	return &config.Toolchain{}, byStamp
}

// strandedToolConfigs reports the generic tool configs left behind when the one
// file carrying forge's stamp is gone.
//
// The stamp authorizing maintenance of four files lives in only one of them.
// Delete .pre-commit-config.yaml and the other three return to exactly what
// this die exists to end: forge wrote them, no verb can reach them, and every
// one reports converged because nothing looked.
//
// All three present or nothing, which is what makes this affordable with no
// stamp to read. Forge deploys the three together, so a repo holding the whole
// set is one forge wrote to, while a repo keeping its own .editorconfig alone
// is not. That evidence is weaker than a stamp and is allowed to be, because
// what it authorizes is a report — the finding is ByHand, so apply cannot reach
// it and nothing here is written, overwritten or removed.
func strandedToolConfigs(root string, basis maintenance) []reconcile.Change {
	// Only where no config is present at all. An unstamped one means the repo
	// has a pre-commit setup of its own, and these files are plausibly part of
	// it rather than forge's leftovers.
	if basis != unmaintained {
		return nil
	}

	// Any spelling counts, not just the one forge writes now. Almost every repo
	// in the portfolio still holds a superseded spelling, and looking only for
	// the current one would take this finding silent across all of them.
	var generic []string
	for _, tool := range toolConfigs {
		if tool.category != "" {
			continue
		}
		found := ""
		for _, rel := range append([]string{tool.rel}, tool.supersedes...) {
			if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
				found = rel
				break
			}
		}
		if found == "" {
			return nil
		}
		generic = append(generic, found)
	}

	var changes []reconcile.Change
	for _, rel := range generic {
		changes = append(changes, blocker(rel, "forge deploys this beside "+preCommitConfigPath+
			", and that file is gone, so nothing left here carries the "+toolchainStamp+
			" stamp that says whether forge wrote it — declare the repo's toolchain to have forge maintain it again, or remove it"))
	}
	return changes
}

// unattributable reports whether a file at a managed path can be shown to be
// neither forge's nor safe to replace.
//
// A marker settles it outright. Without one there is still something forge can
// prove: a file whose settings match the template it is about to write is
// forge's own output from before these files were marked, so writing the marker
// in is forge adopting what it already put there rather than taking someone's.
// Every managed file in the portfolio predates the marker, and without that
// second test the first run would report all of them and maintain none.
//
// Settings, not bytes. These templates carry long explanatory comments and those
// get rewritten on their own — the reason 30 .editorconfig files in the
// portfolio differ from the current template is one rewritten comment paragraph,
// with every key identical. A person adjusting a config changes a setting, so
// comparing the settings is what separates the two cases. Every one of these
// formats comments with #, which is the same property that lets them carry the
// marker at all.
//
// Anything else is a file forge cannot account for. It is reported, and the
// finding carries the two ways out.
func unattributable(root, rel, want string) bool {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		// Absent, so there is nothing to attribute and nothing to lose.
		return false
	}
	if !handWritten(root, rel) {
		return false
	}
	return settings(string(data)) != settings(want)
}

// settings is a config body with its comments and blank lines dropped, so two
// bodies compare on what they configure rather than on what they explain.
func settings(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n")
}

// supersededSpelling decides what to do about a config still on disk under the
// spelling this one replaces.
//
// Removable when its content means the same as what forge is writing at the new
// path. That is a weaker claim than "forge wrote this file", and it is the only
// one a removal needs: the same content lands at the new path in the same run,
// so removing the old one loses nothing. No marker can be asked for here, since
// the whole reason for the new spelling is that the old format cannot carry one.
//
// Where the content differs it is reported and left alone. Both tools prefer the
// superseded spelling, so a repo keeping one is running that config rather than
// forge's, and only a person can say which was meant.
func supersededSpelling(root, spelling, rel, want string) (generatedFile, *reconcile.Change) {
	data, err := os.ReadFile(filepath.Join(root, spelling))
	if err != nil {
		return generatedFile{}, nil
	}

	if same, err := sameConfig(data, []byte(want)); err != nil || !same {
		finding := blocker(spelling, "the tool that reads it resolves this spelling ahead of "+
			rel+", and its content differs from what forge writes there, so forge's config is "+
			"not the one in force — reconcile the two by hand")
		return generatedFile{}, &finding
	}
	// want is empty, which is what makes this a retraction rather than a write.
	return generatedFile{rel: spelling, have: string(data), exists: true}, nil
}

// replacementInPlace reports whether the config that supersedes this spelling is
// on disk and carries what forge writes there.
//
// The removal's whole justification is that the same content lands at the new
// path in the same run. reconcile.Apply continues past a failed change, so
// without this a write that failed followed by a removal that succeeded leaves
// the repo with no config for that tool at all — and the next check reports it
// converged, because the superseded file is gone and the new one is pending.
//
// Read from disk here rather than trusted from the plan, for the same reason
// every other Perform precondition is: the plan was printed before apply ran.
func replacementInPlace(root string, state preCommitState, spelling string) bool {
	for _, tool := range toolConfigs {
		if !slices.Contains(tool.supersedes, spelling) {
			continue
		}
		for _, file := range state.configs {
			if file.rel == tool.rel {
				return readGenerated(root, tool.rel, file.want).matches()
			}
		}
	}
	return false
}

// sameConfig reports whether two config bodies carry the same data.
//
// yaml.v3 reads both spellings, JSON being a subset of YAML, so the comparison
// needs one parser rather than one per format.
func sameConfig(a, b []byte) (bool, error) {
	var left, right any
	if err := yaml.Unmarshal(a, &left); err != nil {
		return false, err
	}
	if err := yaml.Unmarshal(b, &right); err != nil {
		return false, err
	}
	return reflect.DeepEqual(left, right), nil
}

func (PreCommit) Observe(t reconcile.Target) (reconcile.Observation, error) {
	root := t.Repo.Path
	blocksFS, err := fs.Sub(t.Assets.PreCommit, "blocks")
	if err != nil {
		return nil, err
	}

	var existing string
	if data, err := os.ReadFile(filepath.Join(root, preCommitConfigPath)); err == nil {
		existing = string(data)
	}

	declared, basis := maintained(t.Repo.Toolchain, existing)
	if !basis.applicable() {
		return preCommitState{basis: basis, blockers: strandedToolConfigs(root, basis)}, nil
	}

	customSections := precommit.ExtractCustomSections(existing)

	scripts := shebangScripts(root, t.Versioned())

	wanted, err := precommit.Generate(blocksFS, t.Assets.Manifest, declared, customSections, t.Versioned(), scripts)
	if err != nil {
		return nil, err
	}

	state := preCommitState{basis: basis, config: readGenerated(root, preCommitConfigPath, wanted)}

	// Unmarked hooks abort a real sync rather than being destroyed. Surfacing
	// them is the whole point: the fix is adding markers, not letting the sync
	// take them.
	if existing != "" {
		unknown, err := precommit.SafetyCheck(existing, blocksFS, declared, customSections)
		if err != nil {
			return nil, err
		}
		if len(unknown) > 0 {
			state.blockers = append(state.blockers, blocker(preCommitConfigPath,
				fmt.Sprintf("%s need a # > custom: marker or a sync would delete them: %s",
					plural(len(unknown), "hook", "hooks"), strings.Join(unknown, ", "))))
		}
	}

	categories := declaredCategories(declared)
	for _, tool := range toolConfigs {
		if tool.category != "" && !slices.Contains(categories, tool.category) {
			continue
		}
		content, err := fs.ReadFile(t.Assets.PreCommit, tool.asset)
		if err != nil {
			return nil, fmt.Errorf("reading embedded %s: %w", tool.asset, err)
		}
		want := string(content)
		if tool.rel == ".shellcheckrc" {
			want += precommit.ShellcheckDisables(declared)
		}

		// A file at a managed path carrying none of forge's markers is a
		// person's, unless it is forge's own output from before these files
		// were marked. Reported rather than repaired, so check names it and
		// apply cannot reach it. Asked per file: one hand-written config is no
		// reason to stop deploying the others.
		if unattributable(root, tool.rel, want) {
			state.blockers = append(state.blockers, blocker(tool.rel,
				"exists without the "+managedMark+" marker and does not match what forge deploys, so forge "+
					"cannot tell its own earlier output from a file someone wrote — reconcile it by hand, or "+
					"delete it and re-apply to take the standard"))
			continue
		}
		state.configs = append(state.configs, readGenerated(root, tool.rel, want))

		for _, spelling := range tool.supersedes {
			old, finding := supersededSpelling(root, spelling, tool.rel, want)
			if finding != nil {
				state.blockers = append(state.blockers, *finding)
			}
			if old.managed() {
				state.configs = append(state.configs, old)
			}
		}
	}

	// Measured whatever the basis is. A stamp does not authorize installing a
	// hook forge never installed, and it does not make an uninstalled stage
	// stop mattering either: the config names the stage, so the hooks it names
	// silently never run. Not measuring it let apply write that config and plan
	// then report converged over the state hookStages above defines as broken.
	// Diff decides who may fix it; this decides whether anyone is told.
	state.missingHooks = uninstalledHooks(root, wanted)

	// A schema-valid config can still fail on the first commit, which is the
	// check `pre-commit validate-config` cannot make.
	for _, missing := range unresolvedNpmScripts(root, wanted) {
		state.blockers = append(state.blockers, blocker(preCommitConfigPath, "hook would fail on first use: "+missing))
	}
	if finding := validateConfig(wanted); finding != "" {
		state.blockers = append(state.blockers, blocker("generated config", "pre-commit validate-config: "+finding))
	}

	return state, nil
}

// declaredCategories is the block categories a repo's components pull in — the
// same answer `forge precommit stacks` gave the die scripts.
func declaredCategories(declared *config.Toolchain) []string {
	var categories []string
	for _, component := range declared.Components {
		if category := precommit.StackToCategory(component.Stack); !slices.Contains(categories, category) {
			categories = append(categories, category)
		}
	}
	// Not a component: a declared dialect is what pulls in the SQL block, and
	// its ruleset is deployed on the same signal.
	if declared.SQLDialect != "" {
		categories = append(categories, "sql")
	}
	return categories
}

// stageList matches a `stages: [...]` or `default_stages: [...]` declaration.
//
// Parsed rather than substring-matched. "commit-msg" is a substring of
// "prepare-commit-msg", so a bare Contains reported the commit-msg hook missing
// in every repo that declared only the prepare one — a false finding in 37
// repos, found by running this against the portfolio.
var stageList = regexp.MustCompile(`stages:\s*\[([^\]]*)\]`)

// declaredStages is every stage the generated config names.
func declaredStages(generated string) []string {
	var stages []string
	for _, match := range stageList.FindAllStringSubmatch(generated, -1) {
		for _, name := range strings.Split(match[1], ",") {
			if name = strings.TrimSpace(name); name != "" && !slices.Contains(stages, name) {
				stages = append(stages, name)
			}
		}
	}
	return stages
}

// uninstalledHooks returns the stages the config uses whose git hook is absent.
func uninstalledHooks(root, generated string) []string {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}

	used := declaredStages(generated)

	var missing []string
	for _, stage := range hookStages {
		if !slices.Contains(used, stage) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, ".git", "hooks", stage))
		if err != nil || !strings.Contains(string(data), "pre-commit") {
			missing = append(missing, stage)
		}
	}
	return missing
}

// npmRunHook matches a strict `npm run X` the generator emits. --if-present
// invocations are deliberately optional and do not count.
var npmRunHook = regexp.MustCompile(`cd ([^ ]+) && npm run ([a-z][a-z:._-]*)`)

func unresolvedNpmScripts(root, generated string) []string {
	var missing []string
	seen := map[string]bool{}

	for _, match := range npmRunHook.FindAllStringSubmatch(generated, -1) {
		dir, script := match[1], match[2]
		if key := dir + ":" + script; seen[key] {
			continue
		} else {
			seen[key] = true
		}

		data, err := os.ReadFile(filepath.Join(root, dir, "package.json"))
		if err != nil {
			missing = append(missing, dir+" has no package.json")
			continue
		}
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			missing = append(missing, dir+"/package.json is unreadable")
			continue
		}
		if _, ok := manifest.Scripts[script]; !ok {
			missing = append(missing, fmt.Sprintf("%s declares no %q script", dir, script))
		}
	}
	return missing
}

func validateConfig(generated string) string {
	dir, err := os.MkdirTemp("", "forge-precommit-")
	if err != nil {
		return ""
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, preCommitConfigPath)
	if err := os.WriteFile(path, []byte(generated), 0o644); err != nil {
		return ""
	}
	return runValidator(dir, "pre-commit", "validate-config", path)
}

func (PreCommit) Diff(_ reconcile.Target, observed reconcile.Observation) ([]reconcile.Change, error) {
	state, ok := observed.(preCommitState)
	if !ok {
		return nil, fmt.Errorf("precommit: unexpected observation %T", observed)
	}
	if !state.basis.applicable() {
		return state.blockers, nil
	}

	changes := state.blockers

	// An unmarked hook aborts the sync, so the config is not offered as an
	// automatic repair while one is outstanding — the plan would otherwise
	// promise a write that Perform refuses.
	if !hasItem(state.blockers, preCommitConfigPath) {
		if change, drifted := state.config.change("regenerate from the standard blocks"); drifted {
			changes = append(changes, change)
		}
	}

	// Writes first, retractions after. apply performs them in this order, so a
	// run interrupted between the two leaves the superseded config still on
	// disk and still in force — which is the state the repo was already in,
	// rather than a repo with no config for that tool at all.
	for _, file := range state.configs {
		if file.retracting() {
			continue
		}
		if change, drifted := file.change("deploy the standard tool config"); drifted {
			changes = append(changes, change)
		}
	}
	for _, file := range state.configs {
		if !file.retracting() {
			continue
		}
		if change, drifted := file.change("the tool reads this spelling in preference, so it comes out"); drifted {
			changes = append(changes, change)
		}
	}

	// Missing either way, because the hook is genuinely absent. Who may fix it
	// is what the basis decides: a declaration authorizes the install, a stamp
	// authorizes correcting what forge wrote and nothing more. ByHand keeps it
	// out of plan and unreachable from Apply while check still names it.
	for _, stage := range state.missingHooks {
		change := reconcile.Change{
			Item:    ".git/hooks/" + stage,
			Verdict: reconcile.Missing,
			Repair:  reconcile.Automatic,
			Detail:  "the config uses this stage, so without the hook those checks silently never run",
		}
		if state.basis == byStamp {
			change.Repair = reconcile.ByHand
			change.Detail = "the config forge maintains here uses this stage and the hook is absent, so those checks silently never run" +
				" — declare the repo's toolchain to have forge install it, or run `pre-commit install -t " + stage + "` here"
		}
		changes = append(changes, change)
	}

	return changes, nil
}

func (p PreCommit) Perform(t reconcile.Target, change reconcile.Change) (reconcile.Outcome, error) {
	if !change.Actionable() {
		return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "only a person can settle this one"}, nil
	}

	observed, err := p.Observe(t)
	if err != nil {
		return reconcile.Outcome{}, err
	}
	state := observed.(preCommitState)

	if strings.HasPrefix(change.Item, ".git/hooks/") {
		return installHooks(t.Repo.Path, change)
	}

	if change.Item == preCommitConfigPath {
		if hasItem(state.blockers, preCommitConfigPath) {
			return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "an unmarked custom hook appeared since the plan"}, nil
		}
		return writeIfStale(t.Repo.Path, state.config, change)
	}

	for _, file := range state.configs {
		if file.rel != change.Item {
			continue
		}
		if hasItem(state.blockers, file.rel) {
			return reconcile.Outcome{
				Change: change, Status: reconcile.Refused,
				Message: "a hand-written " + file.rel + " appeared since the plan",
			}, nil
		}
		// Checked before the write, because an empty want is exactly what a
		// retraction has and writeIfStale would read it as a file to blank.
		if file.retracting() {
			if !replacementInPlace(t.Repo.Path, state, file.rel) {
				return reconcile.Outcome{
					Change: change, Status: reconcile.Refused,
					Message: "the config that replaces " + file.rel + " is not on disk, so removing it " +
						"would leave the repo with no config for that tool",
				}, nil
			}
			if err := removeGenerated(t.Repo.Path, file); err != nil {
				return reconcile.Outcome{}, err
			}
			return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "removed " + file.rel}, nil
		}
		return writeIfStale(t.Repo.Path, file, change)
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Refused, Message: "no longer part of the standard"}, nil
}

func writeIfStale(root string, file generatedFile, change reconcile.Change) (reconcile.Outcome, error) {
	if file.matches() {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already current"}, nil
	}
	if err := writeGenerated(root, file); err != nil {
		return reconcile.Outcome{}, err
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "wrote " + file.rel}, nil
}

// installHooks installs every stage at once. `pre-commit install` is not
// per-stage cheap, and the stages are decided by one config, so the first
// change of a batch carries its siblings and they report Skipped.
func installHooks(root string, change reconcile.Change) (reconcile.Outcome, error) {
	stage := strings.TrimPrefix(change.Item, ".git/hooks/")
	if data, err := os.ReadFile(filepath.Join(root, ".git", "hooks", stage)); err == nil && strings.Contains(string(data), "pre-commit") {
		return reconcile.Outcome{Change: change, Status: reconcile.Skipped, Message: "already installed"}, nil
	}

	args := []string{"install", "--install-hooks"}
	for _, name := range hookStages {
		args = append(args, "-t", name)
	}
	if _, err := runIn(root, "pre-commit", args...); err != nil {
		return reconcile.Outcome{Change: change, Status: reconcile.Failed, Message: err.Error()}, nil
	}
	return reconcile.Outcome{Change: change, Status: reconcile.Done, Message: "installed"}, nil
}

// shebangScripts is every tracked file the generated Python hooks would skip:
// no extension, and a shebang running Python through something identify does not
// recognize as a Python interpreter.
//
// Detected rather than declared, against this repo's usual direction and for the
// reason ReleaseGatesOnValidate detects its trigger. The failure being prevented
// is a new app landing beside the existing ones and nobody remembering to
// declare it, which is the same silent gap the block closes — a registry key
// cannot prevent that, and reading a first line cannot get it wrong. The scan
// re-derives on every generate, so adding or renaming an app fixes the config by
// itself and the drift shows up as an ordinary pending change.
//
// git's index is the enumeration. An unversioned target has no bounded one, and
// a maintained directory can be a home directory — walking it on every check is
// not a cost this finding is worth, so those targets return nothing.
func shebangScripts(root string, versioned bool) []string {
	if !versioned {
		return nil
	}
	out, err := runIn(root, "git", "ls-files", "--cached")
	if err != nil || out == "" {
		return nil
	}

	var scripts []string
	for _, rel := range strings.Split(out, "\n") {
		if rel == "" || filepath.Ext(rel) != "" {
			continue
		}
		firstLine := firstLineOf(filepath.Join(root, rel))
		if precommit.UntaggedPythonScript(rel, firstLine) {
			scripts = append(scripts, rel)
		}
	}
	slices.Sort(scripts)
	return scripts
}

// firstLineOf reads only far enough to hold a shebang, so the scan costs one
// short read per extensionless file rather than one whole file.
func firstLineOf(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	buf := make([]byte, 256)
	n, _ := file.Read(buf)
	line, _, _ := strings.Cut(string(buf[:n]), "\n")
	return strings.TrimRight(line, "\r")
}
