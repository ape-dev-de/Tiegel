package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

func binding(st KeymapState, id string) (KeymapBinding, bool) {
	for _, b := range st.Bindings {
		if b.Action == id {
			return b, true
		}
	}
	return KeymapBinding{}, false
}

func TestKeymapListSetReset(t *testing.T) {
	a := newTestApp(t) // GOOS linux: pc preset
	var st KeymapState
	call(t, a, "keymap.list", nil, &st)
	back, ok := binding(st, "navigate.back")
	if st.Preset != "pc" || !ok || back.Source != "default" || back.Chords[0] != "Ctrl+Alt+Left" || back.Context != "global" {
		t.Fatalf("list: preset %s back %+v", st.Preset, back)
	}
	call(t, a, "keymap.list", KeymapListParams{Context: "editor", Query: "join"}, &st)
	if len(st.Bindings) != 1 || st.Bindings[0].Action != "edit.joinLines" {
		t.Fatalf("filtered: %+v", st.Bindings)
	}
	call(t, a, "keymap.list", KeymapListParams{Chord: "ctrl+d"}, &st)
	if len(st.Bindings) != 2 {
		t.Fatalf("Ctrl+D: %+v", st.Bindings)
	}
	wantCode(t, a, "keymap.list", KeymapListParams{Context: "nowhere"}, CodeInvalidParams)
	wantCode(t, a, "keymap.list", KeymapListParams{Chord: "Hyper+X"}, CodeInvalidParams)

	events := make(chan any, 4)
	unsub := a.Bus().Subscribe(TopicKeymapChanged, func(e bus.Event) { events <- e.Payload })
	defer unsub()

	// Switch to the mac preset and bind Cmd+B globally to Settings, which
	// conflicts with Go to Definition in the editor.
	var res KeymapSetResult
	call(t, a, "keymap.set", KeymapSetParams{Preset: "mac"}, &res)
	call(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"cmd+b"}}, &res)
	if res.Preset != "mac" || res.Binding == nil || res.Binding.Source != "user" || res.Binding.Chords[0] != "Cmd+B" ||
		len(res.Conflicts) != 1 || res.Conflicts[0].Other != "navigate.definition" {
		t.Fatalf("set: %+v", res)
	}
	call(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"F12"}, Add: true}, &res)
	if len(res.Binding.Chords) != 2 {
		t.Fatalf("add: %+v", res.Binding)
	}
	call(t, a, "keymap.set", KeymapSetParams{Action: "navigate.back", Chords: []string{"Cmd+Alt+Left"}, Add: true}, &res)
	if len(res.Binding.Chords) != 4 { // the mac defaults already contain it
		t.Fatalf("add existing: %+v", res.Binding)
	}
	var cs []KeymapConflict
	call(t, a, "keymap.conflicts", nil, &cs)
	if len(cs) != 1 || cs[0].Chord != "Cmd+B" {
		t.Fatalf("conflicts %+v", cs)
	}
	call(t, a, "keymap.list", KeymapListParams{User: true}, &st)
	if len(st.Bindings) != 2 {
		t.Fatalf("user bindings: %+v", st.Bindings)
	}
	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("no keymap.changed event")
	}
	wantCode(t, a, "keymap.set", KeymapSetParams{}, CodeInvalidParams)
	wantCode(t, a, "keymap.set", KeymapSetParams{Preset: "emacs"}, CodeInvalidParams)
	wantCode(t, a, "keymap.set", KeymapSetParams{Action: "nope", Chords: []string{"F1"}}, CodeNotFound)
	wantCode(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"Cmd+Nope"}}, CodeInvalidParams)

	// The overrides survive a restart: a new App on the same config dir.
	b, err := New(Options{ConfigDir: a.ConfigDir(), GOOS: "linux", Interp: interpOptions(nil)})
	if err != nil {
		t.Fatal(err)
	}
	var st2 KeymapState
	call(t, b, "keymap.list", KeymapListParams{User: true}, &st2)
	_ = b.Close()
	if st2.Preset != "mac" || len(st2.Bindings) != 2 {
		t.Fatalf("after restart: %+v", st2)
	}

	// Reset one action keeps the preset, reset all restores the OS one.
	var rr KeymapResetResult
	call(t, a, "keymap.reset", KeymapResetParams{Action: "app.settings"}, &rr)
	if rr.Preset != "mac" || len(rr.Reset) != 1 {
		t.Fatalf("reset one: %+v", rr)
	}
	call(t, a, "keymap.reset", KeymapResetParams{Action: "app.settings"}, &rr)
	if len(rr.Reset) != 0 {
		t.Fatalf("reset again: %+v", rr)
	}
	wantCode(t, a, "keymap.reset", KeymapResetParams{Action: "nope"}, CodeNotFound)
	call(t, a, "keymap.reset", nil, &rr)
	if rr.Preset != "pc" || len(rr.Reset) != 1 || rr.Reset[0] != "navigate.back" {
		t.Fatalf("reset all: %+v", rr)
	}
	if a.Settings().Has(keymap.SettingOverrides) || a.Settings().Has(keymap.SettingPreset) {
		t.Fatal("reset all should remove the settings")
	}
	// Resetting one action without a stored preset keeps the OS preset.
	call(t, a, "keymap.set", KeymapSetParams{Action: "tab.close", Chords: []string{}}, &res)
	if len(res.Binding.Chords) != 0 {
		t.Fatalf("unbind: %+v", res.Binding)
	}
	_ = a.Settings().Delete(keymap.SettingPreset)
	call(t, a, "keymap.reset", KeymapResetParams{Action: "tab.close"}, &rr)
	if a.Settings().Has(keymap.SettingPreset) || rr.Preset != "pc" {
		t.Fatal("reset one without stored preset")
	}
}

func TestEditJoinLinesToggleCase(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.php"), "<?php\n$x = foo(\n    1,\n    2\n);\necho 'hello';\n")
	call(t, a, "project.open", map[string]string{"path": root}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)

	var res EditRangeResult
	call(t, a, "edit.joinLines", EditRangeParams{Path: "a.php", Start: Pos{Line: 2}, End: &Pos{Line: 5, Col: 2}}, &res)
	if !res.Changed {
		t.Fatalf("join: %+v", res)
	}
	var dt DocText
	call(t, a, "doc.text", DocTextParams{Path: "a.php"}, &dt)
	if dt.Text != "<?php\n$x = foo(1, 2);\necho 'hello';\n" {
		t.Fatalf("joined: %q", dt.Text)
	}
	call(t, a, "edit.toggleCase", EditRangeParams{Path: "a.php", Start: Pos{Line: 3, Col: 7}, End: &Pos{Line: 3, Col: 12}}, &res)
	call(t, a, "doc.text", DocTextParams{Path: "a.php"}, &dt)
	if !res.Changed || dt.Text != "<?php\n$x = foo(1, 2);\necho 'HELLO';\n" || res.SelEnd-res.SelStart != 5 {
		t.Fatalf("toggled: %q %+v", dt.Text, res)
	}
	// One undo step each.
	call(t, a, "doc.undo", PathParams{Path: "a.php"}, nil)
	call(t, a, "doc.undo", PathParams{Path: "a.php"}, nil)
	call(t, a, "doc.text", DocTextParams{Path: "a.php"}, &dt)
	if dt.Text != "<?php\n$x = foo(\n    1,\n    2\n);\necho 'hello';\n" {
		t.Fatalf("undo: %q", dt.Text)
	}
	// Caret on the last line: nothing to join.
	call(t, a, "edit.joinLines", EditRangeParams{Path: "a.php", Start: Pos{Line: 7}}, &res)
	if res.Changed {
		t.Fatal("join on the last line")
	}
	wantCode(t, a, "edit.toggleCase", EditRangeParams{Path: "a.php", Start: Pos{Line: 99}}, CodeInvalidParams)
	wantCode(t, a, "edit.toggleCase", EditRangeParams{Path: "a.php", Start: Pos{Line: 1}, End: &Pos{Offset: -1}}, CodeInvalidParams)
}
