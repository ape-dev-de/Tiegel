package keymap

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Source tells where the chords of an entry come from.
type Source string

// Sources.
const (
	SourceDefault Source = "default"
	SourceUser    Source = "user"
)

// Overrides are user bindings: they replace all default chords of an
// action. An empty chord list unbinds the action.
type Overrides map[Action][]Chord

// ParseOverrides reads the stored form of overrides: one entry per
// action, "<action> <chord> <chord>…" (chords in ParseChord syntax,
// separated by spaces; an action alone means unbound). Later entries for
// the same action replace earlier ones.
//
// A line with an unparseable chord is skipped; the overrides of all other
// lines are returned nevertheless, together with an error that names the
// bad lines (errors.Join of one error per line, each wrapping
// ErrBadChord). One bad line never discards the others (D248).
func ParseOverrides(lines []string) (Overrides, error) {
	o, _, err := parseOverrides(lines)
	return o, err
}

// InvalidOverrides returns the lines of the stored form that
// ParseOverrides skips, verbatim.
func InvalidOverrides(lines []string) []string {
	_, bad, _ := parseOverrides(lines)
	return bad
}

func parseOverrides(lines []string) (Overrides, []string, error) {
	o := Overrides{}
	var bad []string
	var errs []error
lines:
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		cs := make([]Chord, 0, len(f)-1)
		for _, s := range f[1:] {
			c, err := ParseChord(s)
			if err != nil {
				bad = append(bad, l)
				errs = append(errs, fmt.Errorf("keymap override %q: %w", f[0], err))
				continue lines
			}
			cs = append(cs, c)
		}
		o[Action(f[0])] = cs
	}
	return o, bad, errors.Join(errs...)
}

// overrideAction returns the action of a stored override line.
func overrideAction(line string) Action {
	if f := strings.Fields(line); len(f) > 0 {
		return Action(f[0])
	}
	return ""
}

// Format returns the stored form of o, sorted by action.
func (o Overrides) Format() []string {
	out := make([]string, 0, len(o))
	for _, a := range slices.Sorted(maps.Keys(o)) {
		parts := []string{string(a)}
		for _, c := range o[a] {
			parts = append(parts, c.String())
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

// Clone returns an independent copy of o.
func (o Overrides) Clone() Overrides {
	c := make(Overrides, len(o))
	for a, cs := range o {
		c[a] = slices.Clone(cs)
	}
	return c
}

// Entry is one resolved action of a Scheme.
type Entry struct {
	ID      Action
	Title   string
	Context Context
	// Chords are the effective chords; Defaults the preset's chords.
	Chords   []Chord
	Defaults []Chord
	Source   Source
}

// Conflict is a chord bound to two actions whose contexts overlap.
type Conflict struct {
	Chord Chord
	// A and B are the two actions (A sorts first) with their contexts.
	A, B       Action
	CtxA, CtxB Context
}

// Scheme is the catalog resolved for a preset and user overrides: the one
// lookup table of the editor and the window. It is immutable; build a new
// one when the overrides change.
type Scheme struct {
	preset  Preset
	entries []Entry
	index   map[Action]int
	lookup  map[Context]map[Chord]Action
}

// NewScheme resolves Defs (plus extra definitions, e.g. actions of
// plugins; extra entries with a catalog ID are ignored) for preset p with
// the user overrides ov. Overrides of unknown actions are ignored. An
// invalid preset falls back to PresetPC.
func NewScheme(p Preset, ov Overrides, extra ...Def) *Scheme {
	if !p.Valid() {
		p = PresetPC
	}
	s := &Scheme{preset: p, index: map[Action]int{}, lookup: map[Context]map[Chord]Action{}}
	for _, d := range append(Defs(), extra...) {
		if _, dup := s.index[d.ID]; dup || d.ID == "" {
			continue
		}
		if !d.Context.Valid() {
			d.Context = CtxGlobal
		}
		e := Entry{ID: d.ID, Title: d.Title, Context: d.Context, Source: SourceDefault}
		for _, str := range d.chords(p) {
			if c, err := ParseChord(str); err == nil {
				e.Defaults = append(e.Defaults, c)
			}
		}
		e.Chords = slices.Clone(e.Defaults)
		if cs, ok := ov[d.ID]; ok {
			e.Chords, e.Source = slices.Clone(cs), SourceUser
		}
		s.index[d.ID] = len(s.entries)
		s.entries = append(s.entries, e)
	}
	for _, e := range s.byPriority() {
		m := s.lookup[e.Context]
		if m == nil {
			m = map[Chord]Action{}
			s.lookup[e.Context] = m
		}
		for _, c := range e.Chords {
			if _, taken := m[c]; !taken {
				m[c] = e.ID
			}
		}
	}
	return s
}

// byPriority returns the entries in binding priority: user bindings
// (SourceUser) first, then the defaults, each in catalog order. A chord
// the user bound therefore shadows a default binding of the same chord,
// whatever the catalog order (D247); Conflicts still reports the pair.
func (s *Scheme) byPriority() []*Entry {
	out := make([]*Entry, 0, len(s.entries))
	for _, user := range []bool{true, false} {
		for i := range s.entries {
			if (s.entries[i].Source == SourceUser) == user {
				out = append(out, &s.entries[i])
			}
		}
	}
	return out
}

// IsUser reports whether the chords of a come from a user override.
func (s *Scheme) IsUser(a Action) bool {
	i, ok := s.index[a]
	return ok && s.entries[i].Source == SourceUser
}

// Preset returns the scheme's preset.
func (s *Scheme) Preset() Preset { return s.preset }

// Entries returns all entries in catalog order (copies).
func (s *Scheme) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	for i, e := range s.entries {
		e.Chords, e.Defaults = slices.Clone(e.Chords), slices.Clone(e.Defaults)
		out[i] = e
	}
	return out
}

// Entry returns the entry of action a.
func (s *Scheme) Entry(a Action) (Entry, bool) {
	i, ok := s.index[a]
	if !ok {
		return Entry{}, false
	}
	e := s.entries[i]
	e.Chords, e.Defaults = slices.Clone(e.Chords), slices.Clone(e.Defaults)
	return e, true
}

// Has reports whether a is part of the scheme.
func (s *Scheme) Has(a Action) bool {
	_, ok := s.index[a]
	return ok
}

// Chords returns the effective chords of a (nil if unbound or unknown).
func (s *Scheme) Chords(a Action) []Chord {
	if i, ok := s.index[a]; ok {
		return slices.Clone(s.entries[i].Chords)
	}
	return nil
}

// ChordText returns the first keyboard chord of a for display ("" if
// none); gestures are skipped.
func (s *Scheme) ChordText(a Action) string {
	for _, c := range s.Chords(a) {
		if !IsGesture(c) {
			return c.String()
		}
	}
	return ""
}

// Lookup returns the action bound to c in context ctx. Bindings of ctx
// itself win over global ones.
func (s *Scheme) Lookup(ctx Context, c Chord) (Action, bool) {
	if a, ok := s.lookup[ctx][c]; ok {
		return a, true
	}
	if ctx != CtxGlobal {
		a, ok := s.lookup[CtxGlobal][c]
		return a, ok
	}
	return "", false
}

// Keymap returns the keyboard bindings of context ctx (exactly that
// context, gestures left out) whose action passes keep (nil: all). User
// bindings win over defaults of the same chord.
func (s *Scheme) Keymap(ctx Context, keep func(Action) bool) *Keymap {
	k := New(string(s.preset) + ":" + string(ctx))
	for _, e := range s.byPriority() {
		if e.Context != ctx || (keep != nil && !keep(e.ID)) {
			continue
		}
		for _, c := range e.Chords {
			if IsGesture(c) {
				continue
			}
			if _, taken := k.Lookup(c); !taken {
				k.Bind(c, e.ID)
			}
		}
	}
	return k
}

// EditorKeymap returns the bindings the editor widget executes itself.
func (s *Scheme) EditorKeymap() *Keymap { return s.Keymap(CtxEditor, IsWidgetAction) }

// Conflicts returns all pairs of actions sharing a chord in overlapping
// contexts, sorted by chord and actions.
func (s *Scheme) Conflicts() []Conflict {
	type at struct {
		a   Action
		ctx Context
	}
	by := map[Chord][]at{}
	for _, e := range s.entries {
		for _, c := range slices.Compact(slices.Clone(e.Chords)) {
			by[c] = append(by[c], at{e.ID, e.Context})
		}
	}
	var out []Conflict
	for c, list := range by {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				x, y := list[i], list[j]
				if x.a == y.a || !x.ctx.Overlaps(y.ctx) {
					continue
				}
				if y.a < x.a {
					x, y = y, x
				}
				out = append(out, Conflict{Chord: c, A: x.a, B: y.a, CtxA: x.ctx, CtxB: y.ctx})
			}
		}
	}
	slices.SortFunc(out, func(p, q Conflict) int {
		if c := strings.Compare(p.Chord.String(), q.Chord.String()); c != 0 {
			return c
		}
		if c := strings.Compare(string(p.A), string(q.A)); c != 0 {
			return c
		}
		return strings.Compare(string(p.B), string(q.B))
	})
	return out
}

// ConflictsOf returns the conflicts involving action a.
func (s *Scheme) ConflictsOf(a Action) []Conflict {
	var out []Conflict
	for _, c := range s.Conflicts() {
		if c.A == a || c.B == a {
			out = append(out, c)
		}
	}
	return out
}

// Shadowed returns the chords of a that do not reach it: another action
// of the same context wins the chord in the lookup (a user binding over a
// default, D247, or the earlier catalog entry). Global chords that an
// editor or tool window binding covers only in that context are not
// counted (D223).
func (s *Scheme) Shadowed(a Action) []Chord {
	i, ok := s.index[a]
	if !ok {
		return nil
	}
	e := s.entries[i]
	var out []Chord
	for _, c := range e.Chords {
		if w, ok := s.lookup[e.Context][c]; ok && w != a && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// ConflictsWith returns the conflicts that binding c to a would cause:
// one per other action that has c in an overlapping context. The result
// is empty if c is free for a (or a is unknown).
func (s *Scheme) ConflictsWith(a Action, c Chord) []Conflict {
	i, ok := s.index[a]
	if !ok {
		return nil
	}
	ctx := s.entries[i].Context
	var out []Conflict
	for _, e := range s.entries {
		if e.ID == a || !e.Context.Overlaps(ctx) || !slices.Contains(e.Chords, c) {
			continue
		}
		out = append(out, Conflict{Chord: c, A: a, B: e.ID, CtxA: ctx, CtxB: e.Context})
	}
	return out
}
