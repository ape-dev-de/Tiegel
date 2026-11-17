package docker

import (
	"context"
	"errors"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	dk "github.com/ape-dev-de/tiegel/internal/docker"
)

// ContextsResult is the result of "docker.contexts".
type ContextsResult struct {
	Binary   dk.Binary        `json:"binary"`
	Contexts []dk.ContextInfo `json:"contexts"`
	// Selected is the configured context ("" = docker's current one) and
	// Source where it is configured: project, global or docker.
	Selected string `json:"selected"`
	Source   string `json:"source"`
	// Effective is the context docker will use: Selected, else docker's
	// current context.
	Effective string `json:"effective"`
	// DockerHost is $DOCKER_HOST when set: without a selected context
	// docker talks to it instead of a context endpoint.
	DockerHost string `json:"dockerHost,omitempty"`
}

// StatusParams are the params of "docker.status".
type StatusParams struct {
	// Context to check; empty means the configured/effective one.
	Context string `json:"context,omitempty"`
}

// StatusResult is the result of "docker.status".
type StatusResult struct {
	Binary   dk.Binary `json:"binary"`
	Context  string    `json:"context"`
	Source   string    `json:"source"`
	Endpoint string    `json:"endpoint,omitempty"`
	Provider string    `json:"provider"`
	Remote   bool      `json:"remote,omitempty"`
	// Reachable is true when the engine answered within the timeout.
	Reachable bool            `json:"reachable"`
	Version   *dk.VersionInfo `json:"version,omitempty"`
	// Error explains why the engine (or the binary) is not available.
	Error      string `json:"error,omitempty"`
	DockerHost string `json:"dockerHost,omitempty"`
}

// SetContextParams are the params of "docker.setContext".
type SetContextParams struct {
	// Name of the context; must be listed by docker.contexts.
	Name string `json:"name,omitempty"`
	// Global stores the default for all projects instead of the project
	// override (always global without a project).
	Global bool `json:"global,omitempty"`
	// Clear removes the stored value (project: back to the global default,
	// global: back to docker's current context).
	Clear bool `json:"clear,omitempty"`
}

// SetContextResult is the result of "docker.setContext".
type SetContextResult struct {
	Selected string `json:"selected"`
	Source   string `json:"source"`
	Scope    string `json:"scope"`
}

// SetPathParams are the params of "docker.setPath".
type SetPathParams struct {
	Path  string `json:"path,omitempty"`
	Clear bool   `json:"clear,omitempty"`
}

func (m *Module) unavailable(err error) error {
	if errors.Is(err, dk.ErrNotFound) {
		return api.Errorf(api.CodeUnavailable, "%w", err)
	}
	return api.Errorf(api.CodeFailed, "%w", err)
}

func (m *Module) listContexts(ctx context.Context) (dk.Client, dk.Binary, []dk.ContextInfo, error) {
	c, b, err := m.client(ctx, "")
	if err != nil {
		return c, b, nil, m.unavailable(err)
	}
	list, err := c.Contexts(ctx)
	if err != nil {
		return c, b, nil, api.Errorf(api.CodeUnavailable, "docker contexts: %w", err)
	}
	return c, b, list, nil
}

func (m *Module) contexts(ctx context.Context, _ struct{}) (ContextsResult, error) {
	_, b, list, err := m.listContexts(ctx)
	if err != nil {
		return ContextsResult{}, err
	}
	res := ContextsResult{Binary: b, Contexts: list, DockerHost: m.getenv("DOCKER_HOST")}
	res.Selected, res.Source = m.selectedContext()
	res.Effective = res.Selected
	if res.Effective == "" {
		if cur, ok := dk.CurrentContext(list); ok {
			res.Effective = cur.Name
		}
	}
	return res, nil
}

func (m *Module) status(ctx context.Context, p StatusParams) (StatusResult, error) {
	res := StatusResult{DockerHost: m.getenv("DOCKER_HOST"), Provider: string(dk.ProviderUnknown)}
	res.Context, res.Source = m.selectedContext()
	if p.Context != "" {
		res.Context, res.Source = p.Context, "param"
	}
	c, b, err := m.client(ctx, res.Context)
	res.Binary = b
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	list, lerr := c.Contexts(ctx)
	if lerr == nil {
		info, ok := dk.FindContext(list, res.Context)
		if res.Context == "" {
			info, ok = dk.CurrentContext(list)
		}
		switch {
		case ok:
			res.Context, res.Endpoint, res.Provider, res.Remote = info.Name, info.Endpoint, string(info.Provider), info.Remote
			if res.Context == "default" && res.DockerHost != "" && res.Source == "docker" {
				res.Endpoint, res.Provider, res.Remote = res.DockerHost, string(dk.DetectProvider("", res.DockerHost)), dk.IsRemote(res.DockerHost)
			}
		case res.Context != "":
			res.Error = "unknown Docker context " + res.Context + " (see docker.contexts)"
			return res, nil
		}
	}
	v, err := c.WithContext(res.Context).Ping(ctx, m.timeout())
	if v.Client != "" || v.Server != "" {
		vv := v
		res.Version = &vv
	}
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	res.Reachable = true
	res.Provider = string(dk.ProviderFromVersion(v, dk.Provider(res.Provider)))
	return res, nil
}

func (m *Module) setContext(ctx context.Context, p SetContextParams) (SetContextResult, error) {
	store, scope := m.app.ProjectSettings(), "project"
	if p.Global || store == nil {
		store, scope = m.globalStore(), "global"
	}
	if store == nil {
		return SetContextResult{}, api.Errorf(api.CodeFailed, "no settings store")
	}
	name := strings.TrimSpace(p.Name)
	switch {
	case p.Clear:
		if err := store.Delete(SettingContext); err != nil {
			return SetContextResult{}, api.Errorf(api.CodeFailed, "docker context: %w", err)
		}
	case name == "":
		return SetContextResult{}, api.Errorf(api.CodeInvalidParams, "name is required (or clear)")
	default:
		_, _, list, err := m.listContexts(ctx)
		if err != nil {
			return SetContextResult{}, err
		}
		if _, ok := dk.FindContext(list, name); !ok {
			names := make([]string, len(list))
			for i, c := range list {
				names[i] = c.Name
			}
			return SetContextResult{}, api.Errorf(api.CodeNotFound, "unknown Docker context %q (have %s)", name, strings.Join(names, ", "))
		}
		if err := settings.Set(store, SettingContext, name); err != nil {
			return SetContextResult{}, api.Errorf(api.CodeFailed, "docker context: %w", err)
		}
	}
	res := SetContextResult{Scope: scope}
	res.Selected, res.Source = m.selectedContext()
	return res, nil
}

func (m *Module) setPath(ctx context.Context, p SetPathParams) (dk.Binary, error) {
	g := m.globalStore()
	if g == nil {
		return dk.Binary{}, api.Errorf(api.CodeFailed, "no settings store")
	}
	switch {
	case p.Clear:
		if err := g.Delete(SettingPath); err != nil {
			return dk.Binary{}, api.Errorf(api.CodeFailed, "docker path: %w", err)
		}
	case strings.TrimSpace(p.Path) == "":
		return dk.Binary{}, api.Errorf(api.CodeInvalidParams, "path is required (or clear)")
	default:
		path := strings.TrimSpace(p.Path)
		if _, err := dk.Find(ctx, dk.FindOptions{Configured: path, CommonDirs: []string{}}); err != nil {
			return dk.Binary{}, api.Errorf(api.CodeNotFound, "%w", err)
		}
		if err := settings.Set(g, SettingPath, path); err != nil {
			return dk.Binary{}, api.Errorf(api.CodeFailed, "docker path: %w", err)
		}
	}
	m.mu.Lock()
	m.binValid = false
	m.mu.Unlock()
	b, err := m.binary(ctx)
	if err != nil {
		return b, m.unavailable(err)
	}
	return b, nil
}
