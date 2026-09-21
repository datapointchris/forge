package precommit

import (
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	hookAliasRE  = regexp.MustCompile(`^\s+alias:\s*(\S+)`)
	hookEntryRE  = regexp.MustCompile(`^\s+entry:\s*(.+)$`)
	hookStagesRE = regexp.MustCompile(`^\s+stages:\s*\[([^\]]*)\]`)
)

// GeneratedHook is one hook a standard block put into a config.
type GeneratedHook struct {
	// Block is the standard block the hook came from.
	Block string
	// Repo is the repo entry it sits under, `local` for a hook with no remote.
	Repo  string
	ID    string
	Alias string
	// Entry is the command a local hook runs, and empty for a remote hook,
	// whose entry is in its own repo.
	Entry string
	// Stages is empty where the hook takes the config's default_stages.
	Stages []string
}

// Selector is what `pre-commit run` selects the hook by. That is the alias
// where one is set, because the bare id also selects every other hook sharing
// it: `pre-commit run ruff-format` runs the scripts hook and the python one.
func (h GeneratedHook) Selector() string {
	if h.Alias != "" {
		return h.Alias
	}
	return h.ID
}

// GeneratedHooks lists the hooks under a config's `# generated:` sections, in
// the order the config runs them. A hook in a custom section is the repo's own
// and is left out, as is any hook the section stripped from a standard block.
func GeneratedHooks(config string) []GeneratedHook {
	var (
		hooks []GeneratedHook
		block string
		repo  string
	)
	for _, line := range strings.Split(config, "\n") {
		if m := generatedRE.FindStringSubmatch(line); m != nil {
			block, repo = m[1], ""
			continue
		}
		if markerRE.MatchString(line) {
			block, repo = "", ""
			continue
		}
		if block == "" {
			continue
		}
		if m := repoLineRE.FindStringSubmatch(line); m != nil {
			repo = m[1]
			continue
		}
		if m := hookIDRE.FindStringSubmatch(line); m != nil {
			hooks = append(hooks, GeneratedHook{Block: block, Repo: repo, ID: m[1]})
			continue
		}
		if len(hooks) == 0 || hooks[len(hooks)-1].Block != block {
			continue
		}
		last := &hooks[len(hooks)-1]
		if m := hookAliasRE.FindStringSubmatch(line); m != nil {
			last.Alias = m[1]
			continue
		}
		if m := hookEntryRE.FindStringSubmatch(line); m != nil {
			last.Entry = scalar(m[1])
			continue
		}
		if m := hookStagesRE.FindStringSubmatch(line); m != nil {
			for _, stage := range strings.Split(m[1], ",") {
				if stage = strings.Trim(strings.TrimSpace(stage), `"'`); stage != "" {
					last.Stages = append(last.Stages, stage)
				}
			}
		}
	}
	return hooks
}

// Shadowed names each standard hook a custom section replaces.
//
// A custom hook sharing a standard hook's id strips that hook from its block,
// so the declared rev and args never reach it and generation reports nothing.
// standardConfig is the config generated without the custom sections, which
// holds exactly the standard hooks this repo gets.
func Shadowed(standardConfig string, customSections map[string]string) []string {
	custom := GetCustomHookIDs(customSections)
	var shadowed []string
	for _, hook := range GeneratedHooks(standardConfig) {
		if custom[hook.ID] && !slices.Contains(shadowed, hook.ID) {
			shadowed = append(shadowed, hook.ID)
		}
	}
	return shadowed
}

// BlockCategory is the declared category that pulls a block in.
//
// A generic block has none and answers with its own name. For the shell block
// that name is also the stack a shell component declares, so the shell job is
// the one whose checks its hooks are held to.
func BlockCategory(block string) string {
	if category, ok := categoryMap[block]; ok {
		return category
	}
	return block
}

// scalar is one line's value as YAML decodes it. A quoted entry loses its
// quotes and `bash -c '...'` keeps the ones the shell needs.
func scalar(raw string) string {
	var value string
	if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
		return strings.TrimSpace(raw)
	}
	return value
}
