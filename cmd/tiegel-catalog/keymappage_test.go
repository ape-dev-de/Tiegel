package main

import (
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/keymapeditor"
)

func TestKeymapHostAppliesEvents(t *testing.T) {
	h := newKeymapHost()
	h.apply(keymapeditor.CancelPendingEvent{})
	if h.e.Pending != nil {
		t.Fatal("cancel pending")
	}
	// A free chord is added at once, a used one asks.
	h.apply(keymapeditor.RecordedEvent{Action: "edit.joinLines", Chord: "F12"})
	if r := h.find("edit.joinLines"); !r.User || !slices.Contains(r.Chords, "F12") {
		t.Fatalf("free chord %+v", r)
	}
	h.apply(keymapeditor.RecordedEvent{Action: "app.settings", Chord: "Cmd+D"})
	if p := h.e.Pending; p == nil || len(p.Others) != 2 {
		t.Fatalf("pending %+v", p)
	}
	h.apply(keymapeditor.AddEvent{Action: "app.settings", Chord: "Cmd+D", Exclusive: true})
	if slices.Contains(h.find("edit.duplicateLines").Chords, "Cmd+D") || h.e.Pending != nil {
		t.Fatal("exclusive add")
	}
	h.apply(keymapeditor.RemoveEvent{Action: "app.settings", Chord: "Cmd+D"})
	h.apply(keymapeditor.UnbindEvent{Action: "edit.joinLines"})
	h.apply(keymapeditor.ResetEvent{Action: "navigate.back"})
	h.apply(keymapeditor.PresetEvent{Preset: "pc"})
	h.apply(keymapeditor.DropInvalidEvent{Line: "app.find Hyper+F"})
	if len(h.find("edit.joinLines").Chords) != 0 || h.find("navigate.back").Shadowed != nil || h.e.Preset != "pc" || len(h.e.Invalid) != 0 {
		t.Fatalf("events: %+v", h.e)
	}
	h.apply(keymapeditor.ResetAllEvent{})
	for _, r := range h.e.Rows {
		if r.User {
			t.Fatalf("reset all left %s", r.Action)
		}
	}
	if h.last == "" {
		t.Fatal("last event label")
	}
}
