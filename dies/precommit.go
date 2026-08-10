package dies

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

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
// It no longer merges pyproject.toml. That is the `pyproject` die, split out
// because coupling a cheap settings change to a whole toolchain rollout is why
// the cheap change stopped being made.
//
// Absorbs has-pre-commit, checks/pre-commit-config and `precommit check`. The
// default_stages finding the last of those made is structural now: the
// generated config sets it, so a repo missing it is simply drift against the
// generated text.
type PreCommit struct{}

func (PreCommit) Name() string { return "precommit" }

func (PreCommit) Description() string {
	return "Generate .pre-commit-config.yaml from the standard blocks for the declared components, deploy the tool configs its hooks read, and install the git hooks for every stage it uses."
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
}

// Deployed to every repo, or gated on a declared category. The three generic
// ones are generic because the shell block is: a repo that got the hook without
// .editorconfig would be formatted at shfmt's tab default, and one without
// .shellcheckrc would follow sourced files it cannot resolve.
var toolConfigs = []toolConfig{
	{asset: "configs/markdownlint.json", rel: ".markdownlint.json"},
	{asset: "configs/editorconfig.ini", rel: ".editorconfig"},
	{asset: "configs/shellcheckrc.ini", rel: ".shellcheckrc"},
	{asset: "configs/golangci.yml", rel: ".golangci.yml", category: "go"},
	{asset: "configs/prettierrc.json", rel: ".prettierrc.json", category: "vue"},
	{asset: "configs/sqlfluff.ini", rel: ".sqlfluff", category: "sql"},
}

// hookStages are the stages a generated config can use. Every one it names has
// to be installed, or those hooks silently never run — a config declaring
// prepare-commit-msg without the git hook in place looks correct and does
// nothing.
var hookStages = []string{"pre-commit", "commit-msg", "prepare-commit-msg", "post-commit"}

type preCommitState struct {
	applicable   bool
	reason       string
	config       generatedFile
	configs      []generatedFile
	missingHooks []string
	blockers     []reconcile.Change
}

func (s preCommitState) Summary() string {
	if !s.applicable {
		return s.reason
	}
	return fmt.Sprintf("pre-commit current (%s deployed)", plural(len(s.configs)+1, "file", "files"))
}

func (PreCommit) Observe(t reconcile.Target) (reconcile.Observation, error) {
	if t.Repo.Toolchain == nil {
		return preCommitState{reason: "declares no toolchain"}, nil
	}

	root := t.Repo.Path
	blocksFS, err := fs.Sub(t.Assets.PreCommit, "blocks")
	if err != nil {
		return nil, err
	}

	var existing string
	if data, err := os.ReadFile(filepath.Join(root, preCommitConfigPath)); err == nil {
		existing = string(data)
	}
	customSections := precommit.ExtractCustomSections(existing)

	wanted, err := precommit.Generate(blocksFS, t.Assets.Manifest, t.Repo.Toolchain, customSections, t.Versioned())
	if err != nil {
		return nil, err
	}

	state := preCommitState{applicable: true, config: readGenerated(root, preCommitConfigPath, wanted)}

	// Unmarked hooks abort a real sync rather than being destroyed. Surfacing
	// them is the whole point: the fix is adding markers, not letting the sync
	// take them.
	if existing != "" {
		unknown, err := precommit.SafetyCheck(existing, blocksFS, t.Repo.Toolchain, customSections)
		if err != nil {
			return nil, err
		}
		if len(unknown) > 0 {
			state.blockers = append(state.blockers, blocker(preCommitConfigPath,
				fmt.Sprintf("%s need a # > custom: marker or a sync would delete them: %s",
					plural(len(unknown), "hook", "hooks"), strings.Join(unknown, ", "))))
		}
	}

	categories := declaredCategories(t.Repo.Toolchain)
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
			want += precommit.ShellcheckDisables(t.Repo.Toolchain)
		}
		state.configs = append(state.configs, readGenerated(root, tool.rel, want))
	}

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
	if !state.applicable {
		return nil, nil
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

	for _, file := range state.configs {
		if change, drifted := file.change("deploy the standard tool config"); drifted {
			changes = append(changes, change)
		}
	}

	for _, stage := range state.missingHooks {
		changes = append(changes, reconcile.Change{
			Item:    ".git/hooks/" + stage,
			Verdict: reconcile.Missing,
			Repair:  reconcile.Automatic,
			Detail:  "the config uses this stage, so without the hook those checks silently never run",
		})
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
		if file.rel == change.Item {
			return writeIfStale(t.Repo.Path, file, change)
		}
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
