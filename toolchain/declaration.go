package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
)

// Language is a language's declared versions.
//
// Floor and Toolchain are different questions and both are declared. The floor
// is the manifest's own version directive — a statement about who may consume
// the repo. The toolchain is what the build compiles with. Collapsing them into
// one number is what strands a release: raising a floor above the Go installed
// on a machine makes `go install <tool>@latest` prefer an older release there,
// silently, returning 0 and leaving the old binary in place.
//
// A language declaring only a floor is normal. Only Go has a toolchain pin,
// because only Go has a directive that separates the two.
type Language struct {
	Floor     string `json:"floor"`
	Toolchain string `json:"toolchain,omitempty"`
	// BindingMinimum is the hard bottom under Floor: the version some pinned
	// tool in the build itself requires. Declared rather than derived, because
	// reading it needs the module proxy and a die that reaches the network to
	// decide one line is a die that fails offline.
	BindingMinimum struct {
		Value string `json:"value"`
	} `json:"binding_minimum,omitempty"`
}

// Minimum is the declared hard bottom under this language's floor, or empty
// when none is declared.
func (l Language) Minimum() string { return l.BindingMinimum.Value }

// declaration is the on-disk shape of the version file. It differs from the
// embedded manifest's YAML deliberately: this file is read by several tools and
// carries its reasoning in fields, because JSON holds no comments.
type declaration struct {
	Stamp struct {
		Version int `json:"version"`
	} `json:"stamp"`
	Languages map[string]json.RawMessage `json:"languages"`
	Hooks     struct {
		Pins []Hook `json:"pins"`
	} `json:"hooks"`
	Actions struct {
		Pins []Action `json:"pins"`
	} `json:"actions"`
	Tools struct {
		Pins []Tool `json:"pins"`
	} `json:"tools"`
	Binaries struct {
		Pins []Binary `json:"pins"`
	} `json:"binaries"`
}

// LoadFile reads the declaration at path into the same shape Load produces, so
// every generator consumes one type whichever source answered.
func LoadFile(path string) (*Toolchain, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var d declaration
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if d.Stamp.Version < 1 {
		return nil, fmt.Errorf("%s: stamp.version must be >= 1, got %d", path, d.Stamp.Version)
	}

	manifest := &Toolchain{
		Version:   d.Stamp.Version,
		Hooks:     d.Hooks.Pins,
		Actions:   d.Actions.Pins,
		Tools:     d.Tools.Pins,
		Binaries:  d.Binaries.Pins,
		Languages: map[string]Language{},
	}

	// Every language entry carries prose alongside its versions, so the two
	// declared fields are read by name rather than by decoding the whole object
	// into a struct that would have to grow a field per explanation.
	for name, raw := range d.Languages {
		var lang Language
		if err := json.Unmarshal(raw, &lang); err != nil {
			// `reason` sits beside the languages as a sibling string rather than
			// as a language, so a value that is not an object is skipped rather
			// than failing the file.
			continue
		}
		if lang.Floor == "" && lang.Toolchain == "" {
			continue
		}
		manifest.Languages[name] = lang
	}

	// Runtimes is what generated CI reads for a `<name>-version:` input. A
	// language's floor is the same number to that consumer, so it is derived
	// here rather than declared twice.
	for name, lang := range manifest.Languages {
		if lang.Floor != "" {
			manifest.Runtimes = append(manifest.Runtimes, Runtime{Name: name, Version: lang.Floor})
		}
	}
	sortRuntimes(manifest.Runtimes)
	return manifest, nil
}

// LanguageFor returns a language's declared versions, and whether it is managed.
func (t *Toolchain) LanguageFor(name string) (Language, bool) {
	lang, ok := t.Languages[name]
	return lang, ok
}
