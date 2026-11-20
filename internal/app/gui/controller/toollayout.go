package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// ToolGroup is the slot of a side a tool window opens in: every side has
// a primary and a split slot, so two tool windows can be shown on one
// side at the same time (left/right: stacked, bottom: side by side).
type ToolGroup string

// Tool window groups.
const (
	GroupPrimary ToolGroup = "primary"
	GroupSplit   ToolGroup = "split"
)

// ToolGroups lists the groups in display order.
var ToolGroups = []ToolGroup{GroupPrimary, GroupSplit}

// Valid reports whether g is a known group.
func (g ToolGroup) Valid() bool { return g == GroupPrimary || g == GroupSplit }

// KeyToolLayout is the per-project setting holding the tool window layout.
const KeyToolLayout = "ui.toolWindows"

// SideState is the state of one slot (side and group): the tool window
// in front, whether the slot's panel is open and its size.
type SideState struct {
	Active string `json:"active,omitempty"`
	Open   bool   `json:"open"`
	// Size is the panel's share of the window width (left, right) or of
	// the editor column height (bottom), 0..1; 0 means the default. For
	// split slots it is the share of the primary panel within the
	// divided side.
	Size float32 `json:"size,omitempty"`
	// Tabs are the tool windows grouped as tabs in the slot's panel (the
	// header shows them while one of them is in front); fewer than two
	// means no tab group.
	Tabs []string `json:"tabs,omitempty"`
}

// equal reports whether two slot states are the same.
func (s SideState) equal(o SideState) bool {
	if s.Active != o.Active || s.Open != o.Open || s.Size != o.Size || len(s.Tabs) != len(o.Tabs) {
		return false
	}
	for i := range s.Tabs {
		if s.Tabs[i] != o.Tabs[i] {
			return false
		}
	}
	return true
}

// ToolLayout is the docking state of the tool windows of one project
// window: the side and group (slot) of every tool window, the open panel
// per slot, the panel sizes, the maximized panel and the "editor
// maximized" state. Each slot shows at most one tool window at a time,
// so a side shows up to two (primary and split). It is Gio-free and
// persisted per project (KeyToolLayout) by the controller.
type ToolLayout struct {
	// Tools maps tool window IDs to their current side (only tools the
	// view registered or the user moved).
	Tools map[string]ToolSide
	// Groups maps tool window IDs to their current group (as Tools).
	Groups map[string]ToolGroup
	// Sides holds the state of the primary slot per side.
	Sides map[ToolSide]*SideState
	// Splits holds the state of the split slot per side; its Size is the
	// share of the primary panel while both panels are open.
	Splits map[ToolSide]*SideState
	// MaxPanel is the tool window maximized over editor and panels (""
	// none).
	MaxPanel string
	// BottomFull tells per bottom corner (SideLeft, SideRight) whether
	// the bottom panel owns it, i.e. spans under the side panels of that
	// side; otherwise the side panels span the full height (default).
	BottomFull map[ToolSide]bool
	// Version changes on every change (the view and the persistence
	// follow it).
	Version uint64

	defaults  map[string]ToolSide
	defGroups map[string]ToolGroup
	maximized map[slotKey]bool // open slots before ToggleMaximize
	pending   map[string]bool  // shown before registration
}

// slotKey names a slot.
type slotKey struct {
	side  ToolSide
	group ToolGroup
}

// Default panel sizes.
const (
	DefaultSideSize   = 0.22
	DefaultBottomSize = 0.32
	DefaultSplitSize  = 0.5
)

// NewToolLayout returns the default layout: nothing registered, all
// panels closed.
func NewToolLayout() *ToolLayout {
	l := &ToolLayout{
		Tools: map[string]ToolSide{}, Groups: map[string]ToolGroup{},
		Sides: map[ToolSide]*SideState{}, Splits: map[ToolSide]*SideState{},
		BottomFull: map[ToolSide]bool{},
		defaults:   map[string]ToolSide{}, defGroups: map[string]ToolGroup{},
	}
	for _, s := range ToolSides {
		l.Sides[s] = &SideState{}
		l.Splits[s] = &SideState{}
	}
	return l
}

func (l *ToolLayout) changed() { l.Version++ }

// Slot returns the state of slot (side, g).
func (l *ToolLayout) Slot(side ToolSide, g ToolGroup) *SideState {
	m := l.Sides
	if g == GroupSplit {
		m = l.Splits
	}
	s := m[side]
	if s == nil {
		s = &SideState{}
		m[side] = s
	}
	return s
}

// slotOf returns the slot state of tool window id.
func (l *ToolLayout) slotOf(id string) *SideState { return l.Slot(l.SideOf(id), l.GroupOf(id)) }

// eachSlot calls fn for every slot.
func (l *ToolLayout) eachSlot(fn func(k slotKey, s *SideState)) {
	for _, side := range ToolSides {
		for _, g := range ToolGroups {
			fn(slotKey{side, g}, l.Slot(side, g))
		}
	}
}

// Register makes tool window id known with its default side (primary
// group). A placement stored earlier (moved by the user) is kept.
func (l *ToolLayout) Register(id string, def ToolSide) { l.RegisterIn(id, def, GroupPrimary) }

// RegisterIn is Register with a default group. A window shown before it
// was registered opens now.
func (l *ToolLayout) RegisterIn(id string, def ToolSide, group ToolGroup) {
	if !def.Valid() {
		def = SideBottom
	}
	if !group.Valid() {
		group = GroupPrimary
	}
	l.defaults[id] = def
	l.defGroups[id] = group
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
// placement is kept for a later Register.
func (l *ToolLayout) Unregister(id string) {
	if _, ok := l.Tools[id]; !ok {
		return
	}
	if s := l.slotOf(id); s.Active == id {
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

// GroupOf returns the group of tool window id (primary if unknown).
func (l *ToolLayout) GroupOf(id string) ToolGroup {
	if g, ok := l.Groups[id]; ok && g.Valid() {
		return g
	}
	if g, ok := l.defGroups[id]; ok {
		return g
	}
	return GroupPrimary
}

// Visible reports whether tool window id is shown.
func (l *ToolLayout) Visible(id string) bool {
	s := l.slotOf(id)
	return s.Open && s.Active == id
}

// Shown returns the tool window shown in slot (side, g), or "".
func (l *ToolLayout) Shown(side ToolSide, g ToolGroup) string {
	if s := l.Slot(side, g); s.Open {
		return s.Active
	}
	return ""
}

// Show opens tool window id in its slot (replacing the one in front). A
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
	if !known {
		l.Tools[id] = l.SideOf(id)
	}
	s := l.slotOf(id)
	if s.Open && s.Active == id {
		return
	}
	s.Active, s.Open = id, true
	l.maximized = nil
	l.changed()
}

// Hide closes tool window id if it is shown.
func (l *ToolLayout) Hide(id string) {
	s := l.slotOf(id)
	if s.Open && s.Active == id {
		s.Open = false
		if l.MaxPanel == id {
			l.MaxPanel = ""
		}
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

// HideSide closes both panels of side.
func (l *ToolLayout) HideSide(side ToolSide) {
	for _, g := range ToolGroups {
		if s := l.Slot(side, g); s.Open {
			s.Open = false
			l.changed()
		}
	}
}

// Move docks tool window id in the primary slot of side; a shown window
// stays shown there.
func (l *ToolLayout) Move(id string, side ToolSide) { l.MoveTo(id, side, GroupPrimary) }

// MoveTo docks tool window id in slot (side, group); a shown window stays
// shown there (replacing the window in front).
func (l *ToolLayout) MoveTo(id string, side ToolSide, group ToolGroup) {
	if !side.Valid() || !group.Valid() {
		return
	}
	oldSide, oldGroup := l.SideOf(id), l.GroupOf(id)
	if oldSide == side && oldGroup == group {
		_, ok := l.Tools[id]
		_, gok := l.Groups[id]
		if !ok || !gok {
			l.Tools[id], l.Groups[id] = side, group
			l.changed()
		}
		return
	}
	shown := l.Visible(id)
	old := l.Slot(oldSide, oldGroup)
	if old.Active == id {
		old.Active, old.Open = "", false
		// Another tab of the group takes its place.
		for _, t := range old.Tabs {
			if t != id {
				old.Active, old.Open = t, shown
				break
			}
		}
	}
	old.Tabs = removeTab(old.Tabs, id)
	l.Tools[id], l.Groups[id] = side, group
	if shown {
		s := l.Slot(side, group)
		s.Active, s.Open = id, true
	}
	l.changed()
}

// removeTab returns tabs without id; a single remaining tab is no group.
func removeTab(tabs []string, id string) []string {
	var out []string
	for _, t := range tabs {
		if t != id {
			out = append(out, t)
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// JoinTab docks tool window id in slot (side, group) as a tab next to
// the window in front there and shows it. The slot's panel then lists
// both (and earlier tabs) in its header.
func (l *ToolLayout) JoinTab(id string, side ToolSide, group ToolGroup) {
	if !side.Valid() || !group.Valid() {
		return
	}
	front := l.Slot(side, group).Active
	if front == id && l.SideOf(id) == side && l.GroupOf(id) == group {
		return
	}
	l.MoveTo(id, side, group)
	s := l.Slot(side, group)
	tabs := s.Tabs
	if front != "" && front != id && !contains(tabs, front) {
		tabs = append(tabs, front)
	}
	if !contains(tabs, id) {
		tabs = append(tabs, id)
	}
	s.Tabs = tabs
	s.Active, s.Open = id, true
	l.maximized = nil
	l.changed()
}

// LeaveTabs removes tool window id from the tab group of its slot (it
// stays docked there).
func (l *ToolLayout) LeaveTabs(id string) {
	s := l.slotOf(id)
	if !contains(s.Tabs, id) {
		return
	}
	s.Tabs = removeTab(s.Tabs, id)
	l.changed()
}

// TabsOf returns the tab group shown in the header of slot (side, g):
// the slot's tabs while the window in front belongs to them, else nil.
func (l *ToolLayout) TabsOf(side ToolSide, g ToolGroup) []string {
	s := l.Slot(side, g)
	if len(s.Tabs) < 2 || !contains(s.Tabs, s.Active) {
		return nil
	}
	return s.Tabs
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// IsBottomFull reports whether the bottom panel owns the bottom corner
// of side (left or right).
func (l *ToolLayout) IsBottomFull(side ToolSide) bool { return l.BottomFull[side] }

// SetBottomFull gives the bottom corner of side (left or right) to the
// bottom panel (on) or to the side panels.
func (l *ToolLayout) SetBottomFull(side ToolSide, on bool) {
	if side != SideLeft && side != SideRight || l.BottomFull[side] == on {
		return
	}
	if l.BottomFull == nil {
		l.BottomFull = map[ToolSide]bool{}
	}
	if on {
		l.BottomFull[side] = true
	} else {
		delete(l.BottomFull, side)
	}
	l.changed()
}

// Reset restores the default layout: every registered window at its
// default side and group, all panels closed but the project tree, default
// sizes and corners, nothing maximized.
func (l *ToolLayout) Reset() {
	l.reset()
	l.Show(DefaultOpenTool)
	l.changed()
}

// reset clears all placements and slots (registrations stay).
func (l *ToolLayout) reset() {
	l.Tools, l.Groups = map[string]ToolSide{}, map[string]ToolGroup{}
	for id, side := range l.defaults {
		l.Tools[id] = side
	}
	for _, side := range ToolSides {
		l.Sides[side], l.Splits[side] = &SideState{}, &SideState{}
	}
	l.MaxPanel, l.BottomFull, l.maximized = "", map[ToolSide]bool{}, nil
}

// Apply replaces the layout by a stored one (the JSON form of
// MarshalJSON, e.g. a named layout); registrations stay. A layout
// without an open panel shows the project tree.
func (l *ToolLayout) Apply(data []byte) error {
	var probe toolLayoutFile
	if t := bytes.TrimSpace(data); len(t) == 0 || t[0] != '{' {
		return fmt.Errorf("tool layout: not a JSON object")
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("tool layout: %w", err)
	}
	l.reset()
	if err := l.UnmarshalJSON(data); err != nil {
		return err
	}
	if !l.AnyOpen() {
		l.Show(DefaultOpenTool)
	}
	l.changed()
	return nil
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

// SplitSize returns the share of the primary panel of a divided side
// (DefaultSplitSize if unset).
func (l *ToolLayout) SplitSize(side ToolSide) float32 {
	if s := l.Splits[side]; s != nil && s.Size > 0 {
		return s.Size
	}
	return DefaultSplitSize
}

// SetSplitSize sets the share of the primary panel of a divided side
// (clamped to 0.1..0.9).
func (l *ToolLayout) SetSplitSize(side ToolSide, share float32) {
	if !side.Valid() {
		return
	}
	share = min(max(share, 0.1), 0.9)
	if s := l.Slot(side, GroupSplit); s.Size != share {
		s.Size = share
		l.changed()
	}
}

// SetMaxPanel maximizes tool window id over editor and panels ("":
// restore).
func (l *ToolLayout) SetMaxPanel(id string) {
	if l.MaxPanel != id {
		l.MaxPanel = id
		l.changed()
	}
}

// AnyOpen reports whether a panel is open.
func (l *ToolLayout) AnyOpen() bool {
	open := false
	l.eachSlot(func(_ slotKey, s *SideState) {
		if s.Open && s.Active != "" {
			open = true
		}
	})
	return open
}

// Maximized reports whether the editor is maximized (panels hidden by
// ToggleMaximize).
func (l *ToolLayout) Maximized() bool { return l.maximized != nil }

// ToggleMaximize hides all open panels and remembers them; called again
// (or with no panel open but a remembered state) it restores them.
func (l *ToolLayout) ToggleMaximize() {
	if l.maximized != nil && !l.AnyOpen() {
		for k, open := range l.maximized {
			if s := l.Slot(k.side, k.group); open && s.Active != "" {
				s.Open = true
			}
		}
		l.maximized = nil
		l.changed()
		return
	}
	m := map[slotKey]bool{}
	l.eachSlot(func(k slotKey, s *SideState) {
		m[k] = s.Open
		s.Open = false
	})
	l.maximized = m
	l.MaxPanel = ""
	l.changed()
}

// ToolsAt returns the IDs of the registered tool windows docked at side
// (both groups), sorted by order (lower first, then ID).
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
	Tools  map[string]ToolSide    `json:"tools,omitempty"`
	Groups map[string]ToolGroup   `json:"groups,omitempty"`
	Sides  map[ToolSide]SideState `json:"sides,omitempty"`
	Splits map[ToolSide]SideState `json:"splits,omitempty"`
	// Maximized is the maximized tool window.
	Maximized string `json:"maximized,omitempty"`
	// Corners names the owner of each bottom corner ("bottom"; missing:
	// the side panels).
	Corners map[ToolSide]string `json:"corners,omitempty"`
}

// cornerBottom is the persisted owner of a corner held by the bottom panel.
const cornerBottom = "bottom"

// MarshalJSON stores the slots and the user's placements.
func (l *ToolLayout) MarshalJSON() ([]byte, error) {
	f := toolLayoutFile{Tools: map[string]ToolSide{}, Groups: map[string]ToolGroup{}, Sides: map[ToolSide]SideState{},
		Splits: map[ToolSide]SideState{}, Maximized: l.MaxPanel}
	for side, on := range l.BottomFull {
		if on {
			if f.Corners == nil {
				f.Corners = map[ToolSide]string{}
			}
			f.Corners[side] = cornerBottom
		}
	}
	for id, s := range l.Tools {
		if l.defaults[id] != s || l.defaults[id] == "" {
			f.Tools[id] = s
		}
	}
	for id, g := range l.Groups {
		if def, ok := l.defGroups[id]; !ok || def != g {
			f.Groups[id] = g
		}
	}
	l.eachSlot(func(k slotKey, s *SideState) {
		st := *s
		if l.maximized != nil {
			st.Open = l.maximized[k]
		}
		if k.group == GroupSplit {
			if !st.equal(SideState{}) {
				f.Splits[k.side] = st
			}
			return
		}
		f.Sides[k.side] = st
	})
	return json.Marshal(f)
}

// UnmarshalJSON restores a stored layout (unknown sides and groups are
// ignored).
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
	for id, g := range f.Groups {
		if g.Valid() {
			l.Groups[id] = g
		}
	}
	restore := func(dst map[ToolSide]*SideState, src map[ToolSide]SideState, lo float32) {
		for side, s := range src {
			if !side.Valid() {
				continue
			}
			st := s
			st.Size = 0
			st.Tabs = nil
			for _, id := range s.Tabs {
				if id != "" && !contains(st.Tabs, id) {
					st.Tabs = append(st.Tabs, id)
				}
			}
			if len(st.Tabs) < 2 {
				st.Tabs = nil
			}
			if s.Size > 0 {
				st.Size = min(max(s.Size, lo), 0.9)
			}
			dst[side] = &st
		}
	}
	restore(l.Sides, f.Sides, 0.05)
	restore(l.Splits, f.Splits, 0.1)
	l.MaxPanel = f.Maximized
	for _, side := range []ToolSide{SideLeft, SideRight} {
		if f.Corners[side] == cornerBottom {
			if l.BottomFull == nil {
				l.BottomFull = map[ToolSide]bool{}
			}
			l.BottomFull[side] = true
		}
	}
	l.changed()
	return nil
}
