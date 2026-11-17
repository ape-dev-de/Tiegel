package api

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

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
	// Shadowed are chords of the action that another action of the same
	// context wins (e.g. a default chord the user bound elsewhere, D247).
	Shadowed []string `json:"shadowed,omitempty"`
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
// ones instead, Remove takes them out). Preset alone switches the preset.
type KeymapSetParams struct {
	Action string   `json:"action,omitempty"`
	Chords []string `json:"chords,omitempty"`
	Add    bool     `json:"add,omitempty"`
	// Remove removes Chords from the action's current chords (each must
	// be bound to it). If the rest equals the preset's defaults, the
	// override is dropped (source default again).
	Remove bool `json:"remove,omitempty"`
	// Exclusive removes the set or added chords from the other actions
	// whose contexts overlap (resolves the conflicts in the same step).
	Exclusive bool   `json:"exclusive,omitempty"`
	Preset    string `json:"preset,omitempty"`
}

// KeymapSetResult is the result of keymap.set.
type KeymapSetResult struct {
	Preset  string         `json:"preset"`
	Binding *KeymapBinding `json:"binding,omitempty"`
	// Conflicts are the conflicts of the changed action (bindings are
	// stored nevertheless; the client warns).
	Conflicts []KeymapConflict `json:"conflicts"`
	// RemovedFrom lists the actions that lost a chord (Exclusive).
	RemovedFrom []string `json:"removedFrom,omitempty"`
}

// KeymapCheckParams are the params of keymap.check.
type KeymapCheckParams struct {
	Action string `json:"action"`
	Chord  string `json:"chord"`
}

// KeymapResetParams are the params of keymap.reset: one action back to
// its defaults, or (no action) all overrides and the preset. Invalid
// removes one unparseable stored line instead (verbatim, as listed in
// KeymapState.Invalid).
type KeymapResetParams struct {
	Action  string `json:"action,omitempty"`
	Invalid string `json:"invalid,omitempty"`
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
	mustHandle(a, "keymap.reset", "Reset one action (or all overrides and the preset) to the defaults; invalid: drop one unparseable stored line", a.keymapReset)
	mustHandle(a, "keymap.check", "Conflicts that binding chord to action would cause (nothing is stored)", a.keymapCheck)
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

func toBinding(s *keymap.Scheme, e keymap.Entry) KeymapBinding {
	b := KeymapBinding{Action: string(e.ID), Title: e.Title, Context: string(e.Context),
		Chords: chordStrings(e.Chords), Defaults: chordStrings(e.Defaults), Source: string(e.Source)}
	if sh := s.Shadowed(e.ID); len(sh) > 0 {
		b.Shadowed = chordStrings(sh)
	}
	return b
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
		st.Bindings = append(st.Bindings, toBinding(s, e))
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
		st.Bindings = append(st.Bindings, toBinding(s, e))
	}
	return st, nil
}

// keymapMu serialises the read-modify-write of the keymap settings by
// keymap.set and keymap.reset: API calls run concurrently (the GUI calls
// them from goroutines) and would otherwise lose each other's changes.
// It is package wide because several Apps may share one global store.
var keymapMu sync.Mutex

func (a *App) keymapSet(_ context.Context, p KeymapSetParams) (KeymapSetResult, error) {
	keymapMu.Lock()
	res, st, err := a.keymapSetLocked(p)
	keymapMu.Unlock()
	if err != nil {
		return KeymapSetResult{}, err
	}
	// Published outside the lock: subscribers may call keymap.* again.
	a.bus.Publish(TopicKeymapChanged, st)
	return res, nil
}

// keymapSetLocked performs keymap.set with keymapMu held and returns the
// result and the new state to publish.
func (a *App) keymapSetLocked(p KeymapSetParams) (KeymapSetResult, KeymapState, error) {
	s, ks := a.keymapScheme()
	if p.Preset != "" {
		if !keymap.Preset(p.Preset).Valid() {
			return KeymapSetResult{}, KeymapState{}, Errorf(CodeInvalidParams, "unknown preset %q (mac, pc)", p.Preset)
		}
		// An explicit choice is stored; otherwise the preset keeps
		// following the OS (D248).
		ks.Preset, ks.PresetSet = keymap.Preset(p.Preset), true
	}
	if p.Action == "" && p.Preset == "" {
		return KeymapSetResult{}, KeymapState{}, Errorf(CodeInvalidParams, "action or preset is required")
	}
	if p.Add && p.Remove {
		return KeymapSetResult{}, KeymapState{}, Errorf(CodeInvalidParams, "add and remove exclude each other")
	}
	id := keymap.Action(p.Action)
	var removedFrom []string
	if p.Action != "" {
		if !s.Has(id) {
			return KeymapSetResult{}, KeymapState{}, Errorf(CodeNotFound, "unknown action %q (see keymap.list)", p.Action)
		}
		given := make([]keymap.Chord, 0, len(p.Chords))
		for _, str := range p.Chords {
			c, err := keymap.ParseChord(str)
			if err != nil {
				return KeymapSetResult{}, KeymapState{}, Errorf(CodeInvalidParams, "%w", err)
			}
			given = append(given, c)
		}
		cs, err := keymapChords(s, id, given, p.Add, p.Remove)
		if err != nil {
			return KeymapSetResult{}, KeymapState{}, err
		}
		ks.Drop(id) // also replaces an unparseable stored line of id
		ks.Overrides = ks.Overrides.Clone()
		if e, _ := s.Entry(id); p.Remove && slices.Equal(cs, e.Defaults) {
			delete(ks.Overrides, id) // back to the defaults
		} else {
			ks.Overrides[id] = cs
		}
		if p.Exclusive && !p.Remove {
			removedFrom = keymapSteal(s, ks.Overrides, id, given)
		}
	}
	if err := keymap.SaveState(a.global, ks); err != nil {
		return KeymapSetResult{}, KeymapState{}, Errorf(CodeFailed, "%w", err)
	}
	ns := keymap.NewScheme(ks.Preset, ks.Overrides)
	res := KeymapSetResult{Preset: string(ks.Preset), Conflicts: []KeymapConflict{}, RemovedFrom: removedFrom}
	if p.Action != "" {
		e, _ := ns.Entry(id)
		b := toBinding(ns, e)
		res.Binding = &b
		res.Conflicts = toConflicts(ns.ConflictsOf(id))
	}
	return res, a.keymapState(ns, ks), nil
}

// keymapChords returns the new chords of id: given (set), the current
// ones plus given (add) or without given (remove; each must be bound).
func keymapChords(s *keymap.Scheme, id keymap.Action, given []keymap.Chord, add, remove bool) ([]keymap.Chord, error) {
	var cs []keymap.Chord
	if add || remove {
		cs = s.Chords(id)
	}
	if remove {
		for _, c := range given {
			i := slices.Index(cs, c)
			if i < 0 {
				return nil, Errorf(CodeInvalidParams, "%s is not bound to %s", c, id)
			}
			cs = slices.Delete(cs, i, i+1)
		}
		return cs, nil
	}
	for _, c := range given {
		if !slices.Contains(cs, c) {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

// keymapSteal removes chords from every other action of s that would
// conflict with id (updating ov) and returns those actions, sorted.
func keymapSteal(s *keymap.Scheme, ov keymap.Overrides, id keymap.Action, chords []keymap.Chord) []string {
	var out []string
	for _, c := range chords {
		for _, k := range s.ConflictsWith(id, c) {
			cur, ok := ov[k.B]
			if !ok {
				cur = s.Chords(k.B)
			}
			ov[k.B] = slices.DeleteFunc(slices.Clone(cur), func(x keymap.Chord) bool { return x == c })
			if !slices.Contains(out, string(k.B)) {
				out = append(out, string(k.B))
			}
		}
	}
	slices.Sort(out)
	return out
}

func (a *App) keymapCheck(_ context.Context, p KeymapCheckParams) ([]KeymapConflict, error) {
	c, err := keymap.ParseChord(p.Chord)
	if err != nil {
		return nil, Errorf(CodeInvalidParams, "%w", err)
	}
	s, _ := a.keymapScheme()
	id := keymap.Action(p.Action)
	if !s.Has(id) {
		return nil, Errorf(CodeNotFound, "unknown action %q (see keymap.list)", p.Action)
	}
	return toConflicts(s.ConflictsWith(id, c)), nil
}

func (a *App) keymapReset(_ context.Context, p KeymapResetParams) (KeymapResetResult, error) {
	keymapMu.Lock()
	res, st, err := a.keymapResetLocked(p)
	keymapMu.Unlock()
	if err != nil {
		return KeymapResetResult{}, err
	}
	a.bus.Publish(TopicKeymapChanged, st)
	return res, nil
}

// keymapResetLocked performs keymap.reset with keymapMu held and returns
// the result and the new state to publish.
func (a *App) keymapResetLocked(p KeymapResetParams) (KeymapResetResult, KeymapState, error) {
	s, ks := a.keymapScheme()
	res := KeymapResetResult{Reset: []string{}}
	if p.Invalid != "" {
		if p.Action != "" {
			return KeymapResetResult{}, KeymapState{}, Errorf(CodeInvalidParams, "action and invalid exclude each other")
		}
		if !ks.DropInvalid(p.Invalid) {
			return KeymapResetResult{}, KeymapState{}, Errorf(CodeNotFound, "no invalid stored line %q (see keymap.list)", p.Invalid)
		}
		res.Reset = append(res.Reset, p.Invalid)
	} else if p.Action != "" {
		id := keymap.Action(p.Action)
		if !s.Has(id) {
			return KeymapResetResult{}, KeymapState{}, Errorf(CodeNotFound, "unknown action %q (see keymap.list)", p.Action)
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
		return KeymapResetResult{}, KeymapState{}, Errorf(CodeFailed, "%w", err)
	}
	ns, nks := a.keymapScheme()
	res.Preset = string(ns.Preset())
	return res, a.keymapState(ns, nks), nil
}

func (a *App) keymapConflicts(context.Context, struct{}) ([]KeymapConflict, error) {
	s, _ := a.keymapScheme()
	return toConflicts(s.Conflicts()), nil
}
