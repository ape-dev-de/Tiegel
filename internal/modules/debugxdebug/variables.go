package debugxdebug

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
)

// SettingWatches stores the watch expressions ([]string).
const SettingWatches = "debug.watches"

// VariablesParams are the params of "debug.variables".
type VariablesParams struct {
	Session string `json:"session,omitempty"`
	// Depth is the stack frame (0 = current).
	Depth int `json:"depth,omitempty"`
	// Context is the context ID (0 locals, 1 superglobals, 2 constants).
	Context int `json:"context,omitempty"`
	// FullName, if set, returns the children of this property (as given in
	// a variable's fullname) instead of the context.
	FullName string `json:"fullname,omitempty"`
	// Page is the child page of FullName.
	Page int `json:"page,omitempty"`
	// MaxData limits value sizes (0 = engine default).
	MaxData int `json:"maxData,omitempty"`
}

// Variable is a variable or property.
type Variable struct {
	Name        string      `json:"name"`
	FullName    string      `json:"fullname,omitempty"`
	Type        string      `json:"type"`
	ClassName   string      `json:"className,omitempty"`
	Value       string      `json:"value,omitempty"`
	Size        int         `json:"size,omitempty"`
	HasChildren bool        `json:"hasChildren,omitempty"`
	NumChildren int         `json:"numChildren,omitempty"`
	Page        int         `json:"page,omitempty"`
	PageSize    int         `json:"pageSize,omitempty"`
	Children    []*Variable `json:"children,omitempty"`
}

// ContextInfo is a variable context of a frame.
type ContextInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// VariablesResult is the result of "debug.variables".
type VariablesResult struct {
	Contexts  []ContextInfo `json:"contexts"`
	Variables []*Variable   `json:"variables"`
	// Parent is the property whose children are listed (with fullname).
	Parent *Variable `json:"parent,omitempty"`
	// HasMore is true if the property has further child pages.
	HasMore bool `json:"hasMore,omitempty"`
}

func toVariable(p *dbgp.Property) *Variable {
	if p == nil {
		return nil
	}
	v := &Variable{
		Name: p.Name, FullName: p.FullName, Type: p.Type, ClassName: p.ClassName, Value: p.Value, Size: p.Size,
		HasChildren: p.HasChildren, NumChildren: p.NumChildren, Page: p.Page, PageSize: p.PageSize,
	}
	for _, c := range p.Children {
		v.Children = append(v.Children, toVariable(c))
	}
	return v
}

func (m *Module) debugVariables(ctx context.Context, p VariablesParams) (VariablesResult, error) {
	s, err := m.session(p.Session)
	if err != nil {
		return VariablesResult{}, err
	}
	res := VariablesResult{Contexts: []ContextInfo{}, Variables: []*Variable{}}
	ctxs, err := s.ContextNames(ctx, p.Depth)
	if err != nil {
		return res, debugErr(err)
	}
	for _, c := range ctxs {
		res.Contexts = append(res.Contexts, ContextInfo{ID: c.ID, Name: c.Name})
	}
	if p.FullName != "" {
		prop, err := s.Property(ctx, dbgp.PropertyRequest{FullName: p.FullName, Depth: p.Depth, Context: p.Context, Page: p.Page, MaxData: p.MaxData})
		if err != nil {
			return res, debugErr(err)
		}
		parent := toVariable(prop)
		res.Variables = parent.Children
		if res.Variables == nil {
			res.Variables = []*Variable{}
		}
		parent.Children = nil
		res.Parent = parent
		res.HasMore = prop.HasMorePages()
		return res, nil
	}
	props, err := s.Variables(ctx, p.Depth, p.Context)
	if err != nil {
		return res, debugErr(err)
	}
	for _, pr := range props {
		res.Variables = append(res.Variables, toVariable(pr))
	}
	return res, nil
}

// WatchesParams are the params of "debug.watches".
type WatchesParams struct {
	Session string `json:"session,omitempty"`
}

// WatchesSetParams are the params of "debug.watches.set".
type WatchesSetParams struct {
	Expressions []string `json:"expressions"`
}

// Watch is an evaluated watch expression.
type Watch struct {
	Expr  string    `json:"expr"`
	Value *Variable `json:"value,omitempty"`
	Error string    `json:"error,omitempty"`
}

func (m *Module) watches() []string {
	w := settings.Get[[]string](m.app.SettingsStore(), SettingWatches, nil)
	if w == nil {
		w = []string{}
	}
	return w
}

// debugWatches evaluates all watch expressions. Without a running session
// the expressions are returned without values.
func (m *Module) debugWatches(ctx context.Context, p WatchesParams) ([]Watch, error) {
	exprs := m.watches()
	out := make([]Watch, 0, len(exprs))
	var s *dbgp.Session
	if p.Session != "" || len(m.sessions()) == 1 {
		var err error
		if s, err = m.session(p.Session); err != nil {
			return nil, err
		}
	}
	for _, e := range exprs {
		w := Watch{Expr: e}
		if s != nil {
			prop, err := s.Eval(ctx, e)
			if err != nil {
				w.Error = err.Error()
			} else {
				w.Value = toVariable(prop)
			}
		}
		out = append(out, w)
	}
	return out, nil
}

func (m *Module) debugWatchesSet(_ context.Context, p WatchesSetParams) ([]string, error) {
	exprs := []string{}
	for _, e := range p.Expressions {
		if e != "" {
			exprs = append(exprs, e)
		}
	}
	if err := settings.Set(m.app.SettingsStore(), SettingWatches, exprs); err != nil {
		return nil, api.Errorf(api.CodeFailed, "saving watches: %w", err)
	}
	return exprs, nil
}
