package api

import (
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// TestKeymapBadOverrideLineKeepsOthers checks that one unparseable stored
// override line neither disables the valid ones nor gets them erased by
// the next keymap.set; the bad line stays until its action is set or
// reset (D248).
func TestKeymapBadOverrideLineKeepsOthers(t *testing.T) {
	a := newTestApp(t)
	if err := settings.Set(a.Settings(), keymap.SettingOverrides, []string{"navigate.back F5", "app.settings Cmd+Nope", "tab.close"}); err != nil {
		t.Fatal(err)
	}
	var st KeymapState
	call(t, a, "keymap.list", KeymapListParams{User: true}, &st)
	if len(st.Bindings) != 2 || len(st.Invalid) != 1 || st.Invalid[0] != "app.settings Cmd+Nope" {
		t.Fatalf("list: %+v", st)
	}
	if b, _ := binding(st, "navigate.back"); len(b.Chords) != 1 || b.Chords[0] != "F5" {
		t.Fatalf("back %+v", b)
	}
	var res KeymapSetResult
	call(t, a, "keymap.set", KeymapSetParams{Action: "navigate.forward", Chords: []string{"F6"}}, &res)
	lines, _, _ := settings.Lookup[[]string](a.Settings(), keymap.SettingOverrides)
	want := []string{"navigate.back F5", "navigate.forward F6", "tab.close", "app.settings Cmd+Nope"}
	if !slices.Equal(lines, want) {
		t.Fatalf("stored %q", lines)
	}
	// Setting the broken action replaces its line.
	call(t, a, "keymap.set", KeymapSetParams{Action: "app.settings", Chords: []string{"F7"}}, &res)
	lines, _, _ = settings.Lookup[[]string](a.Settings(), keymap.SettingOverrides)
	if slices.Contains(lines, "app.settings Cmd+Nope") || !slices.Contains(lines, "app.settings F7") || len(lines) != 4 {
		t.Fatalf("replaced %q", lines)
	}
	// Reset of an action also drops its invalid line.
	if err := settings.Set(a.Settings(), keymap.SettingOverrides, []string{"navigate.back F5", "app.settings Cmd+Nope"}); err != nil {
		t.Fatal(err)
	}
	var rr KeymapResetResult
	call(t, a, "keymap.reset", KeymapResetParams{Action: "app.settings"}, &rr)
	lines, _, _ = settings.Lookup[[]string](a.Settings(), keymap.SettingOverrides)
	if !slices.Equal(lines, []string{"navigate.back F5"}) {
		t.Fatalf("after reset %q", lines)
	}
}

// TestKeymapSetKeepsFollowingOSPreset checks that keymap.set does not
// store the OS default preset when none was chosen explicitly.
func TestKeymapSetKeepsFollowingOSPreset(t *testing.T) {
	a := newTestApp(t)
	var res KeymapSetResult
	call(t, a, "keymap.set", KeymapSetParams{Action: "navigate.back", Chords: []string{"F5"}}, &res)
	if res.Preset != "pc" || a.Settings().Has(keymap.SettingPreset) {
		t.Fatalf("preset stored: %+v", res)
	}
	call(t, a, "keymap.set", KeymapSetParams{Preset: "mac"}, &res)
	call(t, a, "keymap.set", KeymapSetParams{Action: "navigate.back", Chords: []string{"F6"}}, &res)
	if p, _, _ := settings.Lookup[string](a.Settings(), keymap.SettingPreset); p != "mac" || res.Preset != "mac" {
		t.Fatalf("explicit preset lost: %q %+v", p, res)
	}
}
