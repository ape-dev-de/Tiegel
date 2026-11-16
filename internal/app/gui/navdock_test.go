package gui

import (
	"context"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
)

// navFixture is a small PHP project for navigation: an interface with two
// implementations and a user.
var navFixture = map[string]string{
	"src/Shape.php": `<?php

namespace App;

interface Shape
{
    public function area(): float;
}
`,
	"src/Circle.php": `<?php

namespace App;

final class Circle implements Shape
{
    public function area(): float
    {
        return 3.14;
    }
}
`,
	"src/Square.php": `<?php

namespace App;

final class Square implements Shape
{
    public function area(): float
    {
        return 1.0;
    }
}
`,
	"src/Room.php": `<?php

namespace App;

final class Room
{
    public function __construct(private Shape $floor)
    {
    }

    public function size(): float
    {
        return $this->floor->area();
    }
}
`,
}

// openEditor opens rel and returns its editor session.
func (e *env) openEditor(rel string) *editorSession {
	e.t.Helper()
	e.ctl.OpenFile(rel, nil)
	file := filepath.Join(e.root, filepath.FromSlash(rel))
	e.waitFor("tab "+rel, func() bool { t := e.ctl.ActiveTab(); return t != nil && t.File == file })
	e.v.focusEditor = true
	e.frame()
	e.frame()
	s := e.v.editors[sessionKey{e.ctl.ActiveGroup(), file}]
	if s == nil {
		e.t.Fatal("no editor session")
	}
	return s
}

// caretAt puts the caret on the first occurrence of needle (plus delta).
func (e *env) caretAt(s *editorSession, needle string, delta int) {
	e.t.Helper()
	s.doc.Lock()
	i := strings.Index(string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len())), needle)
	if i < 0 {
		s.doc.Unlock()
		e.t.Fatalf("%q not in document", needle)
	}
	s.ed.SetCaret(i + delta)
	s.doc.Unlock()
	e.frame()
}

// waitIndex waits for the PHP index (skips without Tree-sitter).
func (e *env) waitIndex() {
	e.t.Helper()
	if !index.TreeSitter {
		e.t.Skip("PHP index needs Tree-sitter (cgo)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var st struct {
		State string `json:"state"`
	}
	if err := e.ctl.Backend().Call(ctx, "index.status", map[string]bool{"wait": true}, &st); err != nil || st.State != "ready" {
		e.t.Fatalf("index: %v %+v", err, st)
	}
}

func (e *env) secondaryClick(p image.Point) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	e.h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: pos},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos},
	)
	e.frame()
	e.frame()
}

// E2E with the real index: Cmd+Alt+B on an interface offers both
// implementations, on a method of one implementation jumps up with
// Cmd+U; gutter marks show ↓/↑ and open targets; Back returns.
func TestNavigationWithIndex(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.waitIndex()
	s := e.openEditor("src/Circle.php")
	e.caretAt(s, "implements Shape", len("implements S"))
	// Cmd+B on the interface name jumps to its declaration.
	e.key("B", key.ModCommand)
	e.waitFor("definition", func() bool { t := e.ctl.ActiveTab(); return t != nil && strings.HasSuffix(t.File, "Shape.php") })
	shape := e.v.activeSession()
	e.frame()
	// Cmd+Alt+B on the interface: two implementations → popup list.
	e.caretAt(shape, "interface Shape", len("interface S"))
	e.key("B", key.ModCommand|key.ModAlt)
	e.waitFor("implementations popup", func() bool { return e.v.chooseMode == chooseCustom && len(e.v.chooser.Items) == 2 })
	if e.v.chooser.Title != "Choose Implementation" || !strings.Contains(e.v.chooser.Items[0].Label+e.v.chooser.Items[1].Label, `App\Square`) {
		t.Fatalf("popup %q %+v", e.v.chooser.Title, e.v.chooser.Items)
	}
	e.shot("nav-implementations")
	e.chooseLabel(`App\Square`)
	e.waitFor("square", func() bool { return strings.HasSuffix(e.ctl.ActiveTab().File, "Square.php") })
	// Cmd+U on Square::area goes to the interface method (one target).
	sq := e.openEditor("src/Square.php")
	e.caretAt(sq, "function area", len("function a"))
	e.key("U", key.ModCommand)
	e.waitFor("super", func() bool { return strings.HasSuffix(e.ctl.ActiveTab().File, "Shape.php") })
	// Back (Cmd+[) returns to Square.php.
	e.key("[", key.ModCommand)
	e.waitFor("back", func() bool { return strings.HasSuffix(e.ctl.ActiveTab().File, "Square.php") })

	// Gutter marks of Shape.php: ↓ on the interface and on area().
	shapeFile := filepath.Join(e.root, "src", "Shape.php")
	e.openEditor("src/Shape.php")
	e.waitFor("gutter marks", func() bool { return len(e.v.navd.n.GutterMarks(shapeFile)) == 2 })
	p := e.v.Registry.markers(shapeFile)
	if !hasMarker(p, 4, editorview.MarkerIcon) || !hasMarker(p, 6, editorview.MarkerIcon) {
		t.Fatalf("no ↓ icons: %+v %+v", p.Markers(4), p.Markers(6))
	}
	// A click on the ↓ of area() offers both implementations.
	if !e.v.navd.gutterClick(shapeFile, editorview.GutterClickEvent{Line: 6, Area: editorview.GutterIcons, Clicks: 1}) {
		t.Fatal("gutter click not handled")
	}
	e.frame()
	e.waitFor("gutter popup", func() bool { return e.v.chooseMode == chooseCustom && len(e.v.chooser.Items) == 2 })
	e.v.closeOverlay()
	e.frame()

	// Go to Class (Cmd+O) finds Circle.
	e.key("O", key.ModCommand)
	if e.v.chooser.Title != "Go to Class" {
		t.Fatalf("go to class: %q", e.v.chooser.Title)
	}
	e.v.chooser.Items = e.v.custom.OnQuery("circ")
	e.waitFor("class result", func() bool { return len(e.v.chooser.Items) > 0 && e.v.chooser.Items[0].Label == "Circle" })
	e.chooseLabel("Circle")
	e.waitFor("circle", func() bool { return strings.HasSuffix(e.ctl.ActiveTab().File, "Circle.php") })

	// Type hierarchy (Ctrl+H) opens its tool window.
	e.caretAt(e.v.activeSession(), "class Circle", len("class C"))
	e.key("H", key.ModCtrl)
	e.waitFor("hierarchy", func() bool { return e.v.navd.n.Hierarchy != nil && e.v.ToolShown(ToolHierarchy) })
	if h := e.v.navd.n.Hierarchy; h.Class.FQN != `App\Circle` || len(h.Supertypes) != 1 {
		t.Fatalf("hierarchy %+v", h)
	}
	e.frame()
	if e.v.navd.hierarchy.rowCount() < 3 {
		t.Fatalf("hierarchy rows %d", e.v.navd.hierarchy.rowCount())
	}
}

// Shift Shift opens Search Everywhere with categories; typing in between
// does not.
func TestSearchEverywhere(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.openEditor("src/Room.php")
	shift := func(st key.State) {
		e.h.Router.Queue(key.Event{Name: key.NameShift, State: st})
		e.frame()
	}
	shift(key.Press)
	shift(key.Release)
	shift(key.Press)
	shift(key.Release)
	if e.v.chooseMode != chooseCustom || e.v.chooser.Title != "Search Everywhere" || len(e.v.chooser.Tabs) != 5 {
		t.Fatalf("search everywhere: mode %d title %q", e.v.chooseMode, e.v.chooser.Title)
	}
	e.v.chooser.Items = e.v.custom.OnQuery("room")
	if index.TreeSitter {
		e.waitIndex()
		e.v.chooser.Items = e.v.custom.OnQuery("room")
		e.waitFor("class and file", func() bool {
			groups := map[string]bool{}
			for _, it := range e.v.chooser.Items {
				groups[it.Group] = true
			}
			return groups["Classes"] && groups["Files"]
		})
	}
	// Tab switches to Files.
	e.h.Router.Queue(key.Event{Name: key.NameTab, State: key.Press})
	e.frame()
	e.frame()
	if e.v.chooser.Tab != 1 {
		t.Fatalf("tab %d", e.v.chooser.Tab)
	}
	e.v.chooser.Tab = 2
	e.v.chooser.Items = e.v.custom.OnTab(2, "room")
	e.waitFor("files", func() bool { return len(e.v.chooser.Items) > 0 && e.v.chooser.Items[0].Label == "Room.php" })
	e.v.chooser.Tab = 0
	e.v.chooser.Items = e.v.custom.OnTab(0, "room")
	e.frame()
	e.shot("nav-search-everywhere")
	e.v.closeOverlay()
	e.frame()

	// Shift, a typed letter, Shift: no Search Everywhere.
	s := e.v.activeSession()
	e.caretAt(s, "final", 0)
	shift(key.Press)
	shift(key.Release)
	s.doc.Lock()
	_, _ = s.doc.Buffer().Insert(0, []byte("X"))
	s.doc.Unlock()
	shift(key.Press)
	if e.v.chooseMode != chooseNone {
		t.Fatal("typing between the Shift presses opened Search Everywhere")
	}
}

// A right click into the editor opens the editor menu; choosing an entry
// runs its action. The gutter has its own menu.
func TestEditorContextMenu(t *testing.T) {
	var long strings.Builder
	long.WriteString("<?php\n")
	for i := 0; i < 80; i++ {
		long.WriteString("$a = 1;\n")
	}
	e := newEnvWith(t, true, map[string]string{"src/Long.php": long.String()}, nil)
	s := e.openEditor("src/Long.php")
	// Select everything; a click inside the selection keeps it.
	s.doc.Lock()
	s.ed.Cursor().SetSelections([]cursor.Selection{cursor.Range(0, s.doc.Buffer().Len())}, 0)
	s.doc.Unlock()
	e.frame()
	e.secondaryClick(image.Pt(900, 300))
	if !e.v.navd.menus.Opened() {
		t.Fatal("no editor context menu")
	}
	labels := menuLabels(e.v.navd.menus.menu.Items)
	// (No "Git" submenu: the project is no repository.)
	for _, want := range []string{"Show Context Actions", "Copy", "Go To", "Find Usages", "Rename Symbol…", "Reformat Code", "Comment Line"} {
		if !strings.Contains(labels, want) {
			t.Errorf("editor menu lacks %q: %s", want, labels)
		}
	}
	e.shot("nav-editor-menu")
	e.chooseLabel("Comment Line")
	e.frame()
	e.frame()
	s.doc.Lock()
	text := string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len()))
	s.doc.Unlock()
	if !strings.Contains(text, "// $a = 1;") && !strings.Contains(text, "//$a = 1;") {
		t.Fatalf("comment not applied:\n%s", text[:60])
	}
	if e.v.navd.menus.Opened() {
		t.Fatal("menu still open")
	}
	// Gutter: a right click into the line numbers.
	s.gutterContextClick(3)
	e.frame()
	e.frame()
	if !e.v.navd.menus.Opened() || !strings.Contains(menuLabels(e.v.navd.menus.menu.Items), "Toggle Breakpoint") {
		t.Fatalf("gutter menu: %v", menuLabels(e.v.navd.menus.menu.Items))
	}
	e.chooseLabel("Toggle Breakpoint")
	e.waitFor("breakpoint", func() bool { _, ok := e.ctl.Debug.BreakpointAt(s.file, 3); return ok })
}

func menuLabels(items []molecules.MenuItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
		if len(it.Children) > 0 {
			out = append(out, menuLabels(it.Children))
		}
	}
	return strings.Join(out, "|")
}

// Every editor and gutter menu action has a handler, except the ones
// whose handlers come from other features (deliberately disabled until
// registered).
func TestEditorActionsHaveHandlers(t *testing.T) {
	e := newEnv(t, true)
	deferred := map[string]bool{
		actions.EditorGitAnnotate: true, actions.EditorGitHistory: true, actions.EditorGitCompare: true, actions.EditorGitCompareRev: true, actions.EditorGitRollback: true,
		actions.FileReveal: true, actions.FileHistory: true, // file operations
	}
	r := e.v.Registry
	var walk func(ctx actions.ContextID, items []actions.MenuItem)
	walk = func(ctx actions.ContextID, items []actions.MenuItem) {
		for _, it := range items {
			if it.Action != "" && !r.HasHandler(it.Action) && !deferred[it.Action] {
				t.Errorf("%s: %s has no handler", ctx, it.Action)
			}
			walk(ctx, it.Children)
		}
	}
	walk(actions.CtxEditor, r.Model().Menu(actions.CtxEditor))
	walk(actions.CtxEditorGutter, r.Model().Menu(actions.CtxEditorGutter))
	for _, id := range []string{actions.NavSuper, actions.NavTypeHierarchy, actions.NavGotoClass, actions.NavGotoSymbol, actions.NavSearchEverywhere,
		actions.RefactorRename, actions.CodeReformat, actions.CodeContextActions, actions.ViewStructure, actions.FindInPath, actions.ReplaceInPath} {
		if !r.HasHandler(id) {
			t.Errorf("%s has no handler", id)
		}
	}
	// Without a handler an entry is shown disabled.
	ac := e.v.baseContext(actions.CtxEditor)
	ac.Language, ac.Values["git"] = "php", "true"
	for _, it := range r.ContextEntries(actions.CtxEditor, ac, "darwin") {
		if it.Title == "Git" {
			for _, ch := range it.Children {
				if !ch.Disabled && !ch.Separator {
					t.Errorf("%s enabled without handler", ch.Title)
				}
			}
		}
	}
	// A handler registered later enables it (part 2 docks this way).
	r.AddAction(Action{ID: keymap.Action(actions.EditorGitHistory), Run: func(*View) {}})
	for _, it := range r.ContextEntries(actions.CtxEditor, ac, "darwin") {
		for _, ch := range it.Children {
			if ch.Action == keymap.Action(actions.EditorGitHistory) && (ch.Disabled || ch.Title != "Show History") {
				t.Errorf("registered handler: %+v", ch)
			}
		}
	}
}

// Shortcuts shown in menus fire once: the window binds each chord in one
// scope, editor keymap chords are not bound by the window, and a chord
// pressed once runs its action once (D111 follow-up).
func TestMenuShortcutsFireOnce(t *testing.T) {
	e := newEnv(t, true)
	r := e.v.Registry
	editorKeys := map[string]keymap.Action{}
	for _, b := range keymap.ForOS("darwin").Bindings() {
		editorKeys[b.Chord.String()] = b.Action
	}
	bound := map[string]string{}
	for _, a := range r.Actions() {
		if a.Mac == "" {
			if cs := keymap.ForOS("darwin").ChordsFor(a.ID); len(cs) > 0 && a.Run == nil {
				t.Errorf("%s: editor action without handler", a.ID)
			}
			continue
		}
		c, err := keymap.ParseChord(a.Mac)
		if err != nil {
			t.Errorf("%s: %v", a.ID, err)
			continue
		}
		k := c.String() + "@" + string(rune('0'+a.Scope))
		if prev, ok := bound[k]; ok {
			t.Errorf("%s bound twice: %s and %s", c, prev, a.ID)
		}
		bound[k] = string(a.ID)
		if ed, ok := editorKeys[c.String()]; ok && ed != a.ID {
			t.Errorf("window chord %s of %s shadows editor action %s", c, a.ID, ed)
		}
	}
	// Menus show the chords (main menu from the model).
	got := map[string]string{}
	var walk func(items []MenuItem)
	walk = func(items []MenuItem) {
		for _, it := range items {
			if it.Action != "" {
				got[string(it.Action)] = it.Shortcut
			}
			walk(it.Children)
		}
	}
	for _, m := range r.MainMenus(e.v.actionContext(actions.CtxMain), "darwin") {
		walk(m.Items)
	}
	for id, want := range map[string]string{"navigate.definition": "Cmd+B", "navigate.implementation": "Cmd+Alt+B", "navigate.super": "Cmd+U",
		"navigate.gotoClass": "Cmd+O", "navigate.gotoSymbol": "Cmd+Alt+O", "file.quickOpen": "Cmd+Shift+O", "find.inPath": "Cmd+Shift+F",
		"find.replaceInPath": "Cmd+Shift+R", "search.replace": "Cmd+R", "code.reformat": "Cmd+Alt+L", "refactor.rename": "Shift+F6",
		"navigate.usages": "Alt+F7", "edit.toggleComment": "Cmd+/", "app.findAction": "Cmd+Shift+A"} {
		if got[id] != want {
			t.Errorf("menu shortcut of %s: %q, want %q", id, got[id], want)
		}
	}
	// One press, one run: a global action and the editor's Cmd+/.
	runs := 0
	r.AddAction(Action{ID: "test.count", Title: "Count", Mac: "Cmd+Shift+Y", PC: "Ctrl+Shift+Y", Menu: "Help", Run: func(*View) { runs++ }})
	e.frame() // the window's key filters are registered for the next frame
	e.key("Y", key.ModCommand|key.ModShift)
	if runs != 1 {
		t.Fatalf("ran %d times", runs)
	}
	e.v.runMenuID("test.count") // native menu item
	e.frame()
	if runs != 2 {
		t.Fatalf("menu ran %d times", runs)
	}
	s := e.openEditor("src/Shop.php")
	e.caretAt(s, "enum", 0)
	e.key("/", key.ModCommand)
	s.doc.Lock()
	text := string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len()))
	s.doc.Unlock()
	if !strings.Contains(text, "// enum Suit") {
		t.Fatalf("Cmd+/ toggled twice or not at all:\n%s", text)
	}
}

// Find in Files streams real results into the Find window; the replace
// preview lists the replacements before anything is written.
func TestFindInFilesWindow(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.openEditor("src/Room.php")
	e.key("F", key.ModCommand|key.ModShift)
	if e.v.popup == nil || e.v.popup.Title != "Find in Files" {
		t.Fatal("no find dialog")
	}
	f := e.v.navd.find
	f.pattern.SetText("area")
	f.mask.SetText("*.php")
	e.frame()
	e.shot("nav-find-dialog")
	e.v.popup.Buttons[0].OnClick()
	e.v.ClosePopup()
	e.waitFor("results", func() bool { st := e.v.navd.n.Find; return st != nil && !st.Running && st.Matches == 4 })
	e.frame()
	if !e.v.ToolShown(ToolFind) || f.tree.rowCount() != 4+4 {
		t.Fatalf("find window rows %d", f.tree.rowCount())
	}
	e.shot("nav-find-window")
	// Replace: preview first, nothing written yet.
	e.key("R", key.ModCommand|key.ModShift)
	if e.v.popup == nil || e.v.popup.Title != "Replace in Files" {
		t.Fatal("no replace dialog")
	}
	f.pattern.SetText("area")
	f.replace.SetText("surface")
	e.v.popup.Buttons[0].OnClick()
	e.v.ClosePopup()
	e.waitFor("preview", func() bool { st := e.v.navd.n.Find; return st != nil && st.Replace != nil })
	e.frame()
	room, _ := e.app.Document(filepath.Join(e.root, "src", "Room.php"))
	if room == nil {
		t.Fatal("room not open")
	}
	room.Lock()
	before := room.Buffer().String()
	room.Unlock()
	if strings.Contains(before, "surface") {
		t.Fatal("replaced before applying")
	}
	e.v.navd.n.ApplyReplace(nil)
	e.waitFor("replaced", func() bool { st := e.v.navd.n.Find; return st != nil && st.Replaced != nil })
	room.Lock()
	after := room.Buffer().String()
	room.Unlock()
	if !strings.Contains(after, "->surface()") {
		t.Fatalf("open document not replaced:\n%s", after)
	}
}

// The Usages window groups results by file (fed directly: the test
// environment has no language server).
func TestUsagesWindow(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.openEditor("src/Room.php")
	n := e.v.navd.n
	if e.v.Registry.HasHandler(string(ActFindUsages)) {
		e.v.runAction(ActFindUsages)
		e.frame()
		if !strings.Contains(e.ctl.Status.Notice, "language server") {
			t.Fatalf("notice %q", e.ctl.Status.Notice)
		}
	}
	at := func(rel string, line, off int, text string) controller.Usage {
		return controller.Usage{Location: controller.Location{Path: rel, File: filepath.Join(e.root, rel), Range: controller.TextRange{Start: controller.TextPos{Line: line, Col: 9, Offset: off}}}, LineText: text}
	}
	n.Usages = &controller.Usages{Symbol: "area", Total: 3, Groups: []controller.UsageGroup{
		{Path: "src/Circle.php", Items: []controller.Usage{at("src/Circle.php", 7, 70, "    public function area(): float")}},
		{Path: "src/Room.php", Items: []controller.Usage{at("src/Room.php", 13, 200, "        return $this->floor->area();"), at("src/Room.php", 14, 230, "        // area")}},
	}}
	n.UsagesVersion++
	e.frame()
	e.frame()
	if !e.v.ToolShown(ToolUsages) || e.v.navd.usages.rowCount() != 5 {
		t.Fatalf("usages rows %d", e.v.navd.usages.rowCount())
	}
	e.shot("nav-usages")
	// Opening a usage jumps there.
	e.v.navd.usages.Roots[0].Children[0].Open()
	e.waitFor("jump", func() bool { return strings.HasSuffix(e.ctl.ActiveTab().File, "Circle.php") })
}
