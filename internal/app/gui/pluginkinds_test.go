package gui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
)

// TestNoPluginImports: the GUI (with its controller) is a client of the
// API contract and never imports a plugin package outside tests, so a
// disabled or replaced plugin cannot break it (D41, D462; review wave 8,
// #9).
func TestNoPluginImports(t *testing.T) {
	const banned = "github.com/ape-dev-de/tiegel/internal/modules"
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		files++
		for _, imp := range f.Imports {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil && (p == banned || strings.HasPrefix(p, banned+"/")) {
				t.Errorf("%s imports plugin package %s", fset.Position(imp.Pos()), p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 10 {
		t.Fatalf("scanned only %d files", files)
	}
}

// TestNoPluginTerminalKinds: the GUI gets terminal session kinds from the
// backend (terminal.kinds, D372) and never names a plugin's kind, so a
// renamed or replaced plugin keeps working.
func TestNoPluginTerminalKinds(t *testing.T) {
	banned := map[string]bool{dockermod.TerminalKindService: true, dockermod.TerminalKindContainer: true}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && banned[s] {
					t.Errorf("%s: plugin terminal kind %q", fset.Position(lit.Pos()), s)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
