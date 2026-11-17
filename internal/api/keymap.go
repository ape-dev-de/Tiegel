package api

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// Keymap ("keymap.*", M13): the catalog of bindable actions resolved with
// the preset and the user overrides (settings keymap.preset and
// keymap.overrides, global). Clients such as the GUI resolve the same
// settings with keymap.NewScheme, so a change through these methods
// applies to open windows without restart (settings.changed and
// keymap.changed events).

// TopicKeymapChanged is published after keymap.set and keymap.reset with
// the new KeymapState.
const TopicKeymapChanged = "keymap.changed"

// KeymapBinding is one action of the keymap.
type KeymapBinding struct {
	Action  string `json:"action"`
	Title   string `json:"title"`
	Context string `json:"context"`
	// Chords are the effective chords; Defaults those of the preset.
	Chords   []string `json:"chords"`
	Defaults []string `json:"defaults"`
	// Source is "default" or "user" (overridden).
	Source string `json:"source"`
}

// KeymapConflict is a chord bound to two actions with overlapping
// contexts.
type KeymapConflict struct {
	Chord    string `json:"chord"`
	Action   string `json:"action"`
	Context  string `json:"context"`
	Other    string `json:"other"`
	OtherCtx string `json:"otherContext"`
}

// KeymapState is the result of keymap.list and the payload of
// keymap.changed.
type KeymapState struct {
	Preset   string          `json:"preset"`
	Bindings []KeymapBinding `json:"bindings"`
	// Invalid are stored override lines that cannot be parsed: they are
	// ignored, but kept in the settings until the action is set or reset
	// (D248).
	Invalid []string `json:"invalid,omitempty"`
}

// KeymapListParams filter keymap.list.
type KeymapListParams struct {
	// Context limits the list to one context (global, editor,
	// toolWindow).
	Context string `json:"context,omitempty"`
	// Query matches action IDs and titles (case-insensitive substring).
	Query string `json:"query,omitempty"`
	// Chord lists the actions bound to this chord.
	Chord string `json:"chord,omitempty"`
	// User lists only overridden actions.
	User bool `json:"user,omitempty"`
}

// KeymapSetParams are the params of keymap.set: Chords replace the
// action's chords (an empty list unbinds it; Add appends to the current
// ones instead). Preset alone switches the preset.
type KeymapSetParams struct {
	Action string   `json:"action,omitempty"`
	Chords []string `json:"chords,omitempty"`
	Add    bool     `json:"add,omitempty"`
	Preset string   `json:"preset,omitempty"`
}

// KeymapSetResult is the result of keymap.set.
type KeymapSetResult struct {
	Preset  string         `json:"preset"`
	Binding *KeymapBinding `json:"binding,omitempty"`
	// Conflicts are the conflicts of the changed action (bindings are
	// stored nevertheless; the client warns).
	Conflicts []KeymapConflict `json:"conflicts"`
}

// KeymapResetParams are the params of keymap.reset: one action back to
// its defaults, or (no action) all overrides and the preset.
type KeymapResetParams struct {
	Action string `json:"action,omitempty"`
}

// KeymapResetResult is the result of keymap.reset.
type KeymapResetResult struct {
	Preset string `json:"preset"`
	// Reset lists the actions whose overrides were removed.
	Reset []string `json:"reset"`
}

func (a *App) registerKeymap() {
	mustHandle(a, "keymap.list", "Keymap: actions with chords, context and source default/user (filters context, query, chord, user)", a.keymapList)
	mustHandle(a, "keymap.set", "Bind chords to an action (replace, or add:true; [] unbinds) or switch the preset (mac, pc)", a.keymapSet)
	mustHandle(a, "keymap.reset", "Reset one action (or all overrides and the preset) to the defaults", a.keymapReset)
	mustHandle(a, "keymap.conflicts", "Chords bound to two actions in overlapping contexts", a.keymapConflicts)
}

// keymapScheme resolves the stored settings. Broken stored lines are
// skipped (the others apply), as the GUI does; they stay in State.Invalid.
func (a *App) keymapScheme() (*keymap.Scheme, keymap.State) {
	st, err := keymap.LoadState(a.global, a.opts.GOOS)
	if err != nil {
		a.log.Warn("keymap settings", "err", err)
	}
	return keymap.NewScheme(st.Preset, st.Overrides), st
}

func chordStrings(cs []keymap.Chord) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

func toBinding(e keymap.Entry) KeymapBinding {
	return KeymapBinding{Action: string(e.ID), Title: e.Title, Context: string(e.Context),
		Chords: chordStrings(e.Chords), Defaults: chordStrings(e.Defaults), Source: string(e.Source)}
}

func toConflicts(cs []keymap.Conflict) []KeymapConflict {
	out := make([]KeymapConflict, len(cs))
	for i, c := range cs {
		out[i] = KeymapConflict{Chord: c.Chord.String(), Action: string(c.A), Context: string(c.CtxA), Other: string(c.B), OtherCtx: string(c.CtxB)}
	}
	return out
}

func (a *App) keymapState(s *keymap.Scheme, ks keymap.State) KeymapState {
	st := KeymapState{Preset: string(s.Preset()), Bindings: []KeymapBinding{}, Invalid: ks.Invalid}
	for _, e := range s.Entries() {
		st.Bindings = append(st.Bindings, toBinding(e))
	}
	return st
}

func (a *App) keymapList(_ context.Context, p KeymapListParams) (KeymapState, error) {
	if p.Context != "" && !keymap.Context(p.Context).Valid() {
		return KeymapState{}, Errorf(CodeInvalidParams, "unknown context %q (global, editor, toolWindow)", p.Context)
	}
	var chord *keymap.Chord
	if p.Chord != "" {
		c, err := keymap.ParseChord(p.Chord)
		if err != nil {
			return KeymapState{}, Errorf(CodeInvalidParams, "%w", err)
		}
		chord = &c
	}
	s, ks := a.keymapScheme()
	q := strings.ToLower(p.Query)
	st := KeymapState{Preset: string(s.Preset()), Bindings: []KeymapBinding{}, Invalid: ks.Invalid}
	for _, e := range s.Entries() {
		switch {
		case p.Context != "" && string(e.Context) != p.Context,
			p.User && e.Source != keymap.SourceUser,
			chord != nil && !slices.Contains(e.Chords, *chord),
			q != "" && !strings.Contains(strings.ToLower(string(e.ID)), q) && !strings.Contains(strings.ToLower(e.Title), q):
			continue
		}
		st.Bindings = append(st.Bindings, toBinding(e))
	}
	return st, nil
}

func (a *App) keymapSet(_ context.Context, p KeymapSetParams) (KeymapSetResult, error) {
	s, ks := a.keymapScheme()
	if p.Preset != "" {
		if !keymap.Preset(p.Preset).Valid() {
			return KeymapSetResult{}, Errorf(CodeInvalidParams, "unknown preset %q (mac, pc)", p.Preset)
		}
		// An explicit choice is stored; otherwise the preset keeps
		// following the OS (D248).
		ks.Preset, ks.PresetSet = keymap.Preset(p.Preset), true
	}
	if p.Action == "" && p.Preset == "" {
		return KeymapSetResult{}, Errorf(CodeInvalidParams, "action or preset is required")
	}
	id := keymap.Action(p.Action)
	if p.Action != "" {
		if !s.Has(id) {
			return KeymapSetResult{}, Errorf(CodeNotFound, "unknown action %q (see keymap.list)", p.Action)
		}
		var cs []keymap.Chord
		if p.Add {
			if cur, ok := ks.Overrides[id]; ok {
				cs = cur
			} else {
				cs = s.Chords(id)
			}
		}
		for _, str := range p.Chords {
			c, err := keymap.ParseChord(str)
			if err != nil {
				return KeymapSetResult{}, Errorf(CodeInvalidParams, "%w", err)
			}
			if !slices.Contains(cs, c) {
				cs = append(cs, c)
			}
		}
		ks.Drop(id) // also replaces an unparseable stored line of id
		ks.Overrides = ks.Overrides.Clone()
		ks.Overrides[id] = cs
	}
	if err := keymap.SaveState(a.global, ks); err != nil {
		return KeymapSetResult{}, Errorf(CodeFailed, "%w", err)
	}
	ns := keymap.NewScheme(ks.Preset, ks.Overrides)
	res := KeymapSetResult{Preset: string(ks.Preset), Conflicts: []KeymapConflict{}}
	if p.Action != "" {
		e, _ := ns.Entry(id)
		b := toBinding(e)
		res.Binding = &b
		res.Conflicts = toConflicts(ns.ConflictsOf(id))
	}
	a.bus.Publish(TopicKeymapChanged, a.keymapState(ns, ks))
	return res, nil
}

func (a *App) keymapReset(_ context.Context, p KeymapResetParams) (KeymapResetResult, error) {
	s, ks := a.keymapScheme()
	res := KeymapResetResult{Reset: []string{}}
	if p.Action != "" {
		id := keymap.Action(p.Action)
		if !s.Has(id) {
			return KeymapResetResult{}, Errorf(CodeNotFound, "unknown action %q (see keymap.list)", p.Action)
		}
		if _, ok := ks.Overrides[id]; ok {
			res.Reset = append(res.Reset, p.Action)
		}
		ks.Drop(id) // an explicitly chosen preset is kept
	} else {
		for _, id := range slices.Sorted(maps.Keys(ks.Overrides)) {
			res.Reset = append(res.Reset, string(id))
		}
		ks = keymap.State{}
	}
	if err := keymap.SaveState(a.global, ks); err != nil {
		return KeymapResetResult{}, Errorf(CodeFailed, "%w", err)
	}
	ns, nks := a.keymapScheme()
	res.Preset = string(ns.Preset())
	a.bus.Publish(TopicKeymapChanged, a.keymapState(ns, nks))
	return res, nil
}

func (a *App) keymapConflicts(context.Context, struct{}) ([]KeymapConflict, error) {
	s, _ := a.keymapScheme()
	return toConflicts(s.Conflicts()), nil
}
