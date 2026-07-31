package acceptance_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnitEveryAcceptanceTestIsGuarded is the safety net for the whole suite.
//
// Acceptance tests create real objects in a live Automox organization holding
// about 1,500 employee laptops. Nothing in Go stops someone adding a TestAcc
// function that omits SkipUnlessAcceptance, and the consequence is not a failing
// test: it is continuous integration silently writing to production on every
// pull request. That is the one failure this repository cannot afford to
// discover late, so it is asserted statically rather than trusted to review.
//
// This is deliberately a source scan rather than a runtime check. A runtime
// check could only observe tests that ran, and the tests that matter here are
// exactly the ones that should not.
func TestUnitEveryAcceptanceTestIsGuarded(t *testing.T) {
	root := repoRoot(t)

	var unguarded []string
	checked := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Nothing under these can register a test.
			switch d.Name() {
			case ".git", "docs", "examples", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "TestAcc") {
				continue
			}

			checked++
			if !callsSkipGuard(fn) {
				rel, _ := filepath.Rel(root, path)
				unguarded = append(unguarded, rel+": "+fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if checked == 0 {
		t.Fatal("found no TestAcc functions to check; this guard is not looking where " +
			"the tests are and would pass however broken the suite was")
	}

	if len(unguarded) > 0 {
		t.Errorf("%d acceptance test(s) do not call acceptance.SkipUnlessAcceptance and "+
			"would run against the live Automox organization during an ordinary "+
			"`go test ./...`, including in CI:\n  %s",
			len(unguarded), strings.Join(unguarded, "\n  "))
	}

	t.Logf("checked %d acceptance tests; all guarded", checked)
}

// callsSkipGuard reports whether the function body calls
// acceptance.SkipUnlessAcceptance, at any depth.
//
// Depth matters: a guard inside a helper called from the test would still
// protect it, and this walks the whole body rather than only the first
// statement so a reordering does not read as a regression.
func callsSkipGuard(fn *ast.FuncDecl) bool {
	found := false

	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "SkipUnlessAcceptance" {
			return true
		}
		// Confirm it is the acceptance package's, not a same-named local.
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "acceptance" {
			found = true
			return false
		}
		return true
	})

	return found
}

// repoRoot walks up from this test until it finds go.mod, so the scan covers the
// whole module regardless of where the test is run from.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the test's working directory")
		}
		dir = parent
	}
}
