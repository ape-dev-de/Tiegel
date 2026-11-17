package settings

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func ip(n int) *int { return &n }

func samplePages() []Page {
	return []Page{
		{ID: "core:Appearance", Title: "Appearance", Fields: []Field{
			{Key: "ui.theme", Kind: KindEnum, Label: "Theme", Options: []string{"dark", "light", "system"}, Value: "light", Source: "global", HasGlobal: true},
			{Key: "ui.fontSize", Kind: KindFloat, Label: "UI font size", Value: 13.0, Source: "default", Min: ip(9), Max: ip(24)},
		}},
		{ID: "core:Editor", Title: "Editor", Extra: ExtraFontPreview, Fields: []Field{
			{Key: "editor.fontFamily", Kind: KindString, Label: "Editor font", Value: "", Source: "default", Choices: []string{"Menlo", "Monaco"}, ChoiceDefault: "System default"},
			{Key: "editor.tabWidth", Kind: KindInt, Label: "Tab width", Help: "Indent size.", Value: 4.0, Source: "default", Min: ip(1), Max: ip(16)},
			{Key: "editor.insertSpaces", Kind: KindBool, Label: "Indent with spaces", Value: true, Source: "default"},
		}},
		{ID: "core:Files", Title: "Files", Fields: []Field{
			{Key: "files.associations", Kind: KindList, Label: "File type associations", Value: []any{"*.tpl=php"}, Source: "project", HasProject: true, ProjectScope: true, Group: "Types"},
			{Key: "files.editorconfig", Kind: KindBool, Label: "Use .editorconfig", Value: true, Source: "default", ProjectScope: true, Invalid: "want bool"},
		}},
		{ID: "core:Interpreter", Title: "Interpreter", Extra: ExtraInterpreter},
		{ID: "core:Updates", Title: "Updates", Extra: ExtraUpdates, Fields: []Field{
			{Key: "update.checkOnStart", Kind: KindBool, Label: "Check for updates on start", Value: false, Source: "default"},
		}},
		{ID: "plugin:git", Title: "git", Module: &Module{ID: "git", Enabled: true, Running: true, Dependents: []string{"x"}}, Fields: []Field{
			{Key: "git.updateMethod", Kind: KindEnum, Label: "Update method", Options: []string{"merge", "rebase"}, Value: "merge", Source: "default", ProjectScope: true},
		}},
		{ID: "plugin:qa-tool", Title: "qa-tool", Module: &Module{ID: "qa-tool", Enabled: true, Error: "not found", DependsOn: []string{"php-runtime"}}, Fields: []Field{
			{Key: "qa-tool.path", Kind: KindPath, Label: "Executable", Value: "vendor/bin/tool", Source: "default", ProjectScope: true},
		}},
		{ID: "plugin:empty", Title: "empty", Module: &Module{ID: "empty", Enabled: true}},
		{ID: "plugin:off", Title: "off", Module: &Module{ID: "off"}, Fields: []Field{
			{Key: "off.x", Kind: KindBool, Label: "X", Value: false},
		}},
	}
}

func TestHelpers(t *testing.T) {
	pages := samplePages()
	if r := Results(pages, ""); len(r) != len(pages) {
		t.Fatal("empty query")
	}
	r := Results(pages, "TAB indent")
	if len(r) != 1 || r[0].ID != "core:Editor" || len(r[0].Fields) != 1 {
		t.Fatalf("results %+v", r)
	}
	if r := Results(pages, "git update"); len(r) != 1 || r[0].Fields[0].Key != "git.updateMethod" {
		t.Fatalf("plugin %+v", r)
	}
	if len(Results(pages, "zzz")) != 0 {
		t.Fatal("no match")
	}
	cases := []struct {
		kind string
		v    any
		want string
	}{
		{KindList, []any{"a", "b"}, "a, b"}, {KindList, []string{"x"}, "x"}, {KindInt, 4.0, "4"}, {KindInt, 3, "3"},
		{KindFloat, 1.4, "1.4"}, {KindFloat, 13.0, "13"}, {KindBool, true, "true"}, {KindString, nil, ""}, {KindString, "s", "s"},
	}
	for _, c := range cases {
		if got := FormatValue(c.kind, c.v); got != c.want {
			t.Errorf("%s %v: %q", c.kind, c.v, got)
		}
	}
	f := Field{Kind: KindInt, Min: ip(1), Max: ip(16)}
	if v, err := ParseValue(f, " 8 "); err != nil || v != 8 {
		t.Fatalf("int %v %v", v, err)
	}
	for _, bad := range []string{"0", "17", "x"} {
		if _, err := ParseValue(f, bad); err == nil {
			t.Errorf("int %q accepted", bad)
		}
	}
	if v, err := ParseValue(Field{Kind: KindFloat}, "1,5"); err != nil || v != 1.5 {
		t.Fatalf("float %v %v", v, err)
	}
	if _, err := ParseValue(Field{Kind: KindFloat}, "x"); err == nil {
		t.Fatal("float")
	}
	if v, _ := ParseValue(Field{Kind: KindList}, "a, ,b"); !reflect.DeepEqual(v, []string{"a", "b"}) {
		t.Fatalf("list %v", v)
	}
	if v, _ := ParseValue(Field{Kind: KindBool}, "true"); v != true {
		t.Fatal("bool")
	}
	if v, _ := ParseValue(Field{Kind: KindPath}, " /x "); v != "/x" {
		t.Fatal("path")
	}
}

func TestPanelPages(t *testing.T) {
	p := &Panel{Pages: samplePages(), UpdateStatus: "Tiegel is up to date"}
	p.Interpreter.Items = []runtime.Interpreter{{ID: "local", Label: "PHP 8.3", Kind: runtime.KindLocal}}
	p.Mappings.Items = []runtime.Mapping{{Host: ".", Remote: "/var/www"}}
	p.Hints.Items = []runtime.Hint{{Severity: runtime.SeverityWarning, Code: "mode", Message: "mode lacks debug"}}
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		p.Update(gtx)
		return p.Layout(gtx, th)
	}
	for _, pg := range p.Pages {
		p.Selected = pg.ID
		uitest.Smoke(t, pg.ID, image.Pt(1000, 700), w)
	}
	p.Scope = ScopeProject
	p.Selected = "core:Files"
	uitest.Smoke(t, "project scope", image.Pt(1000, 700), w)
	p.Selected = "core:Appearance"
	uitest.Smoke(t, "global only in project scope", image.Pt(1000, 700), w)
	p.NoProject = true
	p.Selected = "core:Interpreter"
	uitest.Smoke(t, "no project", image.Pt(1000, 700), w)
	p.SetQuery("update")
	uitest.Smoke(t, "search", image.Pt(1000, 700), w)
	p.SetQuery("nothing-matches")
	uitest.Smoke(t, "search empty", image.Pt(1000, 700), w)
	if p.Query() != "nothing-matches" {
		t.Fatal("query")
	}
	p.SetQuery("")
	p.Pages = nil
	uitest.Smoke(t, "no pages", image.Pt(1000, 700), w)
	if p.Page() != nil {
		t.Fatal("page without pages")
	}
}

func TestPanelEvents(t *testing.T) {
	p := &Panel{Pages: samplePages()}
	var evs []Event
	blur := false
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		if blur {
			gtx.Execute(key.FocusCmd{})
			blur = false
		}
		evs = append(evs, p.Update(gtx)...)
		return p.Layout(gtx, th)
	}
	h := uitest.NewHarness(image.Pt(1000, 700))
	h.Frame(w)
	if p.Page().ID != "core:Appearance" {
		t.Fatalf("default page %s", p.Page().ID)
	}

	// Tree navigation: scan the nav column for the "Editor" row.
	for y := 60; y < 400 && p.Selected != "core:Editor"; y += 6 {
		h.Click(image.Pt(80, y))
		h.Frame(w)
	}
	if p.Selected != "core:Editor" {
		t.Fatal("Editor page not reachable from the tree")
	}

	// A bool field: click the checkbox → SetEvent false (was true).
	evs = nil
	x := 220 + 1 + 16 + 8
	for y := 40; y < 500 && len(evs) == 0; y += 4 {
		h.Click(image.Pt(x, y))
		h.Frame(w)
		for _, e := range evs {
			if _, ok := e.(SetEvent); !ok {
				evs = nil // a dropdown opened or similar: ignore
			}
		}
		p.fields["editor.fontFamily"].sel.Open = false
	}
	want := SetEvent{Key: "editor.insertSpaces", Scope: ScopeGlobal, Value: false}
	if len(evs) != 1 || !reflect.DeepEqual(evs[0], want) {
		t.Fatalf("events %+v", evs)
	}

	// Text fields commit on focus loss; invalid input is reported.
	st := p.fields["editor.tabWidth"]
	blur = true
	h.Frame(w)
	st.text.SetText("abc")
	st.focus = true
	evs = nil
	h.Frame(w)
	if len(evs) != 0 || st.err == "" || !st.text.Invalid {
		t.Fatalf("invalid: %+v %q", evs, st.err)
	}
	st.err, st.text.Invalid = "", false
	st.text.SetText("8")
	st.focus = true
	h.Frame(w)
	want = SetEvent{Key: "editor.tabWidth", Scope: ScopeGlobal, Value: 8}
	if len(evs) != 1 || !reflect.DeepEqual(evs[0], want) {
		t.Fatalf("commit: %+v", evs)
	}

	// Project scope edits project values; reset only where a value exists.
	p.Scope = ScopeProject
	p.Selected = "core:Files"
	h.Frame(w)
	if got := p.editScope(p.Page().Fields[0]); got != ScopeProject {
		t.Fatalf("scope %s", got)
	}
	if got := p.editScope(Field{}); got != ScopeGlobal {
		t.Fatal("global-only field")
	}
	if !storedIn(p.Page().Fields[0], ScopeProject) || storedIn(p.Page().Fields[1], ScopeProject) {
		t.Fatal("storedIn")
	}
}
