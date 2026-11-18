package qa

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// corePackages are the kernel, the GUI client and the shared helper:
// none of them may name a quality tool (AP17.1, D190).
var corePackages = []string{
	"../../api", "../../core", "../../run", "../../install", "../../actions",
	"../../app", "../../headless", "../../ui", "qakit",
}

// splitCamel inserts a space at lower→upper case changes ("fooBar" →
// "foo Bar").
func splitCamel(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(rs[i-1]) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestCoreNamesNoTool: identifiers and string literals of the core
// packages (comments aside) contain no tool name or plugin ID.
func TestCoreNamesNoTool(t *testing.T) {
	var words []string
	for _, p := range All() {
		d := p.Definition()
		words = append(words, regexp.QuoteMeta(d.ID), regexp.QuoteMeta(strings.ToLower(d.Program)))
		if d.FormatProgram != "" {
			words = append(words, regexp.QuoteMeta(d.FormatProgram))
		}
	}
	re := regexp.MustCompile(`(^|[^a-z0-9])(` + strings.Join(words, "|") + `)([^a-z0-9]|$)`)
	if !re.MatchString("vendor/bin/phpstan") || !re.MatchString("qa-pint.path") || re.MatchString("stepinto") {
		t.Fatal("pattern broken")
	}
	files := 0
	for _, dir := range corePackages {
		err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files++
			fset := token.NewFileSet()
			var s scanner.Scanner
			s.Init(fset.AddFile(path, -1, len(src)), src, nil, 0)
			for {
				pos, tok, lit := s.Scan()
				if tok == token.EOF {
					break
				}
				if tok != token.IDENT && tok != token.STRING && tok != token.CHAR {
					continue
				}
				for _, v := range []string{strings.ToLower(lit), strings.ToLower(splitCamel(lit))} {
					if m := re.FindStringSubmatch(v); m != nil {
						t.Errorf("%s: names tool %q in %s", fset.Position(pos), m[2], lit)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 100 {
		t.Fatalf("only %d files scanned", files)
	}
}
