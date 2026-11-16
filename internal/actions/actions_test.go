package actions

import (
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

func labels(es []Entry) []string {
	var out []string
	for _, e := range es {
		switch {
		case e.Separator:
			out = append(out, "-")
		case len(e.Children) > 0:
			out = append(out, e.Label+"{"+strings.Join(labels(e.Children), ",")+"}")
		default:
			out = append(out, e.Label)
		}
	}
	return out
}

func TestAddValidation(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(Action{ID: "a"}); err == nil {
		t.Error("missing label accepted")
	}
	if err := r.Add(Action{ID: "a", Label: "A"}, Action{ID: "a", Label: "B"}); err == nil {
		t.Error("duplicate in one call accepted")
	}
	if err := r.Add(Action{ID: "a", Label: "A", Shortcut: Shortcut{Mac: "Cmd+Nope"}}); err == nil {
		t.Error("bad chord accepted")
	}
	if len(r.All()) != 0 {
		t.Fatal("failed Add left actions behind")
	}
	v := r.Version()
	if err := r.Add(Action{ID: "a", Label: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Action{ID: "a", Label: "A"}); err == nil {
		t.Error("duplicate accepted")
	}
	if r.Version() == v {
		t.Error("version unchanged")
	}
	if _, ok := r.Get("a"); !ok {
		t.Fatal("get")
	}
	r.Remove("a")
	if _, ok := r.Get("a"); ok {
		t.Fatal("not removed")
	}
}

func TestResolve(t *testing.T) {
	r := NewRegistry()
	checked := false
	err := r.Add(
		Action{ID: "x.copy", Label: "Copy", Icon: "copy", Shortcut: Shortcut{Mac: "Cmd+C", PC: "Ctrl+C"}, Enabled: func(c Context) bool { return c.Selection }},
		Action{ID: "x.hidden", Label: "Hidden", Visible: func(Context) bool { return false }},
		Action{ID: "x.wrap", Label: "Soft Wrap", Check: CheckBox, Checked: func(Context) bool { return checked }},
		Action{ID: "x.lf", Label: "LF", Check: CheckRadio, Checked: func(Context) bool { return true }},
	)
	if err != nil {
		t.Fatal(err)
	}
	r.SetMenu(CtxEditor,
		Separator(), Item("x.copy"), Separator(), Separator(), Item("x.hidden"), Item("missing"),
		Sub("Empty", Item("x.hidden")),
		Sub("View", Item("x.wrap"), Separator(), Item("x.lf")),
		Separator(),
	)
	es := r.Resolve(Context{Menu: CtxEditor}, nil, "darwin")
	if got := strings.Join(labels(es), "|"); got != "Copy|-|View{Soft Wrap,-,LF}" {
		t.Fatalf("labels %q", got)
	}
	if !es[0].Disabled || es[0].Shortcut != "Cmd+C" || es[0].Icon != "copy" || es[0].ID != "x.copy" {
		t.Fatalf("copy entry %+v", es[0])
	}
	if es := r.Resolve(Context{Menu: CtxEditor, Selection: true}, nil, "linux"); es[0].Disabled || es[0].Shortcut != "Ctrl+C" {
		t.Fatalf("enabled copy %+v", es[0])
	}
	view := es[2].Children
	if view[0].Check != CheckBox || view[0].Checked || view[2].Check != CheckRadio || !view[2].Checked {
		t.Fatalf("check states %+v", view)
	}
	// Rebound shortcut wins over the default.
	km := keymap.New("test", keymap.Binding{Chord: keymap.MustParse("Cmd+Shift+K"), Action: "x.copy"})
	if es := r.Resolve(Context{Menu: CtxEditor}, km, "darwin"); es[0].Shortcut != "Cmd+Shift+K" {
		t.Fatalf("rebound %q", es[0].Shortcut)
	}
	if s := r.ShortcutText("x.copy", nil, "windows"); s != "Ctrl+C" {
		t.Fatalf("ShortcutText %q", s)
	}
	if r.ShortcutText("x.wrap", nil, "darwin") != "" || r.ShortcutText("none", nil, "darwin") != "" {
		t.Fatal("unexpected shortcut")
	}
	// AppendMenu adds a separated group; label override.
	r.AppendMenu(CtxEditor, MenuItem{Action: "x.copy", Label: "Copy Again"})
	if got := labels(r.Resolve(Context{Menu: CtxEditor}, nil, "linux")); got[len(got)-2] != "-" || got[len(got)-1] != "Copy Again" {
		t.Fatalf("append %q", got)
	}
	r.AppendMenu(CtxGitLog, Item("x.copy"))
	if got := labels(r.Resolve(Context{Menu: CtxGitLog}, nil, "linux")); len(got) != 1 {
		t.Fatalf("append to empty %q", got)
	}
}

func TestFind(t *testing.T) {
	r := Standard("darwin")
	r.SetMenu(CtxMain, Sub("File", Sub("New", Item(FileNewPHPClass))))
	ms := r.Find("php class", Context{}, nil, "darwin")
	if len(ms) == 0 || ms[0].ID != FileNewPHPClass || ms[0].Path != "File › New" {
		t.Fatalf("find %+v", ms)
	}
	ms = r.Find("rename…", Context{Paths: []string{"a.php"}}, nil, "darwin")
	if len(ms) == 0 || ms[0].ID != FileRename || ms[0].Disabled || ms[0].Shortcut != "Shift+F6" {
		t.Fatalf("rename %+v", ms)
	}
	if ms := r.Find("trash", Context{}, nil, "linux"); len(ms) != 1 || ms[0].ID != FileDelete || !ms[0].Disabled {
		t.Fatalf("keyword %+v", ms)
	}
	if ms := r.Find("zzzz", Context{}, nil, "linux"); len(ms) != 0 {
		t.Fatalf("no match %+v", ms)
	}
	all := r.Find("", Context{Language: "php", Values: map[string]string{"git": "1"}}, nil, "linux")
	if len(all) != len(r.All()) {
		t.Fatalf("empty query lists %d of %d", len(all), len(r.All()))
	}
	// Invisible actions are not found, PHP-only ones are disabled.
	if ms := r.Find("annotate", Context{Language: "text"}, nil, "linux"); len(ms) != 0 {
		t.Fatalf("invisible found %+v", ms)
	}
	if ms := r.Find("go to definition", Context{Language: "text"}, nil, "linux"); len(ms) != 1 || !ms[0].Disabled {
		t.Fatalf("php-only action %+v", ms)
	}
	if ms := r.Find("close", Context{}, nil, "linux"); ms[0].Label != "Close" {
		t.Fatalf("prefix ranking %+v", ms[0])
	}
}

func TestStandard(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		r := Standard(goos)
		if len(r.All()) != len(StandardActions()) {
			t.Fatalf("%s: standard actions failed to register", goos)
		}
		a, _ := r.Get(FileReveal)
		if a.Label != RevealLabel(goos) {
			t.Errorf("reveal label %q", a.Label)
		}
		for _, ctx := range append(slices.Clone(Contexts), CtxMain) {
			items := r.Menu(ctx)
			if len(items) == 0 {
				t.Fatalf("%s: no menu", ctx)
			}
			var check func([]MenuItem)
			check = func(items []MenuItem) {
				for _, it := range items {
					if it.Action != "" {
						if _, ok := r.Get(it.Action); !ok {
							t.Errorf("%s: unknown action %q", ctx, it.Action)
						}
					}
					check(it.Children)
				}
			}
			check(items)
			c := Context{Menu: ctx, Paths: []string{"src/A.php"}, Language: "php", Selection: true, Values: map[string]string{"git": "1", "commit": "abc"}}
			if es := r.Resolve(c, nil, goos); len(es) == 0 {
				t.Errorf("%s: resolves empty", ctx)
			}
		}
		if bs := r.Bindings(goos); len(bs) < 10 {
			t.Errorf("bindings %d", len(bs))
		}
	}
	tree := labels(Standard("darwin").Resolve(Context{Menu: CtxProjectTree}, nil, "darwin"))
	if tree[0] != "New{File…,Directory…,-,PHP Class…}" {
		t.Fatalf("tree menu %q", tree)
	}
}

func TestContextHelpers(t *testing.T) {
	c := Context{Paths: []string{"a", "b"}, Dirs: []bool{false, true}, Values: map[string]string{"k": "v"}}
	if c.Single() || c.IsDir(0) || !c.IsDir(1) || c.IsDir(5) || c.Value("k") != "v" || c.Value("x") != "" {
		t.Fatal("helpers")
	}
}

func TestFormatChord(t *testing.T) {
	for _, tc := range []struct{ chord, goos, want string }{
		{"Cmd+Shift+Z", "darwin", "Cmd+Shift+Z"},
		{"Ctrl+Alt+Shift+Cmd+Left", "darwin", "Cmd+Ctrl+Alt+Shift+←"},
		{"Cmd+Backspace", "darwin", "Cmd+Backspace"},
		{"Right", "darwin", "→"},
		{"Up", "linux", "↑"},
		{"Down", "linux", "↓"},
		{"Ctrl+Shift+N", "linux", "Ctrl+Shift+N"},
		{"Cmd+Alt+Delete", "windows", "Alt+Meta+Del"},
		{"Escape", "linux", "Esc"},
		{"PageUp", "linux", "PgUp"},
		{"PageDown", "darwin", "PgDn"},
		{"F6", "darwin", "F6"},
	} {
		if got := FormatChord(keymap.MustParse(tc.chord), tc.goos); got != tc.want {
			t.Errorf("%s/%s = %q, want %q", tc.chord, tc.goos, got, tc.want)
		}
	}
	for _, tc := range []struct{ chord, want string }{
		{"Cmd+Shift+Z", "⇧⌘Z"},
		{"Ctrl+Alt+Shift+Cmd+Left", "⌃⌥⇧⌘←"},
		{"Cmd+Backspace", "⌘⌫"},
		{"Enter", "↩"}, {"Escape", "⎋"}, {"Tab", "⇥"}, {"Space", "Space"}, {"Right", "→"},
		{"Up", "↑"}, {"Down", "↓"}, {"Delete", "⌦"}, {"PageUp", "⇞"}, {"PageDown", "⇟"},
		{"Home", "↖"}, {"End", "↘"}, {"F6", "F6"},
	} {
		if got := FormatChordSymbols(keymap.MustParse(tc.chord)); got != tc.want {
			t.Errorf("symbols %s = %q, want %q", tc.chord, got, tc.want)
		}
	}
	if FormatChord(keymap.Chord{}, "darwin") != "" || FormatChordSymbols(keymap.Chord{}) != "" {
		t.Error("empty chord")
	}
}
