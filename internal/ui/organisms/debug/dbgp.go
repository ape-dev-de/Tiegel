package debug

import "github.com/ape-dev-de/tiegel/internal/dbgp"

// This file converts internal/dbgp values into the view models of this
// package. Only types are used; the organisms never talk to a session.

// StateFromStatus maps a DBGp engine status to a UI state.
func StateFromStatus(s dbgp.Status) State {
	switch s {
	case dbgp.StatusStarting:
		return StateStarting
	case dbgp.StatusRunning:
		return StateRunning
	case dbgp.StatusBreak:
		return StateBreak
	case dbgp.StatusStopping:
		return StateStopping
	case dbgp.StatusStopped:
		return StateStopped
	}
	return StateNone
}

// FramesFromStack converts a DBGp stack.
func FramesFromStack(stack []dbgp.StackFrame) []Frame {
	out := make([]Frame, len(stack))
	for i, f := range stack {
		file := f.File
		if file == "" {
			file = f.FileURI
		}
		out[i] = Frame{Level: f.Level, Where: f.Where, File: file, Line: f.Line}
	}
	return out
}

// VariableFromProperty converts a property and its loaded children.
func VariableFromProperty(p *dbgp.Property) *Variable {
	if p == nil {
		return nil
	}
	v := &Variable{
		Name: p.Name, FullName: p.FullName, Type: p.Type, ClassName: p.ClassName, Value: p.Value,
		HasChildren: p.HasChildren, NumChildren: p.NumChildren, Page: p.Page, PageSize: p.PageSize,
	}
	if v.Name == "" {
		v.Name = p.Key
	}
	for _, c := range p.Children {
		v.Children = append(v.Children, VariableFromProperty(c))
	}
	return v
}

// VariablesFromProperties converts a context listing (context_get).
func VariablesFromProperties(ps []*dbgp.Property) []*Variable {
	out := make([]*Variable, 0, len(ps))
	for _, p := range ps {
		out = append(out, VariableFromProperty(p))
	}
	return out
}

// MergePage appends the children of a further page (property_get with
// page > 0) to v and records the page number.
func (v *Variable) MergePage(page *Variable) {
	if page == nil {
		return
	}
	v.Children = append(v.Children, page.Children...)
	v.Page, v.Loading = page.Page, false
	if page.PageSize > 0 {
		v.PageSize = page.PageSize
	}
}

// Find returns the node with fullName below vs (depth-first), or nil.
func Find(vs []*Variable, fullName string) *Variable {
	for _, v := range vs {
		if v.FullName == fullName {
			return v
		}
		if f := Find(v.Children, fullName); f != nil {
			return f
		}
	}
	return nil
}

// BreakpointFromDBGp converts an engine breakpoint. id overrides the
// engine ID when non-empty (hosts usually keep their own IDs).
func BreakpointFromDBGp(bp dbgp.Breakpoint, id string) Breakpoint {
	if id == "" {
		id = bp.ID
	}
	file := bp.File
	if file == "" {
		file = bp.FileURI
	}
	return Breakpoint{ID: id, File: file, Line: bp.Line, Condition: bp.Condition, Enabled: !bp.Disabled}
}
