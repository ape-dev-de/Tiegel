package gui

import (
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/keymapeditor"
)

// ---- keymap editor page of the settings dialog (AP13.3) ---------------------------

// keymapRecording reports whether the keymap editor records a shortcut:
// the window then runs none of its own shortcuts, Escape handling or
// gestures, so every key reaches the recorder.
func (v *View) keymapRecording() bool {
	return v.settingsOpen && v.settings.Recording()
}

// fillKeymapEditor copies the controller's keymap.list result and the
// pending conflict into the organism (rows only when the list changed).
func (v *View) fillKeymapEditor() {
	k := &v.settings.Keymap
	ed := v.ctl.KeymapEdit
	if ed.List != nil && ed.Version != v.keymapSeen {
		v.keymapSeen = ed.Version
		k.Preset, k.Invalid = ed.List.Preset, ed.List.Invalid
		k.Rows = k.Rows[:0]
		for _, b := range ed.List.Bindings {
			k.Rows = append(k.Rows, keymapeditor.Row{Action: b.Action, Title: b.Title, Context: b.Context,
				Chords: b.Chords, Defaults: b.Defaults, Shadowed: b.Shadowed, User: b.Source == "user"})
		}
	}
	k.Pending = nil
	if p := ed.Pending; p != nil {
		kp := &keymapeditor.Pending{Action: p.Action, Chord: p.Chord}
		for _, c := range p.Conflicts {
			o := keymapeditor.Other{Action: c.Other, Title: c.Other, Context: c.OtherCtx}
			for _, r := range k.Rows {
				if r.Action == c.Other {
					o.Title = r.Title
				}
			}
			kp.Others = append(kp.Others, o)
		}
		k.Pending = kp
	}
}

// handleKeymapEvent turns an event of the keymap editor into a keymap.*
// call (through the controller).
func (v *View) handleKeymapEvent(e keymapeditor.Event) {
	c := v.ctl
	switch e := e.(type) {
	case keymapeditor.PresetEvent:
		c.KeymapPreset(e.Preset)
	case keymapeditor.RecordedEvent:
		c.KeymapRecorded(e.Action, e.Chord)
	case keymapeditor.AddEvent:
		c.KeymapAdd(e.Action, e.Chord, e.Exclusive)
	case keymapeditor.CancelPendingEvent:
		c.KeymapCancelPending()
	case keymapeditor.RemoveEvent:
		c.KeymapRemove(e.Action, e.Chord)
	case keymapeditor.UnbindEvent:
		c.KeymapUnbind(e.Action)
	case keymapeditor.ResetEvent:
		c.KeymapReset(e.Action)
	case keymapeditor.ResetAllEvent:
		c.KeymapReset("")
	case keymapeditor.DropInvalidEvent:
		c.KeymapDropInvalid(e.Line)
	}
}
