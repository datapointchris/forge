package reconcile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Target's docstring says a die never calls os.Chdir, and the reason is that
// every die shares one process with the walk that drives it.
//
// The site that breaks this is the one that never opens that docstring. A die
// shelling out to a tool that wants a working directory is the shape that
// reaches for a chdir, and it is written in dies/ rather than here, so the
// prose guarding it has the inverse reach of the thing it guards.
//
// Test files are exempt: a test may chdir into its own t.TempDir() without
// affecting a walk that is not running.
func TestNoDieChangesTheProcessWorkingDirectory(t *testing.T) {
	var offenders []string

	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isChdir(call.Fun) {
				return true
			}
			offenders = append(offenders, fset.Position(call.Pos()).String())
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %s", err)
	}

	if len(offenders) > 0 {
		t.Errorf("os.Chdir called in production code, which races a parallel walk: %s",
			strings.Join(offenders, ", "))
	}
}

func isChdir(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Chdir" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "os"
}
