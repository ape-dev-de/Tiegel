package keymap

import (
	"errors"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

func TestDefaultsHaveNoConflicts(t *testing.T) {
	for _, p := range Presets {
		if cs := NewScheme(p, nil).Conflicts(); len(cs) > 0 {
			t.Errorf("preset %s: conflicts %+v", p, cs)
		}
	}
}

func TestCatalogIsConsistent(t *testing.T) {
	seen := map[Action]bool{}
	for _, d := range Defs() {
		if seen[d.ID] {
			t.Errorf("duplicate action %s", d.ID)
		}
		seen[d.ID] = true
		if d.Title == "" || !d.Context.Valid() {
			t.Errorf("%s: title %q context %q", d.ID, d.Title, d.Context)
		}
		for _, list := range [][]string{d.Mac, d.PC} {
			for _, s := range list {
				if _, err := ParseChord(s); err != nil {
					t.Errorf("%s: %v", d.ID, err)
				}
			}
		}
	}
	// Every action of the editor tables has a catalog entry.
	for _, k := range []*Keymap{Mac(), PC()} {
		for _, b := range k.Bindings() {
			if !IsWidgetAction(b.Action) {
				t.Errorf("table action %s has no title", b.Action)
			}
		}
	}
}

func TestGestureChords(t *testing.T) {
	c, err := ParseChord("mouseback")
	if err != nil || c != (Chord{Key: KeyMouseBack}) || !IsGesture(c) {
		t.Fatalf("ParseChord(mouseback) = %v, %v", c, err)
	}
	if _, err := ParseChord("Cmd+SwipeBack"); !errors.Is(err, ErrBadChord) {
		t.Fatalf("gesture with modifier: %v", err)
	}
	if IsGesture(MustParse("Cmd+[")) {
		t.Fatal("Cmd+[ is a key")
	}
	s := NewScheme(PresetMac, nil)
	for g, want := range map[string]Action{KeyMouseBack: NavBack, KeyMouseForward: NavForward, KeySwipeBack: NavBack, KeySwipeForward: NavForward, KeyDoubleShift: "navigate.searchEverywhere"} {
		if a, ok := s.Lookup(CtxEditor, Chord{Key: g}); !ok || a != want {
			t.Errorf("%s → %q", g, a)
		}
	}
	// Gestures are not keyboard bindings.
	if _, ok := s.Keymap(CtxGlobal, nil).Lookup(Chord{Key: KeyMouseBack}); ok {
		t.Error("gesture in keyboard keymap")
	}
	if got := s.ChordText(NavBack); got != "Cmd+[" {
		t.Errorf("ChordText(back) = %q", got)
	}
}

func TestMultipleChordsAndContexts(t *testing.T) {
	s := NewScheme(PresetMac, nil)
	back := s.Chords(NavBack)
	if !slices.Contains(back, MustParse("Cmd+Alt+Left")) || !slices.Contains(back, MustParse("Cmd+[")) {
		t.Fatalf("back chords %v", back)
	}
	// Cmd+D duplicates in the editor and shows the diff in tool windows.
	if a, _ := s.Lookup(CtxEditor, MustParse("Cmd+D")); a != DuplicateLines {
		t.Errorf("editor Cmd+D = %s", a)
	}
	if a, _ := s.Lookup(CtxToolWindow, MustParse("Cmd+D")); a != "git.showDiff" {
		t.Errorf("tool window Cmd+D = %s", a)
	}
	// Global bindings are seen from every context.
	if a, _ := s.Lookup(CtxToolWindow, MustParse("Cmd+Shift+O")); a != "file.quickOpen" {
		t.Errorf("tool window Cmd+Shift+O = %s", a)
	}
	if _, ok := s.Lookup(CtxGlobal, MustParse("Cmd+D")); ok {
		t.Error("Cmd+D is not global")
	}
	ek := s.EditorKeymap()
	if a, _ := ek.Lookup(MustParse("Alt+Left")); a != MoveWordLeft {
		t.Errorf("editor keymap Alt+Left = %s", a)
	}
	if _, ok := ek.Lookup(MustParse("Cmd+B")); ok {
		t.Error("window action in widget keymap")
	}
	if _, ok := s.Entry("nope"); ok || s.Has("nope") || s.Chords("nope") != nil || s.ChordText("nope") != "" {
		t.Error("unknown action")
	}
	if s.Preset() != PresetMac || NewScheme("bogus", nil).Preset() != PresetPC {
		t.Error("preset")
	}
}

func TestOverridesAndConflicts(t *testing.T) {
	ov := Overrides{
		NavBack:        {MustParse("Ctrl+Alt+Left")},
		"tab.close":    nil,                      // unbound
		"app.settings": {MustParse("Cmd+B")},     // global: clashes with editor Go to Definition
		"git.showDiff": {MustParse("Cmd+Alt+D")}, // tool window only
		"unknown":      {MustParse("F5")},
	}
	s := NewScheme(PresetMac, ov, Def{ID: "plugin.hello", Title: "Hello", Context: "weird", Mac: []string{"Cmd+Alt+H"}})
	e, _ := s.Entry(NavBack)
	if e.Source != SourceUser || len(e.Chords) != 1 || len(e.Defaults) != 4 {
		t.Fatalf("back entry %+v", e)
	}
	if _, ok := s.Lookup(CtxGlobal, MustParse("Cmd+[")); ok {
		t.Error("overridden default still bound")
	}
	if len(s.Chords("tab.close")) != 0 {
		t.Error("tab.close should be unbound")
	}
	p, _ := s.Entry("plugin.hello")
	if p.Context != CtxGlobal || len(p.Chords) != 1 {
		t.Errorf("plugin entry %+v", p)
	}
	cs := s.Conflicts()
	if len(cs) != 1 || cs[0].A != "app.settings" || cs[0].B != "navigate.definition" || cs[0].Chord != MustParse("Cmd+B") {
		t.Fatalf("conflicts %+v", cs)
	}
	if len(s.ConflictsOf("navigate.definition")) != 1 || len(s.ConflictsOf(NavBack)) != 0 {
		t.Error("ConflictsOf")
	}
	// In the editor the editor binding wins; elsewhere the global one.
	if a, _ := s.Lookup(CtxEditor, MustParse("Cmd+B")); a != "navigate.definition" {
		t.Errorf("editor Cmd+B = %s", a)
	}
	if a, _ := s.Lookup(CtxToolWindow, MustParse("Cmd+B")); a != "app.settings" {
		t.Errorf("tool window Cmd+B = %s", a)
	}

	// Stored form round trip.
	lines := ov.Format()
	back, err := ParseOverrides(append(lines, "  "))
	if err != nil || len(back) != len(ov) || len(back["tab.close"]) != 0 || back[NavBack][0] != MustParse("Ctrl+Alt+Left") {
		t.Fatalf("round trip %v → %v, %v", lines, back, err)
	}
	if _, err := ParseOverrides([]string{"x Cmd+Nope"}); !errors.Is(err, ErrBadChord) {
		t.Fatalf("bad override: %v", err)
	}
	c := ov.Clone()
	c[NavBack][0] = MustParse("F1")
	if ov[NavBack][0] == c[NavBack][0] {
		t.Error("Clone shares chords")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	for _, sp := range SettingSpecs("darwin") {
		if err := sp.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if SettingSpecs("linux")[0].Default != "pc" || SettingSpecs("darwin")[0].Default != "mac" {
		t.Fatal("preset default per OS")
	}
	if p, ov, err := Load(nil, "darwin"); p != PresetMac || len(ov) != 0 || err != nil {
		t.Fatal("nil store")
	}
	dir := t.TempDir()
	st, err := settings.OpenGlobal(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, _, _ := Load(st, "linux"); p != PresetPC {
		t.Fatal("default preset")
	}
	ov := Overrides{NavBack: {MustParse("Cmd+Alt+Left"), Chord{Key: KeyMouseBack}}}
	if err := Store(st, PresetPC, ov); err != nil {
		t.Fatal(err)
	}
	// A new store on the same directory (restart) sees the overrides.
	st2, err := settings.OpenGlobal(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, got, err := Load(st2, "darwin")
	if err != nil || p != PresetPC || len(got[NavBack]) != 2 || got[NavBack][1].Key != KeyMouseBack {
		t.Fatalf("after restart: %s %v %v", p, got, err)
	}
	if err := Store(st2, "", nil); err != nil {
		t.Fatal(err)
	}
	if st2.Has(SettingPreset) || st2.Has(SettingOverrides) {
		t.Fatal("reset keeps keys")
	}
	// Broken stored values fall back to defaults with an error.
	_ = settings.Set(st2, SettingPreset, "dvorak")
	_ = settings.Set(st2, SettingOverrides, []string{"a Cmd+Nope"})
	if p, ov, err := Load(st2, "darwin"); err == nil || p != PresetMac || len(ov) != 0 {
		t.Fatalf("broken: %s %v %v", p, ov, err)
	}
	_ = settings.Set(st2, SettingPreset, 3)
	_ = settings.Set(st2, SettingOverrides, 3)
	if _, _, err := Load(st2, "darwin"); err == nil {
		t.Fatal("wrong types should be reported")
	}
}

func TestContextAndPreset(t *testing.T) {
	if !CtxGlobal.Overlaps(CtxEditor) || CtxEditor.Overlaps(CtxToolWindow) || !CtxEditor.Overlaps(CtxEditor) {
		t.Fatal("overlaps")
	}
	if Context("x").Valid() || !PresetMac.Valid() || Preset("x").Valid() {
		t.Fatal("valid")
	}
	if PresetFor("darwin") != PresetMac || PresetFor("windows") != PresetPC {
		t.Fatal("PresetFor")
	}
}

// TestUserBindingShadowsEarlierDefault checks that a user binding wins over
// a default binding of the same chord in the same context, even when the
// default's action comes first in the catalog (D247).
func TestUserBindingShadowsEarlierDefault(t *testing.T) {
	back := MustParse("Cmd+[") // default of NavBack (global, early in the catalog)
	ov := Overrides{"plugin.late": {back}}
	s := NewScheme(PresetMac, ov, Def{ID: "plugin.late", Title: "Late", Context: CtxGlobal, Mac: []string{"Cmd+Alt+J"}})
	if a, ok := s.Lookup(CtxGlobal, back); !ok || a != "plugin.late" {
		t.Fatalf("Lookup = %s, %v", a, ok)
	}
	if a, ok := s.Keymap(CtxGlobal, nil).Lookup(back); !ok || a != "plugin.late" {
		t.Fatalf("Keymap = %s, %v", a, ok)
	}
	if !s.IsUser("plugin.late") || s.IsUser(NavBack) || s.IsUser("nope") {
		t.Error("IsUser")
	}
	found := false
	for _, c := range s.ConflictsOf("plugin.late") {
		found = found || (c.Chord == back && (c.A == NavBack || c.B == NavBack))
	}
	if !found {
		t.Errorf("conflict not reported: %+v", s.ConflictsOf("plugin.late"))
	}
	// Editor context: a user binding of a later editor action wins too.
	ed := Overrides{"plugin.edit": {MustParse("Cmd+D")}}
	s = NewScheme(PresetMac, ed, Def{ID: "plugin.edit", Title: "Edit", Context: CtxEditor})
	if a, _ := NewScheme(PresetMac, nil).Lookup(CtxEditor, MustParse("Cmd+D")); a == "" {
		t.Skip("Cmd+D has no editor default in the mac preset")
	}
	if a, _ := s.Lookup(CtxEditor, MustParse("Cmd+D")); a != "plugin.edit" {
		t.Errorf("editor Lookup = %s", a)
	}
	if a, _ := s.Keymap(CtxEditor, nil).Lookup(MustParse("Cmd+D")); a != "plugin.edit" {
		t.Errorf("editor Keymap = %s", a)
	}
}
