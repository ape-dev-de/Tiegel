package api

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Tool window layouts ("layout.*"). The GUI keeps the docking state of
// its tool windows (sides, slots, sizes, tab groups, corner owners) per
// project in SettingToolLayout; named layouts the user saved live in the
// global SettingToolLayouts so that they can be applied to any project.
// The kernel treats a layout as an opaque JSON object: only the GUI
// interprets it (controller.ToolLayout), so a layout written by a newer
// client survives an older one.

// Layout setting keys.
const (
	// SettingToolLayout is the project setting with the current layout.
	SettingToolLayout = "ui.toolWindows"
	// SettingToolLayouts is the global setting with the named layouts
	// (object: name → layout).
	SettingToolLayouts = "ui.toolLayouts"
)

// layoutMu serializes read-modify-write of the named layouts.
var layoutMu sync.Mutex

// LayoutParams address a layout: without Name the project's current
// layout, with Name a named layout.
type LayoutParams struct {
	Name string `json:"name,omitempty"`
}

// LayoutSaveParams are the params of "layout.save": Name is required;
// Layout is the layout to store (default: the project's current one).
type LayoutSaveParams struct {
	Name   string          `json:"name"`
	Layout json.RawMessage `json:"layout,omitempty"`
}

// LayoutState is the result of the layout methods.
type LayoutState struct {
	// Name is the named layout read, saved, applied or deleted ("" for
	// the project's layout).
	Name string `json:"name,omitempty"`
	// Found reports whether Layout is stored (false: defaults apply).
	Found  bool            `json:"found"`
	Layout json.RawMessage `json:"layout,omitempty"`
	// Names are all named layouts, sorted.
	Names []string `json:"names"`
}

func (a *App) registerLayout() {
	mustHandle(a, "layout.get", "Tool window layout of the project (or a named layout with name) and the names of all saved layouts", a.layoutGet)
	mustHandle(a, "layout.reset", "Reset the project's tool window layout to the defaults (the GUI applies it when the project is opened)", a.layoutReset)
	mustHandle(a, "layout.save", "Save a tool window layout under a name (layout: JSON object; default: the project's current layout)", a.layoutSave)
	mustHandle(a, "layout.apply", "Make a named layout the project's tool window layout", a.layoutApply)
	mustHandle(a, "layout.delete", "Delete a named tool window layout", a.layoutDelete)
}

// namedLayouts returns the stored named layouts.
func (a *App) namedLayouts() (map[string]json.RawMessage, error) {
	m, _, err := settings.Lookup[map[string]json.RawMessage](a.global, SettingToolLayouts)
	if err != nil {
		return nil, Errorf(CodeFailed, "settings: %w", err)
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

func layoutNames(m map[string]json.RawMessage) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// projectLayout returns the project's stored layout.
func (a *App) projectLayout() (json.RawMessage, bool, error) {
	ps := a.ProjectSettings()
	if ps == nil {
		return nil, false, Errorf(CodeNoProject, "no project open")
	}
	v, ok, err := settings.Lookup[json.RawMessage](ps, SettingToolLayout)
	if err != nil {
		return nil, false, Errorf(CodeFailed, "settings: %w", err)
	}
	return v, ok, nil
}

func layoutName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Errorf(CodeInvalidParams, "name is required")
	}
	if len(name) > 80 {
		return "", Errorf(CodeInvalidParams, "name is too long (max. 80 characters)")
	}
	return name, nil
}

// validLayout checks that raw is a JSON object.
func validLayout(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	var m map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &m) != nil {
		return Errorf(CodeInvalidParams, "layout must be a JSON object")
	}
	return nil
}

func (a *App) layoutGet(_ context.Context, p LayoutParams) (LayoutState, error) {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	named, err := a.namedLayouts()
	if err != nil {
		return LayoutState{}, err
	}
	res := LayoutState{Names: layoutNames(named)}
	if p.Name != "" {
		name, err := layoutName(p.Name)
		if err != nil {
			return LayoutState{}, err
		}
		raw, ok := named[name]
		if !ok {
			return LayoutState{}, Errorf(CodeNotFound, "no layout named %q", name)
		}
		res.Name, res.Found, res.Layout = name, true, raw
		return res, nil
	}
	raw, ok, err := a.projectLayout()
	if err != nil {
		return LayoutState{}, err
	}
	res.Found, res.Layout = ok, raw
	return res, nil
}

func (a *App) layoutReset(_ context.Context, _ struct{}) (LayoutState, error) {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	ps := a.ProjectSettings()
	if ps == nil {
		return LayoutState{}, Errorf(CodeNoProject, "no project open")
	}
	if err := ps.Delete(SettingToolLayout); err != nil {
		return LayoutState{}, Errorf(CodeFailed, "settings: %w", err)
	}
	named, err := a.namedLayouts()
	if err != nil {
		return LayoutState{}, err
	}
	return LayoutState{Names: layoutNames(named)}, nil
}

func (a *App) layoutSave(_ context.Context, p LayoutSaveParams) (LayoutState, error) {
	name, err := layoutName(p.Name)
	if err != nil {
		return LayoutState{}, err
	}
	layoutMu.Lock()
	defer layoutMu.Unlock()
	raw := json.RawMessage(bytes.TrimSpace(p.Layout))
	if len(raw) == 0 {
		cur, ok, err := a.projectLayout()
		if err != nil {
			return LayoutState{}, err
		}
		if !ok {
			cur = json.RawMessage(`{}`) // the defaults
		}
		raw = cur
	}
	if err := validLayout(raw); err != nil {
		return LayoutState{}, err
	}
	named, err := a.namedLayouts()
	if err != nil {
		return LayoutState{}, err
	}
	named[name] = raw
	if err := settings.Set(a.global, SettingToolLayouts, named); err != nil {
		return LayoutState{}, Errorf(CodeFailed, "settings: %w", err)
	}
	return LayoutState{Name: name, Found: true, Layout: raw, Names: layoutNames(named)}, nil
}

func (a *App) layoutApply(_ context.Context, p LayoutParams) (LayoutState, error) {
	name, err := layoutName(p.Name)
	if err != nil {
		return LayoutState{}, err
	}
	layoutMu.Lock()
	defer layoutMu.Unlock()
	ps := a.ProjectSettings()
	if ps == nil {
		return LayoutState{}, Errorf(CodeNoProject, "no project open")
	}
	named, err := a.namedLayouts()
	if err != nil {
		return LayoutState{}, err
	}
	raw, ok := named[name]
	if !ok {
		return LayoutState{}, Errorf(CodeNotFound, "no layout named %q", name)
	}
	if err := settings.Set(ps, SettingToolLayout, raw); err != nil {
		return LayoutState{}, Errorf(CodeFailed, "settings: %w", err)
	}
	return LayoutState{Name: name, Found: true, Layout: raw, Names: layoutNames(named)}, nil
}

func (a *App) layoutDelete(_ context.Context, p LayoutParams) (LayoutState, error) {
	name, err := layoutName(p.Name)
	if err != nil {
		return LayoutState{}, err
	}
	layoutMu.Lock()
	defer layoutMu.Unlock()
	named, err := a.namedLayouts()
	if err != nil {
		return LayoutState{}, err
	}
	if _, ok := named[name]; !ok {
		return LayoutState{}, Errorf(CodeNotFound, "no layout named %q", name)
	}
	delete(named, name)
	if err := settings.Set(a.global, SettingToolLayouts, named); err != nil {
		return LayoutState{}, Errorf(CodeFailed, "settings: %w", err)
	}
	return LayoutState{Name: name, Names: layoutNames(named)}, nil
}
