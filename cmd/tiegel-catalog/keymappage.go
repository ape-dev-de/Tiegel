package main

import (
	"fmt"
	"slices"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/keymapeditor"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// keymapHost is a fake host for the keymap editor: it applies the events
// to its sample rows the way the app does through keymap.* (simplified:
// conflicts are chords shared in the same context or with a global one).
type keymapHost struct {
	e    keymapeditor.Editor
	last string
}

func sampleKeymapRows() []keymapeditor.Row {
	row := func(id, title, ctx string, chords ...string) keymapeditor.Row {
		return keymapeditor.Row{Action: id, Title: title, Context: ctx, Chords: chords, Defaults: slices.Clone(chords)}
	}
	rows := []keymapeditor.Row{
		row("navigate.back", "Back", "global", "Cmd+[", "Cmd+Alt+Left", "MouseBack", "SwipeBack"),
		row("navigate.forward", "Forward", "global", "Cmd+]", "Cmd+Alt+Right", "MouseForward", "SwipeForward"),
		row("navigate.searchEverywhere", "Search Everywhere", "global", "DoubleShift"),
		row("app.settings", "Settings…", "global", "Cmd+,"),
		row("edit.duplicateLines", "Duplicate Line or Selection", "editor", "Cmd+D"),
		row("edit.joinLines", "Join Lines", "editor", "Ctrl+Shift+J"),
		row("navigate.definition", "Go to Definition", "editor", "Cmd+B"),
		row("git.showDiff", "Show Diff", "toolWindow", "Cmd+D"),
		row("tab.close", "Close Tab", "global", "Cmd+W"),
	}
	// A changed action and a shadowed default.
	rows[3].Chords, rows[3].User = []string{"Cmd+,", "Cmd+["}, true
	rows[0].Shadowed = []string{"Cmd+["}
	rows[8].Chords, rows[8].User = nil, true
	return rows
}

func newKeymapHost() *keymapHost {
	h := &keymapHost{e: keymapeditor.Editor{Preset: "mac", Rows: sampleKeymapRows(), Invalid: []string{"app.find Hyper+F"}, Selected: "app.settings"}}
	h.e.Pending = &keymapeditor.Pending{Action: "app.settings", Chord: "Cmd+B",
		Others: []keymapeditor.Other{{Action: "navigate.definition", Title: "Go to Definition", Context: "editor"}}}
	return h
}

func (h *keymapHost) find(id string) *keymapeditor.Row {
	for i := range h.e.Rows {
		if h.e.Rows[i].Action == id {
			return &h.e.Rows[i]
		}
	}
	return nil
}

func overlaps(a, b string) bool { return a == b || a == "global" || b == "global" }

func (h *keymapHost) apply(ev keymapeditor.Event) {
	switch e := ev.(type) {
	case keymapeditor.PresetEvent:
		h.e.Preset = e.Preset
	case keymapeditor.RecordedEvent:
		r := h.find(e.Action)
		var others []keymapeditor.Other
		for _, o := range h.e.Rows {
			if o.Action != e.Action && overlaps(o.Context, r.Context) && slices.Contains(o.Chords, e.Chord) {
				others = append(others, keymapeditor.Other{Action: o.Action, Title: o.Title, Context: o.Context})
			}
		}
		if len(others) > 0 {
			h.e.Pending = &keymapeditor.Pending{Action: e.Action, Chord: e.Chord, Others: others}
		} else {
			h.add(e.Action, e.Chord, false)
		}
	case keymapeditor.AddEvent:
		h.add(e.Action, e.Chord, e.Exclusive)
		h.e.Pending = nil
	case keymapeditor.CancelPendingEvent:
		h.e.Pending = nil
	case keymapeditor.RemoveEvent:
		r := h.find(e.Action)
		r.Chords = slices.DeleteFunc(r.Chords, func(c string) bool { return c == e.Chord })
		r.User = !slices.Equal(r.Chords, r.Defaults)
	case keymapeditor.UnbindEvent:
		r := h.find(e.Action)
		r.Chords, r.User = nil, true
	case keymapeditor.ResetEvent:
		r := h.find(e.Action)
		r.Chords, r.User, r.Shadowed = slices.Clone(r.Defaults), false, nil
	case keymapeditor.ResetAllEvent:
		h.e.Rows, h.e.Invalid, h.e.Pending = sampleKeymapRows(), nil, nil
		for i := range h.e.Rows {
			r := &h.e.Rows[i]
			r.Chords, r.User, r.Shadowed = slices.Clone(r.Defaults), false, nil
		}
	case keymapeditor.DropInvalidEvent:
		h.e.Invalid = slices.DeleteFunc(h.e.Invalid, func(l string) bool { return l == e.Line })
	default:
		return
	}
	h.last = strings.TrimPrefix(fmt.Sprintf("%T %+v", ev, ev), "keymapeditor.")
}

func (h *keymapHost) add(action, chord string, exclusive bool) {
	r := h.find(action)
	if !slices.Contains(r.Chords, chord) {
		r.Chords = append(r.Chords, chord)
	}
	r.User = true
	if !exclusive {
		return
	}
	for i := range h.e.Rows {
		o := &h.e.Rows[i]
		if o.Action != action && overlaps(o.Context, r.Context) && slices.Contains(o.Chords, chord) {
			o.Chords = slices.DeleteFunc(o.Chords, func(c string) bool { return c == chord })
			o.User = true
		}
	}
}

func keymapEditorPage() *page {
	h := newKeymapHost()
	return &page{
		Level: "Organisms", Name: "KeymapEditor",
		Glyph:   atoms.GlyphSettings,
		Summary: "Keymap page of the settings dialog (AP13.3): preset, searchable table of all actions (by text or by recording a shortcut), user-changed rows marked \"custom\", shadowed chords marked; per action add (recorder: next chord incl. DoubleShift and mouse back/forward), remove, no shortcut, reset; inline conflict warning (remove from other / keep both); unreadable stored lines with Remove; Reset all. Double-click a row to record.",
		Variants: func(th *tokens.Theme) []variant {
			label := "Keymap editor"
			if h.last != "" {
				label += " – " + h.last
			}
			return []variant{{label, sized(560, func(gtx layout.Context) layout.Dimensions {
				for _, ev := range h.e.Update(gtx) {
					h.apply(ev)
				}
				return h.e.Layout(gtx, th)
			})}}
		},
	}
}
