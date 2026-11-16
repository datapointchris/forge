package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// streamWalk is what one pass over the source found: the files it parsed, the
// writes through a command's own writer, and every write that went around one.
type streamWalk struct {
	files    int
	injected int
	bypasses []string
}

// printMethods are the names whose call writes to the process's stdout: fmt's
// and log's package functions, and color's Printf family on a *color.Color.
// The Fprint family takes its writer and is the door, so it is not here.
var printMethods = map[string]bool{"Print": true, "Printf": true, "Println": true}

// logWrites are the log package's functions that write to the process's
// stderr, whatever a command was handed.
var logWrites = map[string]bool{
	"Print": true, "Printf": true, "Println": true,
	"Fatal": true, "Fatalf": true, "Fatalln": true,
	"Panic": true, "Panicf": true, "Panicln": true,
}

// scanStreamWrites records each write in one parsed file. Import names are read
// from the file, so an aliased os, fmt or log is caught under its alias.
func scanStreamWrites(fset *token.FileSet, file *ast.File, walk *streamWalk) {
	imported := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imported[name] = path
	}
	report := func(n ast.Node, what string) {
		walk.bypasses = append(walk.bypasses, fset.Position(n.Pos()).String()+": "+what)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := node.X.(*ast.Ident); ok && imported[pkg.Name] == "os" &&
				(node.Sel.Name == "Stdout" || node.Sel.Name == "Stderr") {
				report(node, "os."+node.Sel.Name)
			}
		case *ast.CallExpr:
			switch fn := node.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "print" || fn.Name == "println" {
					report(node, fn.Name)
				}
			case *ast.SelectorExpr:
				name := fn.Sel.Name
				if name == "OutOrStdout" || name == "ErrOrStderr" {
					walk.injected++
				}
				if pkg, ok := fn.X.(*ast.Ident); ok && imported[pkg.Name] == "log" {
					if logWrites[name] {
						report(node, "log."+name)
					}
					return true
				}
				if printMethods[name] {
					report(node, "a bare "+name)
				}
			}
		}
		return true
	})
}

// walkStreamWrites scans every non-test Go file under root. Test files are left
// out, because a test captures os.Stdout on purpose to prove what lands there.
func walkStreamWrites(t *testing.T, root string) streamWalk {
	t.Helper()
	fset := token.NewFileSet()
	var walk streamWalk
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		walk.files++
		scanStreamWrites(fset, file, &walk)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return walk
}

// Every line forge prints goes through the writer its command was handed, so a
// test can read it and a caller can redirect it. A bare fmt.Printf compiles,
// passes vet and lint, and lands on the process's stdout where neither can
// reach it. The walk covers the whole module, because runner and the dies
// print rows a command hands them a writer for.
func TestEveryWriteGoesThroughTheCommandsWriter(t *testing.T) {
	walk := walkStreamWrites(t, "..")
	for _, bypass := range walk.bypasses {
		t.Errorf("%s writes past the command's writer; take cmd.OutOrStdout() or cmd.ErrOrStderr() instead", bypass)
	}
	// A walk that parsed nothing, or read the wrong tree, would pass with every
	// write a bypass.
	if walk.files < 30 || walk.injected < 20 {
		t.Fatalf("parsed %d files and found %d command-writer calls, want the module's source", walk.files, walk.injected)
	}
}

func TestStreamWalkCatchesEachBypass(t *testing.T) {
	cases := map[string]string{
		"os.Stdout":       `import "os"; var w = os.Stdout`,
		"aliased os":      `import sys "os"; var w = sys.Stderr`,
		"fmt.Println":     `import "fmt"; func f() { fmt.Println() }`,
		"color Printf":    `func f(c interface{ Printf(string, ...any) }) { c.Printf("x") }`,
		"println builtin": `func f() { println() }`,
		"log.Fatalf":      `import "log"; func f() { log.Fatalf("x") }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "case.go", "package p; "+src, 0)
			if err != nil {
				t.Fatal(err)
			}
			var walk streamWalk
			scanStreamWrites(fset, file, &walk)
			if len(walk.bypasses) != 1 {
				t.Errorf("found %v, want one bypass", walk.bypasses)
			}
		})
	}
}
