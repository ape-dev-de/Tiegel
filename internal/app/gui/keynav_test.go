package gui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/syntax"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

func (e *env) activeFile() string {
	if t := e.ctl.ActiveTab(); t != nil {
		return t.File
	}
	return ""
}

func (e *env) sideButton(b pointer.Buttons) {
	pos := f32.Pt(600, 300)
	e.h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: b, Position: pos},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos},
	)
	e.frame()
	e.frame()
}

// Back/Forward with every reference chord, the mouse side buttons and
// native gestures; a keymap override applies live.
func TestBackForwardChordsButtonsAndOverride(t *testing.T) {
	e := newEnv(t, true)
	shop := filepath.Join(e.root, "src", "Shop.php")
	index := filepath.Join(e.root, "index.php")
	s := e.openEditor("src/Shop.php")
	e.caretAt(s, "function greet", 0)
	e.ctl.GoTo(e.v.currentLocation(), controller.At(index, 6))
	e.waitFor("index.php", func() bool { return e.activeFile() == index })
	e.frame()

	step := func(what, want string, do func()) {
		t.Helper()
		do()
		e.waitFor(what, func() bool { return e.activeFile() == want })
	}
	step("Cmd+Alt+Left", shop, func() { e.key(key.NameLeftArrow, key.ModCommand|key.ModAlt) })
	step("Cmd+Alt+Right", index, func() { e.key(key.NameRightArrow, key.ModCommand|key.ModAlt) })
	step("Cmd+[", shop, func() { e.key("[", key.ModCommand) })
	step("Cmd+]", index, func() { e.key("]", key.ModCommand) })
	step("mouse back", shop, func() { e.sideButton(pointer.ButtonQuaternary) })
	step("mouse forward", index, func() { e.sideButton(pointer.ButtonQuinary) })
	step("swipe back", shop, func() { e.v.runMenuID(gesturePrefix + keymap.KeySwipeBack); e.frame() })
	step("swipe forward", index, func() { e.v.runMenuID(gesturePrefix + keymap.KeySwipeForward); e.frame() })
	if e.v.Gesture("Nothing") {
		t.Fatal("unbound gesture ran")
	}
	if got := e.v.Registry.ChordText(ActBack, "darwin"); got != "Cmd+[" {
		t.Fatalf("menu chord of Back %q", got)
	}

	// Rebind Back to F5 through the API: Cmd+[ stops working, F5 works,
	// without restarting the window.
	var res api.KeymapSetResult
	if err := e.ctl.Backend().Call(context.Background(), "keymap.set", api.KeymapSetParams{Action: "navigate.back", Chords: []string{"F5"}}, &res); err != nil {
		t.Fatal(err)
	}
	e.waitFor("keymap reload", func() bool { return e.v.Registry.Scheme().ChordText(ActBack) == "F5" })
	e.key("[", key.ModCommand)
	if e.activeFile() != index {
		t.Fatal("Cmd+[ should be unbound now")
	}
	step("F5", shop, func() { e.key(key.NameF5, 0) })
	if got := e.v.Registry.ChordText(ActBack, "darwin"); got != "F5" {
		t.Fatalf("menu chord after override %q", got)
	}
	if got := e.v.Registry.ChordText(ActBack, "linux"); got != "Ctrl+Alt+Left" {
		t.Fatalf("pc chord %q", got)
	}
}

// Every window action of the catalog has a handler, and the chords the
// window registers agree with the catalog.
func TestCatalogMatchesRegistry(t *testing.T) {
	e := newEnv(t, true)
	r := e.v.Registry
	gitOnly := map[keymap.Action]bool{"git.commit": true, "git.push": true, "git.rollback": true, "git.updateProject": true, "view.git": true, "view.commit": true, "git.showDiff": true}
	for _, d := range keymap.Defs() {
		if keymap.IsWidgetAction(d.ID) || gitOnly[d.ID] {
			continue
		}
		if !r.HasHandler(string(d.ID)) {
			t.Errorf("catalog action %s has no handler", d.ID)
		}
	}
	mac, pc := keymap.NewScheme(keymap.PresetMac, nil), keymap.NewScheme(keymap.PresetPC, nil)
	for _, a := range r.Actions() {
		for _, c := range []struct {
			chord string
			s     *keymap.Scheme
		}{{a.Mac, mac}, {a.PC, pc}} {
			if c.chord == "" || !c.s.Has(a.ID) {
				continue
			}
			if !slices.Contains(c.s.Chords(a.ID), keymap.MustParse(c.chord)) {
				t.Errorf("%s: registry chord %s not in catalog %v", a.ID, c.chord, c.s.Chords(a.ID))
			}
		}
	}
}

// In a git project the VCS actions of the catalog have handlers, too.
func TestCatalogGitActions(t *testing.T) {
	e := newGitEnv(t)
	e.waitFor("git actions", func() bool { return e.v.Registry.HasHandler("git.commit") })
	for _, id := range []string{"git.commit", "git.push", "git.rollback", "git.updateProject", "view.git", "view.commit", "git.showDiff"} {
		if !e.v.Registry.HasHandler(id) {
			t.Errorf("%s has no handler", id)
		}
	}
}

// Recent Locations lists edits and jumps; Last Edit Location returns to
// the last edit; Join Lines and Toggle Case run in the editor.
func TestRecentLocationsAndLastEdit(t *testing.T) {
	e := newEnv(t, true)
	index := filepath.Join(e.root, "index.php")
	s := e.openEditor("index.php")
	s.doc.Lock()
	s.ed.SetCaret(s.doc.Buffer().Len())
	s.ed.Cursor().Insert("echo   \n   2;")
	s.doc.Unlock()
	e.frame()
	e.openEditor("src/Shop.php")
	e.key(key.NameDeleteBackward, key.ModCommand|key.ModShift)
	e.waitFor("last edit", func() bool { return e.activeFile() == index })

	e.key("E", key.ModCommand|key.ModShift)
	if e.v.chooseMode != chooseCustom || !strings.HasPrefix(e.v.chooser.Items[0].Label, "index.php:") || !strings.Contains(e.v.chooser.Items[0].Detail, "2;") {
		t.Fatalf("recent locations: mode %d items %+v", e.v.chooseMode, e.v.chooser.Items)
	}
	e.chooseLabel("index.php")
	e.frame()

	// Join Lines (Ctrl+Shift+J) and Toggle Case (Cmd+Shift+U).
	s = e.openEditor("index.php")
	e.caretAt(s, "echo   ", 0)
	e.key("J", key.ModCtrl|key.ModShift)
	e.caretAt(s, "echo 2", 1)
	e.key("U", key.ModCommand|key.ModShift)
	s.doc.Lock()
	text := s.doc.Buffer().String()
	s.doc.Unlock()
	if !strings.Contains(text, "ECHO 2;") {
		t.Fatalf("join + toggle: %q", text)
	}
}

// Paste from History offers what was copied and pastes the choice.
func TestPasteFromHistory(t *testing.T) {
	e := newEnv(t, true)
	s := e.openEditor("index.php")
	clipHistory.Push("first-copy")
	clipHistory.Push("second-copy")
	e.caretAt(s, "echo", 0)
	e.key("V", key.ModCommand|key.ModShift)
	if e.v.chooseMode != chooseCustom || len(e.v.chooser.Items) < 2 || e.v.chooser.Items[0].Label != "second-copy" {
		t.Fatalf("history chooser %+v", e.v.chooser.Items)
	}
	e.v.custom.OnQuery("first")
	e.v.choose(palette.ChooseEvent{Index: 0, Query: "first"})
	e.frame()
	s.doc.Lock()
	text := s.doc.Buffer().String()
	s.doc.Unlock()
	if !strings.Contains(text, "first-copyecho") || clipHistory.Items()[0] != "first-copy" {
		t.Fatalf("pasted: %q", text)
	}
}

// Next/Previous Highlighted Error and Quick Documentation show the
// diagnostics at the caret.
func TestNextErrorAndQuickDoc(t *testing.T) {
	// The project targets a PHP version that fits the sample code, so the
	// PHP version check adds no diagnostic of its own (it raced with the
	// published ones and could come first).
	e := newEnvWith(t, true, map[string]string{"composer.json": `{"require": {"php": "^8.3"}}`}, nil)
	file := filepath.Join(e.root, "src", "Shop.php")
	s := e.openEditor("src/Shop.php")
	e.app.Bus().Publish(api.TopicLSPDiagnostics, map[string]any{
		"path": "src/Shop.php", "file": file, "version": 1,
		"diagnostics": []map[string]any{
			{"severity": "error", "message": "first problem", "start": 10, "end": 14, "line": 3, "col": 1},
			{"severity": "warning", "message": "second problem", "start": 40, "end": 44, "line": 8, "col": 1},
		},
	})
	// Wait for the two published problems themselves: the PHP version
	// check may add its own diagnostics at any time (so the count is no
	// condition), and the event is delivered asynchronously (slow under
	// load; waitFor's deadline is generous).
	e.waitFor("diagnostics", func() bool {
		found := 0
		for _, d := range e.ctl.Diagnostics(file) {
			if d.Message == "first problem" || d.Message == "second problem" {
				found++
			}
		}
		return found == 2
	})
	e.frame()
	e.key(key.NameF2, 0)
	if caret := s.ed.Cursor().Primary().Head; caret != 10 || !e.ctl.Hover.Open || !strings.Contains(e.ctl.Hover.Text, "first problem") {
		t.Fatalf("F2: caret %d hover %+v", caret, e.ctl.Hover)
	}
	e.key(key.NameF2, 0)
	e.key(key.NameF2, key.ModShift)
	if caret := s.ed.Cursor().Primary().Head; caret != 10 {
		t.Fatalf("Shift+F2: caret %d", caret)
	}
	e.ctl.CloseHover()
	e.key(key.NameF1, 0)
	// (The PHP version check may add its own diagnostic at the caret.)
	if !e.ctl.Hover.Open || !strings.Contains(e.ctl.Hover.Text, "first problem") {
		t.Fatalf("F1: %+v", e.ctl.Hover)
	}
	// Parameter Info without a language server does nothing.
	e.key("P", key.ModCommand)
	if e.ctl.Signature.Open {
		t.Fatal("signature without LSP")
	}
}

// The File Structure popup lists the outline and jumps to a member.
func TestFileStructurePopup(t *testing.T) {
	if !syntax.TreeSitter {
		t.Skip("outline needs Tree-sitter (cgo)")
	}
	e := newEnv(t, true)
	s := e.openEditor("src/Shop.php")
	e.key(key.NameF12, key.ModCommand)
	e.waitFor("structure popup", func() bool { return e.v.chooseMode == chooseCustom })
	labels := ""
	for _, it := range e.v.chooser.Items {
		labels += it.Label + ";"
	}
	if !strings.Contains(labels, "greet") || !strings.Contains(labels, "Suit") {
		t.Fatalf("structure items %q", labels)
	}
	e.v.chooser.Items = e.v.custom.OnQuery("gre")
	if len(e.v.chooser.Items) != 1 {
		t.Fatalf("filtered %+v", e.v.chooser.Items)
	}
	e.chooseLabel("greet")
	e.waitFor("jump", func() bool {
		s.doc.Lock()
		defer s.doc.Unlock()
		h := s.ed.Cursor().Primary().Head
		return strings.HasPrefix(string(s.doc.Buffer().Slice(h, s.doc.Buffer().Len())), "greet")
	})
}
