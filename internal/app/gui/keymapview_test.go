package gui

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"gioui.org/io/key"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/keymapeditor"
	settingsui "github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
)

// keymapRow returns the editor row of action.
func keymapRow(e *env, action string) keymapeditor.Row {
	for _, r := range e.v.settings.Keymap.Rows {
		if r.Action == action {
			return r
		}
	}
	return keymapeditor.Row{}
}

// TestKeymapEditorPage drives the keymap page of the settings dialog
// against the real backend: recording (keys, gestures) without running
// window shortcuts, conflicts, and every change through keymap.*.
func TestKeymapEditorPage(t *testing.T) {
	e := newEnv(t, true)
	e.v.openSettings()
	e.waitFor("settings schema", func() bool { return e.ctl.Schema != nil && e.ctl.KeymapEdit.List != nil })
	var page *settingsui.Page
	for _, pg := range settingsPages(e.ctl) {
		if pg.Extra == settingsui.ExtraKeymap {
			page = &pg
		}
	}
	if page == nil || page.ID != "core:Keymap" || len(page.Fields) != 0 {
		t.Fatalf("keymap page %+v", page)
	}
	e.v.settings.Selected = page.ID
	e.settle()
	k := &e.v.settings.Keymap
	if len(k.Rows) < 50 {
		t.Fatalf("rows %d", len(k.Rows))
	}
	// A defined preset makes the chords independent of the test's OS.
	e.v.handleKeymapEvent(keymapeditor.PresetEvent{Preset: "mac"})
	e.waitFor("mac preset", func() bool { return k.Preset == "mac" })

	// Recording a used chord: the window shortcut (Go to File) does not
	// run, the editor asks; "remove from other" moves the chord.
	k.StartRecording("app.settings")
	e.frame()
	if !e.v.keymapRecording() {
		t.Fatal("not recording")
	}
	e.h.Router.Queue(key.Event{Name: "O", Modifiers: key.ModCommand | key.ModShift, State: key.Press})
	e.frame()
	e.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	e.frame()
	if e.v.chooseMode != chooseNone || !e.v.settingsOpen {
		t.Fatal("window shortcut ran while recording")
	}
	e.waitFor("conflict", func() bool { return k.Pending != nil })
	if p := k.Pending; p.Chord != "Cmd+Shift+O" || len(p.Others) != 1 || p.Others[0].Action != "file.quickOpen" || p.Others[0].Title != "Go to File…" {
		t.Fatalf("pending %+v", p)
	}
	e.v.handleKeymapEvent(keymapeditor.AddEvent{Action: "app.settings", Chord: "Cmd+Shift+O", Exclusive: true})
	e.waitFor("moved chord", func() bool {
		return slices.Contains(keymapRow(e, "app.settings").Chords, "Cmd+Shift+O") && !slices.Contains(keymapRow(e, "file.quickOpen").Chords, "Cmd+Shift+O")
	})
	if k.Pending != nil || !keymapRow(e, "app.settings").User {
		t.Fatal("pending kept or row not marked")
	}
	k.Selected = "app.settings"
	e.shot("settings-keymap")
	// The window follows (settings.changed).
	e.waitFor("window scheme", func() bool {
		return slices.Contains(e.v.Registry.Scheme().Chords("app.settings"), keymap.MustParse("Cmd+Shift+O"))
	})

	// A free chord is added at once; Escape cancels without closing the
	// dialog.
	k.StartRecording("edit.joinLines")
	e.frame()
	e.h.Router.Queue(key.Event{Name: "K", Modifiers: key.ModCommand | key.ModCtrl | key.ModAlt, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Press})
	e.frame()
	e.waitFor("free chord", func() bool { return slices.Contains(keymapRow(e, "edit.joinLines").Chords, "Cmd+Ctrl+Alt+K") })
	k.StartRecording("edit.joinLines")
	e.frame()
	e.h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	e.frame()
	if !e.v.settingsOpen || k.Recording() {
		t.Fatal("escape while recording")
	}

	// A gesture is recorded instead of navigating; keep both.
	k.StartRecording("navigate.forward")
	e.frame()
	if !e.v.Gesture(keymap.KeyMouseBack) {
		t.Fatal("gesture not taken")
	}
	e.frame()
	e.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	e.frame()
	e.waitFor("gesture conflict", func() bool { return k.Pending != nil && k.Pending.Chord == "MouseBack" })
	e.v.handleKeymapEvent(keymapeditor.CancelPendingEvent{})
	e.frame()
	if k.Pending != nil {
		t.Fatal("cancel pending")
	}
	e.v.handleKeymapEvent(keymapeditor.AddEvent{Action: "navigate.forward", Chord: "MouseBack"})
	e.waitFor("keep both", func() bool {
		return slices.Contains(keymapRow(e, "navigate.forward").Chords, "MouseBack") && slices.Contains(keymapRow(e, "navigate.back").Chords, "MouseBack")
	})

	// Remove one chord, no shortcut, reset one.
	e.v.handleKeymapEvent(keymapeditor.RemoveEvent{Action: "navigate.forward", Chord: "MouseBack"})
	e.waitFor("removed", func() bool {
		r := keymapRow(e, "navigate.forward")
		return !r.User && !slices.Contains(r.Chords, "MouseBack")
	})
	e.v.handleKeymapEvent(keymapeditor.UnbindEvent{Action: "tab.close"})
	e.waitFor("unbound", func() bool { r := keymapRow(e, "tab.close"); return r.User && len(r.Chords) == 0 })
	e.v.handleKeymapEvent(keymapeditor.ResetEvent{Action: "tab.close"})
	e.waitFor("reset one", func() bool { return !keymapRow(e, "tab.close").User })

	// An unreadable stored line is shown and can be removed.
	lines, _ := json.Marshal([]string{"app.settings Cmd+Nope", "edit.joinLines Cmd+Ctrl+Alt+K"})
	var sv api.SettingValue
	if err := e.ctl.Backend().Call(context.Background(), "settings.set", api.SettingsSetParams{Key: keymap.SettingOverrides, Value: lines}, &sv); err != nil {
		t.Fatal(err)
	}
	e.waitFor("invalid line", func() bool { return slices.Equal(k.Invalid, []string{"app.settings Cmd+Nope"}) })
	e.v.handleKeymapEvent(keymapeditor.DropInvalidEvent{Line: "app.settings Cmd+Nope"})
	e.waitFor("dropped", func() bool { return len(k.Invalid) == 0 })

	// Reset all: no user rows, preset back to the OS default.
	e.v.handleKeymapEvent(keymapeditor.ResetAllEvent{})
	e.waitFor("reset all", func() bool {
		for _, r := range k.Rows {
			if r.User {
				return false
			}
		}
		return true
	})
	if e.app.Settings().Has(keymap.SettingOverrides) || e.app.Settings().Has(keymap.SettingPreset) {
		t.Fatal("reset all left settings")
	}
	// Errors from the backend show up as a notice.
	e.v.handleKeymapEvent(keymapeditor.RemoveEvent{Action: "tab.close", Chord: "F19"})
	e.waitFor("error notice", func() bool { return e.ctl.Status.Notice != "" })

	// Leaving the page stops a recording; the window shortcuts work again.
	k.StartRecording("app.settings")
	e.v.settings.Selected = "core:Editor"
	e.frame()
	if e.v.keymapRecording() || k.Recording() {
		t.Fatal("recording outlived the page")
	}
}
