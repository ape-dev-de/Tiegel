package gui

import (
	"strings"
	"testing"

	"gioui.org/io/key"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func docText(s *editorSession) string {
	s.doc.Lock()
	defer s.doc.Unlock()
	return s.doc.Buffer().String()
}

func selectedTexts(s *editorSession) []string {
	s.doc.Lock()
	defer s.doc.Unlock()
	return s.ed.Cursor().SelectedText()
}

// Tab expands an abbreviation, the tab stops are walked with Tab, all
// occurrences of a variable are edited together.
func TestLiveTemplateTabStops(t *testing.T) {
	if !syntax.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	e := newEnvWith(t, true, map[string]string{"src/T.php": "<?php\nfunction f() {\n    for\n}\n"}, nil)
	s := e.openEditor("src/T.php")
	e.waitFor("templates", func() bool { return e.ctl.Templates.IsAbbreviation("for") })
	e.caretAt(s, "for\n", 3)
	e.key(key.NameTab, 0)
	e.settle()
	e.waitFor("expansion", func() bool { return strings.Contains(docText(s), "for ($i = 0; $i < 10; $i++) {") && s.snip != nil })
	if got := selectedTexts(s); len(got) != 3 || got[0] != "i" {
		t.Fatalf("first stop %q", got)
	}
	// Typing replaces all three occurrences.
	s.doc.Lock()
	s.ed.Cursor().Insert("n")
	s.doc.Unlock()
	e.frame()
	if !strings.Contains(docText(s), "for ($n = 0; $n < 10; $n++) {") {
		t.Fatalf("linked editing %q", docText(s))
	}
	e.key(key.NameTab, 0)
	if got := selectedTexts(s); len(got) != 1 || got[0] != "10" {
		t.Fatalf("second stop %q", got)
	}
	e.key(key.NameTab, key.ModShift)
	if got := selectedTexts(s); len(got) != 3 || got[0] != "n" {
		t.Fatalf("back to the first stop %q", got)
	}
	e.key(key.NameTab, 0)
	e.key(key.NameTab, 0)
	if s.snip != nil {
		t.Fatal("session should end after the last stop")
	}
	s.doc.Lock()
	caret := s.ed.Cursor().Primary().Head
	text := s.doc.Buffer().String()
	s.doc.Unlock()
	if !strings.HasSuffix(text[:caret], "$n++) {\n        ") {
		t.Fatalf("caret at end: %q", text[:caret])
	}
	// Without an abbreviation Tab indents as usual.
	e.caretAt(s, "function", 0)
	e.key(key.NameTab, 0)
	if !strings.Contains(docText(s), "    function f()") {
		t.Fatalf("plain tab %q", docText(s))
	}
}

// Insert Live Template and Surround With offer the templates of the
// context in a chooser.
func TestLiveTemplateChooser(t *testing.T) {
	if !syntax.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	e := newEnvWith(t, true, map[string]string{"src/T.php": "<?php\nfunction f() {\n    work();\n}\n"}, nil)
	s := e.openEditor("src/T.php")
	e.caretAt(s, "work", 0)
	e.v.RunAction("code.surroundWith")
	e.waitFor("chooser", func() bool { return e.v.chooseMode == chooseCustom && len(e.v.chooser.Items) > 0 })
	for _, it := range e.v.chooser.Items {
		if it.Label == "pubf" {
			t.Fatal("pubf is no surround template")
		}
	}
	e.chooseLabel("try")
	e.waitFor("surrounded", func() bool {
		return s.snip != nil && strings.Contains(docText(s), "    try {\n        work();\n    } catch (\\Throwable $e) {")
	})
	if got := selectedTexts(s); len(got) != 1 || got[0] != "Throwable" {
		t.Fatalf("exception stop %q", got)
	}
	e.key(key.NameEscape, 0)
	if s.snip != nil {
		t.Fatal("escape should end the session")
	}
	e.caretAt(s, "<?php\n", 6)
	e.v.RunAction("code.insertLiveTemplate")
	e.waitFor("chooser", func() bool { return e.v.chooseMode == chooseCustom && len(e.v.chooser.Items) > 0 })
	e.chooseLabel("if")
	e.waitFor("inserted", func() bool { return strings.Contains(docText(s), "<?php\nif (CONDITION) {") && s.snip != nil })
}

// Generate (Cmd+N) offers the built-in generators; with several
// properties it asks which.
func TestGenerateChooser(t *testing.T) {
	if !syntax.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	e := newEnvWith(t, true, map[string]string{"src/G.php": "<?php\nclass G\n{\n    private int $a;\n    private string $b;\n}\n"}, nil)
	s := e.openEditor("src/G.php")
	e.caretAt(s, "private int", 0)
	e.v.RunAction("code.generate")
	e.waitFor("menu", func() bool { return e.v.chooseMode == chooseCustom && strings.HasPrefix(e.v.chooser.Title, "Generate") })
	e.chooseLabel("Getters and Setters")
	e.waitFor("properties", func() bool { return e.v.chooseMode == chooseCustom && e.v.chooser.Title == "Getters and Setters" })
	e.chooseLabel("$b")
	e.waitFor("generated", func() bool {
		return strings.Contains(docText(s), "public function getB(): string") && strings.Contains(docText(s), "public function setB(string $b): void")
	})
	if strings.Contains(docText(s), "getA") {
		t.Fatal("only $b was chosen")
	}
}
