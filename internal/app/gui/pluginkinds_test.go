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
