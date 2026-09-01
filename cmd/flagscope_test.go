package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// walk visits every command in the tree, naming each by what a person types.
func walk(root *cobra.Command, visit func(path string, cmd *cobra.Command)) {
	var descend func(cmd *cobra.Command)
	descend = func(cmd *cobra.Command) {
		visit(strings.TrimPrefix(strings.TrimPrefix(cmd.CommandPath(), "forge"), " "), cmd)
		for _, child := range cmd.Commands() {
			if child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			descend(child)
		}
	}
	descend(root)
}

// commandsCarrying names every command whose help screen shows the flag, which
// is its own flags plus everything it inherits from a namespace above it.
func commandsCarrying(flag string) []string {
	var carrying []string
	walk(rootCmd, func(path string, cmd *cobra.Command) {
		if cmd.LocalFlags().Lookup(flag) != nil || cmd.InheritedFlags().Lookup(flag) != nil {
			carrying = append(carrying, path)
		}
	})
	sort.Strings(carrying)
	return carrying
}

// TestNothingIsGlobalOnTheRoot is the rule the rest of this file measures.
//
// Cobra prints every root persistent flag under "Global Flags" on every
// subcommand, so one flag declared there appears on all of them whether they
// read it or not. `forge version --help` advertised a path to a repo registry
// that command never opens. A help screen naming a flag its command ignores is
// read as a fact and is wrong, which is worse than one that omits something.
func TestNothingIsGlobalOnTheRoot(t *testing.T) {
	var declared []string
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { declared = append(declared, f.Name) })
	if len(declared) != 0 {
		t.Fatalf("the root declares persistent flags, which land on every command: %v", declared)
	}
}

func TestTheRegistryFlagIsOnEveryCommandThatReadsTheRegistryAndNoOther(t *testing.T) {
	// Four namespaces own it, so every command under one carries it: repos and
	// directories resolve their targets from the registry, cli discovers its
	// tools there when none is named, and config show answers which registry
	// was resolved. test has no subtree and declares it alone.
	//
	// dies, toolchain, version and update are the commands that never open one,
	// and their absence here is the point.
	want := []string{
		"cli",
		"cli audit",
		"cli diff",
		"cli snapshot",
		"cli spec",
		"config",
		"config show",
		"directories",
		"directories apply",
		"directories check",
		"directories list",
		"directories plan",
		"directories run",
		"repos",
		"repos apply",
		"repos check",
		"repos exec",
		"repos list",
		"repos plan",
		"test",
	}
	got := commandsCarrying(registryFlag)
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("-c is on:\n  %v\nwant:\n  %v", got, want)
	}
}

// The direction the list above cannot see.
//
// A command that reads the registry and declares nothing is absent from both
// sides of that comparison, so both agree and the test stays green. This is the
// half that catches it: loadRepos is the only door to the registry, and it
// refuses a command that has not declared the flag rather than ignoring a path
// someone typed.
func TestNothingReachesTheRegistryExceptThroughLoadRepos(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "loadRepos" {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "config" {
					return true
				}
				if sel.Sel.Name == "LoadRepos" || sel.Sel.Name == "LoadSyncerConfig" {
					t.Errorf("%s in %s opens the registry directly, so -c is dead on the command "+
						"it serves; call loadRepos(cmd) instead", fn.Name.Name, file)
				}
				return true
			})
		}
	}
}

// The refusal itself, which is what makes the rule above enforceable rather
// than only readable.
func TestLoadReposRefusesACommandWithoutTheFlag(t *testing.T) {
	bare := &cobra.Command{Use: "bare"}
	if _, err := loadRepos(bare); err == nil {
		t.Fatal("loadRepos accepted a command that never declared -c")
	} else if !strings.Contains(err.Error(), "bare") {
		t.Errorf("error = %q, want it to name the command", err)
	}

	declared := &cobra.Command{Use: "declared"}
	addRegistryFlag(declared)
	if _, err := loadRepos(declared); err != nil && strings.Contains(err.Error(), "without declaring") {
		t.Errorf("a command declaring -c was refused: %v", err)
	}
}
