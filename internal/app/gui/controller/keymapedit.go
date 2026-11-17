package controller

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// ---- keymap editor (AP13.3) ------------------------------------------------------
//
// Every change goes through the keymap.* API (D41); the controller only
// keeps the last keymap.list result and a recorded chord that waits for
// the user's decision because it conflicts (keymap.check).

// KeymapEdit is the state of the keymap editor.
type KeymapEdit struct {
	// List is the last keymap.list result (nil until loaded).
	List *api.KeymapState
	// Version changes with every new List.
	Version uint64
	// Pending is a recorded chord that conflicts with other actions.
	Pending *KeymapPending
}

// KeymapPending is a recorded chord awaiting "keep both" or "remove from
// the other actions".
type KeymapPending struct {
	Action    string
	Chord     string
	Conflicts []api.KeymapConflict
}

// fetchKeymapList calls keymap.list (background) and returns the apply
// step for the UI goroutine.
func (c *Controller) fetchKeymapList(ctx context.Context) func() {
	var st api.KeymapState
	err := c.call(ctx, "keymap.list", nil, &st)
	return func() {
		if err != nil {
			return
		}
		c.KeymapEdit.List = &st
		c.KeymapEdit.Version++
	}
}

// LoadKeymapList reloads the keymap editor's list.
func (c *Controller) LoadKeymapList() {
	c.Go(func(ctx context.Context) func() { return c.fetchKeymapList(ctx) })
}

// keymapCall runs a keymap.* method, reports an error and reloads the
// list (the bindings of the window follow via settings.changed).
func (c *Controller) keymapCall(method string, params any) {
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, method, params, nil)
		apply := c.fetchKeymapList(ctx)
		return func() {
			c.notice(err)
			apply()
		}
	})
}

// KeymapRecorded handles a chord recorded for action: free → it is added
// at once; used elsewhere → it becomes Pending.
func (c *Controller) KeymapRecorded(action, chord string) {
	c.Go(func(ctx context.Context) func() {
		var cs []api.KeymapConflict
		err := c.call(ctx, "keymap.check", api.KeymapCheckParams{Action: action, Chord: chord}, &cs)
		if err != nil || len(cs) > 0 {
			return func() {
				c.notice(err)
				if err == nil {
					c.KeymapEdit.Pending = &KeymapPending{Action: action, Chord: chord, Conflicts: cs}
				}
			}
		}
		err = c.call(ctx, "keymap.set", api.KeymapSetParams{Action: action, Chords: []string{chord}, Add: true}, nil)
		apply := c.fetchKeymapList(ctx)
		return func() {
			c.notice(err)
			apply()
		}
	})
}

// KeymapAdd adds chord to action; exclusive removes it from the actions
// it conflicts with. It clears Pending.
func (c *Controller) KeymapAdd(action, chord string, exclusive bool) {
	c.KeymapEdit.Pending = nil
	c.keymapCall("keymap.set", api.KeymapSetParams{Action: action, Chords: []string{chord}, Add: true, Exclusive: exclusive})
}

// KeymapCancelPending drops a pending conflicting chord.
func (c *Controller) KeymapCancelPending() { c.KeymapEdit.Pending = nil }

// KeymapRemove removes one chord of action.
func (c *Controller) KeymapRemove(action, chord string) {
	c.keymapCall("keymap.set", api.KeymapSetParams{Action: action, Chords: []string{chord}, Remove: true})
}

// KeymapUnbind leaves action without a shortcut.
func (c *Controller) KeymapUnbind(action string) {
	c.keymapCall("keymap.set", api.KeymapSetParams{Action: action})
}

// KeymapReset restores the defaults of action ("" = everything,
// including the preset choice).
func (c *Controller) KeymapReset(action string) {
	if action == "" {
		c.KeymapEdit.Pending = nil
	}
	c.keymapCall("keymap.reset", api.KeymapResetParams{Action: action})
}

// KeymapPreset switches the preset.
func (c *Controller) KeymapPreset(preset string) {
	c.keymapCall("keymap.set", api.KeymapSetParams{Preset: preset})
}

// KeymapDropInvalid removes an unparseable stored line.
func (c *Controller) KeymapDropInvalid(line string) {
	c.keymapCall("keymap.reset", api.KeymapResetParams{Invalid: line})
}
