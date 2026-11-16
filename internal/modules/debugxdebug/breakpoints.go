package debugxdebug

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
)

// SettingBreakpoints stores the project breakpoints ([]Breakpoint).
const SettingBreakpoints = "debug.breakpoints"

// Breakpoint is a project breakpoint. It exists independently of debug
// sessions and is set in every new session while enabled.
type Breakpoint struct {
	// ID is "<path>:<line>".
	ID string `json:"id"`
	// Path is project relative (slash separated) inside the project,
	// absolute otherwise.
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Condition string `json:"condition,omitempty"`
	Enabled   bool   `json:"enabled"`
}

func bpID(path string, line int) string { return fmt.Sprintf("%s:%d", path, line) }

// BreakpointParams address and describe a project breakpoint.
type BreakpointParams struct {
	// ID addresses an existing breakpoint (alternative to File+Line).
	ID   string `json:"id,omitempty"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Condition is a PHP expression; the breakpoint only stops when it is
	// true (set only).
	Condition string `json:"condition,omitempty"`
	// Enabled defaults to true for set; required for enable.
	Enabled *bool `json:"enabled,omitempty"`
}

// BreakpointsResult is the result of the debug.breakpoints.* methods.
type BreakpointsResult struct {
	Breakpoints []Breakpoint `json:"breakpoints"`
	// Changed is the breakpoint that was set, removed or toggled.
	Changed *Breakpoint `json:"changed,omitempty"`
}

func (m *Module) load() []Breakpoint {
	out := settings.Get[[]Breakpoint](m.app.SettingsStore(), SettingBreakpoints, nil)
	if out == nil {
		out = []Breakpoint{}
	}
	return out
}

func (m *Module) save(list []Breakpoint) error {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Path != list[j].Path {
			return list[i].Path < list[j].Path
		}
		return list[i].Line < list[j].Line
	})
	if err := settings.Set(m.app.SettingsStore(), SettingBreakpoints, list); err != nil {
		return api.Errorf(api.CodeFailed, "saving breakpoints: %w", err)
	}
	m.app.Bus().Publish(api.TopicDebugBreakpoints, list)
	return nil
}

// abs resolves a stored breakpoint path.
func (m *Module) abs(path string) string {
	a, err := m.app.ResolvePath(path)
	if err != nil {
		return path
	}
	return a
}

// find returns the index of the breakpoint p addresses, or -1.
func (m *Module) find(list []Breakpoint, p BreakpointParams) (int, error) {
	id := p.ID
	if id == "" {
		if p.File == "" || p.Line < 1 {
			return -1, api.Errorf(api.CodeInvalidParams, "id or file and line (>= 1) are required")
		}
		abs, err := m.app.ResolvePath(p.File)
		if err != nil {
			return -1, err
		}
		id = bpID(m.app.DisplayPath(abs), p.Line)
	}
	for i, bp := range list {
		if bp.ID == id {
			return i, nil
		}
	}
	return -1, nil
}

func (m *Module) bpList(context.Context, struct{}) (BreakpointsResult, error) {
	return BreakpointsResult{Breakpoints: m.load()}, nil
}

func (m *Module) bpSet(ctx context.Context, p BreakpointParams) (BreakpointsResult, error) {
	if p.File == "" || p.Line < 1 {
		return BreakpointsResult{}, api.Errorf(api.CodeInvalidParams, "file and line (>= 1) are required")
	}
	abs, err := m.app.ResolvePath(p.File)
	if err != nil {
		return BreakpointsResult{}, err
	}
	m.bpMu.Lock()
	defer m.bpMu.Unlock()
	list := m.load()
	path := m.app.DisplayPath(abs)
	bp := Breakpoint{ID: bpID(path, p.Line), Path: path, Line: p.Line, Condition: strings.TrimSpace(p.Condition), Enabled: p.Enabled == nil || *p.Enabled}
	i, _ := m.find(list, BreakpointParams{ID: bp.ID})
	if i >= 0 {
		list[i] = bp
	} else {
		list = append(list, bp)
	}
	if err := m.save(list); err != nil {
		return BreakpointsResult{}, err
	}
	for _, s := range m.sessions() {
		m.remove(ctx, s, bp.ID) // a changed condition needs a new engine breakpoint
		m.apply(ctx, s, bp)
	}
	return BreakpointsResult{Breakpoints: list, Changed: &bp}, nil
}

func (m *Module) bpRemove(ctx context.Context, p BreakpointParams) (BreakpointsResult, error) {
	m.bpMu.Lock()
	defer m.bpMu.Unlock()
	list := m.load()
	i, err := m.find(list, p)
	if err != nil {
		return BreakpointsResult{}, err
	}
	if i < 0 {
		return BreakpointsResult{}, api.Errorf(api.CodeNotFound, "no breakpoint %s", describe(p))
	}
	bp := list[i]
	list = append(list[:i], list[i+1:]...)
	if err := m.save(list); err != nil {
		return BreakpointsResult{}, err
	}
	for _, s := range m.sessions() {
		m.remove(ctx, s, bp.ID)
	}
	return BreakpointsResult{Breakpoints: list, Changed: &bp}, nil
}

func (m *Module) bpEnable(ctx context.Context, p BreakpointParams) (BreakpointsResult, error) {
	if p.Enabled == nil {
		return BreakpointsResult{}, api.Errorf(api.CodeInvalidParams, "enabled is required")
	}
	m.bpMu.Lock()
	defer m.bpMu.Unlock()
	list := m.load()
	i, err := m.find(list, p)
	if err != nil {
		return BreakpointsResult{}, err
	}
	if i < 0 {
		return BreakpointsResult{}, api.Errorf(api.CodeNotFound, "no breakpoint %s", describe(p))
	}
	list[i].Enabled = *p.Enabled
	bp := list[i]
	if err := m.save(list); err != nil {
		return BreakpointsResult{}, err
	}
	for _, s := range m.sessions() {
		m.mu.Lock()
		eid := m.engineIDs[s.ID()][bp.ID]
		m.mu.Unlock()
		switch {
		case eid != "":
			if err := s.SetBreakpointEnabled(ctx, eid, bp.Enabled); err != nil {
				m.app.Logger().Warn("debug: breakpoint_update", "err", err)
			}
		case bp.Enabled:
			m.apply(ctx, s, bp)
		}
	}
	return BreakpointsResult{Breakpoints: list, Changed: &bp}, nil
}

func describe(p BreakpointParams) string {
	if p.ID != "" {
		return p.ID
	}
	return bpID(p.File, p.Line)
}

// applyAll sets all enabled project breakpoints in s and returns how many
// were set.
func (m *Module) applyAll(ctx context.Context, s *dbgp.Session) int {
	n := 0
	for _, bp := range m.load() {
		if bp.Enabled && m.apply(ctx, s, bp) {
			n++
		}
	}
	return n
}

// apply sets bp in s and records the engine ID.
func (m *Module) apply(ctx context.Context, s *dbgp.Session, bp Breakpoint) bool {
	if !bp.Enabled {
		return false
	}
	set, err := s.SetBreakpoint(ctx, dbgp.Breakpoint{File: m.abs(bp.Path), Line: bp.Line, Condition: bp.Condition})
	if err != nil {
		m.app.Logger().Warn("debug: breakpoint_set", "breakpoint", bp.ID, "err", err)
		return false
	}
	m.mu.Lock()
	if m.engineIDs[s.ID()] == nil {
		m.engineIDs[s.ID()] = map[string]string{}
	}
	m.engineIDs[s.ID()][bp.ID] = set.ID
	m.mu.Unlock()
	return true
}

// remove deletes the engine breakpoint of project breakpoint id from s.
func (m *Module) remove(ctx context.Context, s *dbgp.Session, id string) {
	m.mu.Lock()
	eid := m.engineIDs[s.ID()][id]
	delete(m.engineIDs[s.ID()], id)
	m.mu.Unlock()
	if eid == "" {
		return
	}
	if err := s.RemoveBreakpoint(ctx, eid); err != nil {
		m.app.Logger().Warn("debug: breakpoint_remove", "err", err)
	}
}
