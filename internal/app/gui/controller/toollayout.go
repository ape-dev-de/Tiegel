package controller

import (
	"encoding/json"
	"sort"
)

// ToolSide is where a tool window is docked.
type ToolSide string

// Tool window sides.
const (
	SideLeft   ToolSide = "left"
	SideRight  ToolSide = "right"
	SideBottom ToolSide = "bottom"
)

// ToolSides lists the sides in display order.
var ToolSides = []ToolSide{SideLeft, SideRight, SideBottom}

// Valid reports whether s is a known side.
func (s ToolSide) Valid() bool { return s == SideLeft || s == SideRight || s == SideBottom }

// KeyToolLayout is the per-project setting holding the tool window layout.
const KeyToolLayout = "ui.toolWindows"

// SideState is the state of one side: the tool window in front, whether
// the side's panel is open and its size.
type SideState struct {
	Active string `json:"active,omitempty"`
	Open   bool   `json:"open"`
	// Size is the panel's share of the window width (left, right) or of
	// the editor column height (bottom), 0..1; 0 means the default.
	Size float32 `json:"size,omitempty"`
}

// ToolLayout is the docking state of the tool windows of one project
// window: the side of every tool window, the open panel per side, the
// panel sizes and the "editor maximized" state. Each side shows at most
// one tool window at a time. It is Gio-free and persisted per project
// (KeyToolLayout) by the controller.
type ToolLayout struct {
	// Tools maps tool window IDs to their current side (only tools the
	// view registered or the user moved).
	Tools map[string]ToolSide
	// Sides holds the state per side.
	Sides map[ToolSide]*SideState
	// Version changes on every change (the view and the persistence
	// follow it).
	Version uint64

	defaults  map[string]ToolSide
	maximized map[ToolSide]bool // open sides before MaximizeEditor
	pending   map[string]bool   // shown before registration
}

// Default panel sizes.
const (
	DefaultSideSize   = 0.22
	DefaultBottomSize = 0.32
)

// NewToolLayout returns the default layout: nothing registered, all
// panels closed.
func NewToolLayout() *ToolLayout {
	l := &ToolLayout{Tools: map[string]ToolSide{}, Sides: map[ToolSide]*SideState{}, defaults: map[string]ToolSide{}}
	for _, s := range ToolSides {
		l.Sides[s] = &SideState{}
	}
	return l
}

func (l *ToolLayout) changed() { l.Version++ }

// Register makes tool window id known with its default side. A side
// stored earlier (moved by the user) is kept. open shows the tool when
// its side has no tool in front yet and the layout was never stored
// (first start).
func (l *ToolLayout) Register(id string, def ToolSide) {
	if !def.Valid() {
		def = SideBottom
	}
	l.defaults[id] = def
	if _, ok := l.Tools[id]; !ok {
		l.Tools[id] = def
		l.changed()
	}
	if l.pending[id] {
		delete(l.pending, id)
		l.Show(id)
	}
}

// Unregister forgets a tool window that went away (module disabled); its
// side is kept for a later Register.
func (l *ToolLayout) Unregister(id string) {
	side, ok := l.Tools[id]
	if !ok {
		return
	}
	if s := l.Sides[side]; s.Active == id {
		s.Active, s.Open = "", false
		l.changed()
	}
}

// SideOf returns the side of tool window id (bottom if unknown).
func (l *ToolLayout) SideOf(id string) ToolSide {
	if s, ok := l.Tools[id]; ok {
		return s
	}
	if s, ok := l.defaults[id]; ok {
		return s
	}
	return SideBottom
}

// Visible reports whether tool window id is shown.
func (l *ToolLayout) Visible(id string) bool {
	s := l.Sides[l.SideOf(id)]
	return s.Open && s.Active == id
}

// Show opens tool window id on its side (replacing the one in front). A
// window that is not registered yet opens when it is registered.
func (l *ToolLayout) Show(id string) {
	_, known := l.Tools[id]
	if _, ok := l.defaults[id]; !ok && !known {
		if l.pending == nil {
			l.pending = map[string]bool{}
		}
		l.pending[id] = true
		return
	}
	side := l.SideOf(id)
	if !known {
		l.Tools[id] = side
	}
	s := l.Sides[side]
	if s.Open && s.Active == id {
		return
	}
	s.Active, s.Open = id, true
	l.maximized = nil
	l.changed()
}

// Hide closes tool window id if it is shown.
func (l *ToolLayout) Hide(id string) {
	s := l.Sides[l.SideOf(id)]
	if s.Open && s.Active == id {
		s.Open = false
		l.changed()
	}
}

// Toggle shows or hides tool window id. It reports whether it is shown
// afterwards.
func (l *ToolLayout) Toggle(id string) bool {
	if l.Visible(id) {
		l.Hide(id)
		return false
	}
	l.Show(id)
	return true
}

// HideSide closes the panel of side.
func (l *ToolLayout) HideSide(side ToolSide) {
	if s := l.Sides[side]; s != nil && s.Open {
		s.Open = false
		l.changed()
	}
}

// Move docks tool window id at side; a shown window stays shown there.
func (l *ToolLayout) Move(id string, side ToolSide) {
	if !side.Valid() {
		return
	}
	old := l.SideOf(id)
	if old == side {
		if _, ok := l.Tools[id]; !ok {
			l.Tools[id] = side
			l.changed()
		}
		return
	}
	shown := l.Visible(id)
	if s := l.Sides[old]; s.Active == id {
		s.Active, s.Open = "", false
	}
	l.Tools[id] = side
	if shown {
		s := l.Sides[side]
		s.Active, s.Open = id, true
	}
	l.changed()
}

// Size returns the share of side (the default if unset).
func (l *ToolLayout) Size(side ToolSide) float32 {
	if s := l.Sides[side]; s != nil && s.Size > 0 {
		return s.Size
	}
	if side == SideBottom {
		return DefaultBottomSize
	}
	return DefaultSideSize
}

// SetSize sets the share of side (clamped to 0.05..0.9).
func (l *ToolLayout) SetSize(side ToolSide, share float32) {
	share = min(max(share, 0.05), 0.9)
	s := l.Sides[side]
	if s == nil || s.Size == share {
		return
	}
	s.Size = share
	l.changed()
}

// AnyOpen reports whether a panel is open.
func (l *ToolLayout) AnyOpen() bool {
	for _, s := range l.Sides {
		if s.Open && s.Active != "" {
			return true
		}
	}
	return false
}

// Maximized reports whether the editor is maximized (panels hidden by
// ToggleMaximize).
func (l *ToolLayout) Maximized() bool { return l.maximized != nil }

// ToggleMaximize hides all open panels and remembers them; called again
// (or with no panel open but a remembered state) it restores them.
func (l *ToolLayout) ToggleMaximize() {
	if l.maximized != nil && !l.AnyOpen() {
		for side, open := range l.maximized {
			if open && l.Sides[side].Active != "" {
				l.Sides[side].Open = true
			}
		}
		l.maximized = nil
		l.changed()
		return
	}
	m := map[ToolSide]bool{}
	for side, s := range l.Sides {
		m[side] = s.Open
		s.Open = false
	}
	l.maximized = m
	l.changed()
}

// ToolsAt returns the IDs of the registered tool windows docked at side,
// sorted by order (lower first, then ID).
func (l *ToolLayout) ToolsAt(side ToolSide, order func(id string) int) []string {
	var out []string
	for id := range l.defaults {
		if l.SideOf(id) == side {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := order(out[i]), order(out[j])
		if oi != oj {
			return oi < oj
		}
		return out[i] < out[j]
	})
	return out
}

// toolLayoutFile is the persisted form.
type toolLayoutFile struct {
	Tools map[string]ToolSide    `json:"tools,omitempty"`
	Sides map[ToolSide]SideState `json:"sides,omitempty"`
}

// MarshalJSON stores sides and the user's placements.
func (l *ToolLayout) MarshalJSON() ([]byte, error) {
	f := toolLayoutFile{Tools: map[string]ToolSide{}, Sides: map[ToolSide]SideState{}}
	for id, s := range l.Tools {
		if l.defaults[id] != s || l.defaults[id] == "" {
			f.Tools[id] = s
		}
	}
	for side, s := range l.Sides {
		st := *s
		if l.maximized != nil {
			st.Open = l.maximized[side]
		}
		f.Sides[side] = st
	}
	return json.Marshal(f)
}

// UnmarshalJSON restores a stored layout (unknown sides are ignored).
func (l *ToolLayout) UnmarshalJSON(data []byte) error {
	var f toolLayoutFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if l.Tools == nil {
		*l = *NewToolLayout()
	}
	for id, s := range f.Tools {
		if s.Valid() {
			l.Tools[id] = s
		}
	}
	for side, s := range f.Sides {
		if side.Valid() {
			st := s
			st.Size = 0
			if s.Size > 0 {
				st.Size = min(max(s.Size, 0.05), 0.9)
			}
			l.Sides[side] = &st
		}
	}
	l.changed()
	return nil
}
