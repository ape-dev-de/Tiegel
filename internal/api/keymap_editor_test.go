package api

import (
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// TestKeymapEditorOperations covers the methods behind the keymap editor
// (AP13.3): check before binding, exclusive add, removing one chord,
// shadowed chords and dropping an invalid stored line.
func TestKeymapEditorOperations(t *testing.T) {
	a := newTestApp(t)
	var res KeymapSetResult
	call(t, a, "keymap.set", KeymapSetParams{Preset: "mac"}, &res)

	// check: Cmd+B is Go to Definition (editor) → conflicts globally.
	var cs []KeymapConflict
	call(t, a, "keymap.check", KeymapCheckParams{Action: "app.settings", Chord: "cmd+b"}, &cs)
	if len(cs) != 1 || cs[0].Action != "app.settings" || cs[0].Other != "navigate.definition" || cs[0].OtherCtx != "editor" {
		t.Fatalf("check %+v", cs)
	}
	call(t, a, "keymap.check", KeymapCheckParams{Action: "app.settings", Chord: "Cmd+Ctrl+Alt+Shift+F9"}, &cs)
	if len(cs) != 0 {
		t.Fatalf("free %+v", cs)
	}
	wantCode(t, a, "keymap.check", KeymapCheckParams{Action: "app.settings", Chord: "Hyper+X"}, CodeInvalidParams)
	wantCode(t, a, "keymap.check", KeymapCheckParams{Action: "nope", Chord: "F1"}, CodeNotFound)

	// Exclusive add takes the chord away from the other action.
	call(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"Cmd+B"}, Add: true, Exclusive: true}, &res)
	if len(res.Conflicts) != 0 || !slices.Equal(res.RemovedFrom, []string{"navigate.definition"}) || !slices.Contains(res.Binding.Chords, "Cmd+B") {
		t.Fatalf("exclusive %+v", res)
	}
	var st KeymapState
	call(t, a, "keymap.list", KeymapListParams{Query: "navigate.definition"}, &st)
	if def, _ := binding(st, "navigate.definition"); def.Source != "user" || slices.Contains(def.Chords, "Cmd+B") {
		t.Fatalf("definition %+v", def)
	}

	// A plain (non-exclusive) set shadows a default of the same context.
	call(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"Cmd+["}, Add: true}, &res)
	call(t, a, "keymap.list", KeymapListParams{Query: "navigate.back"}, &st)
	if back, _ := binding(st, "navigate.back"); !slices.Equal(back.Shadowed, []string{"Cmd+["}) {
		t.Fatalf("shadowed %+v", back)
	}

	// remove: one chord at a time; back to the defaults drops the override.
	call(t, a, "keymap.set", KeymapSetParams{Action: "navigate.back", Chords: []string{"MouseBack"}, Remove: true}, &res)
	if res.Binding.Source != "user" || slices.Contains(res.Binding.Chords, "MouseBack") {
		t.Fatalf("remove %+v", res.Binding)
	}
	call(t, a, "keymap.set", KeymapSetParams{Action: "navigate.back", Chords: []string{"MouseBack"}, Add: true}, &res)
	if res.Binding.Source != "user" {
		t.Fatal("add keeps an override")
	}
	call(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"Cmd+B", "Cmd+["}, Remove: true}, &res)
	if res.Binding.Source != "default" {
		t.Fatalf("remove back to defaults %+v", res.Binding)
	}
	wantCode(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"F9"}, Remove: true}, CodeInvalidParams)
	wantCode(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"F9"}, Add: true, Remove: true}, CodeInvalidParams)

	// An invalid stored line is listed and can be dropped on its own.
	lines, _, _ := settings.Lookup[[]string](a.Settings(), keymap.SettingOverrides)
	if err := settings.Set(a.Settings(), keymap.SettingOverrides, append(lines, "app.find Cmd+Nope")); err != nil {
		t.Fatal(err)
	}
	call(t, a, "keymap.list", nil, &st)
	if !slices.Equal(st.Invalid, []string{"app.find Cmd+Nope"}) {
		t.Fatalf("invalid %q", st.Invalid)
	}
	var rr KeymapResetResult
	wantCode(t, a, "keymap.reset", KeymapResetParams{Invalid: "zzz"}, CodeNotFound)
	wantCode(t, a, "keymap.reset", KeymapResetParams{Invalid: "x", Action: "app.settings"}, CodeInvalidParams)
	call(t, a, "keymap.reset", KeymapResetParams{Invalid: "app.find Cmd+Nope"}, &rr)
	if rr.Preset != "mac" || !slices.Equal(rr.Reset, []string{"app.find Cmd+Nope"}) {
		t.Fatalf("drop invalid %+v", rr)
	}
	call(t, a, "keymap.list", nil, &st)
	if len(st.Invalid) != 0 {
		t.Fatalf("still invalid %q", st.Invalid)
	}
	if b, _ := binding(st, "navigate.definition"); b.Source != "user" {
		t.Fatal("dropping an invalid line touched other overrides")
	}
}
