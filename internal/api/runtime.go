package api

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// settingInterpreter stores the selected interpreter ID (project settings,
// or global settings without a project).
const settingInterpreter = "php.interpreter"

// InterpreterInfo describes an interpreter for "interp.list".
type InterpreterInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Version  string `json:"version,omitempty"`
	Path     string `json:"path,omitempty"`
	Service  string `json:"service,omitempty"`
	Score    int    `json:"score,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

// InterpListParams are the params of "interp.list".
type InterpListParams struct {
	// Compose includes services of the project's compose file (default
	// true; set false to skip loading compose files).
	Compose *bool `json:"compose,omitempty"`
}

// InterpSelectParams are the params of "interp.select".
type InterpSelectParams struct {
	ID string `json:"id"`
}

// InterpVersionParams are the params of "interp.version".
type InterpVersionParams struct {
	// ID of the interpreter; empty means the selected one. "local:<path>"
	// works for any php binary, even if it was not discovered.
	ID      string `json:"id,omitempty"`
	Modules bool   `json:"modules,omitempty"`
	Ini     bool   `json:"ini,omitempty"`
}

// InterpVersionResult is the result of "interp.version".
type InterpVersionResult struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Modules []string          `json:"modules,omitempty"`
	Ini     map[string]string `json:"ini,omitempty"`
}

func (a *App) registerInterp() {
	mustHandle(a, "interp.list", "PHP interpreters: local binaries and compose services", a.interpList)
	mustHandle(a, "interp.select", "Select the project's PHP interpreter", a.interpSelect)
	mustHandle(a, "interp.version", "Query version (and optionally modules/ini) of an interpreter", a.interpVersion)
}

// settingsStore returns the project settings, or the global ones.
func (a *App) settingsStore() *settings.Store {
	if ps := a.ProjectSettings(); ps != nil {
		return ps
	}
	return a.global
}

func (a *App) interpList(ctx context.Context, p InterpListParams) ([]InterpreterInfo, error) {
	selected := settings.Get(a.settingsStore(), settingInterpreter, "")
	found := make(map[string]interp.Interpreter)
	out := []InterpreterInfo{}
	for _, l := range interp.Discover(ctx, a.opts.Interp) {
		found[l.ID()] = l
		out = append(out, InterpreterInfo{ID: l.ID(), Label: l.Label(), Kind: string(l.Kind()), Version: l.PHPVersion, Path: l.Path})
	}
	if root := a.ProjectRoot(); root != "" && (p.Compose == nil || *p.Compose) {
		if proj, err := compose.Load(ctx, root, compose.LoadOptions{Env: a.opts.ComposeEnv}); err == nil {
			for _, c := range compose.Detect(proj) {
				ci := compose.NewInterpreter(proj, c.Service)
				found[ci.ID()] = ci
				out = append(out, InterpreterInfo{ID: ci.ID(), Label: ci.Label(), Kind: string(ci.Kind()), Service: c.Service, Score: c.Score})
			}
		} else if !errors.Is(err, compose.ErrNoComposeFile) {
			a.log.Warn("compose", "err", err)
		}
	}
	for i := range out {
		out[i].Selected = out[i].ID == selected
	}
	a.mu.Lock()
	a.interps = found
	a.mu.Unlock()
	return out, nil
}

// interpreter finds an interpreter by ID in the last listing (listing first
// if needed). "local:<path>" IDs are accepted for any binary.
func (a *App) interpreter(ctx context.Context, id string) (interp.Interpreter, error) {
	a.mu.Lock()
	i, ok := a.interps[id]
	empty := len(a.interps) == 0
	a.mu.Unlock()
	if ok {
		return i, nil
	}
	if path, isLocal := strings.CutPrefix(id, "local:"); isLocal && path != "" {
		if _, err := exec.LookPath(path); err != nil {
			return nil, Errorf(CodeNotFound, "interpreter %s: %w", id, err)
		}
		return interp.NewLocal(path), nil
	}
	if empty {
		if _, err := a.interpList(ctx, InterpListParams{}); err != nil {
			return nil, err
		}
		a.mu.Lock()
		i, ok = a.interps[id]
		a.mu.Unlock()
		if ok {
			return i, nil
		}
	}
	return nil, Errorf(CodeNotFound, "unknown interpreter %q (see interp.list)", id)
}

func (a *App) interpSelect(ctx context.Context, p InterpSelectParams) (InterpreterInfo, error) {
	if p.ID == "" {
		return InterpreterInfo{}, Errorf(CodeInvalidParams, "id is required")
	}
	i, err := a.interpreter(ctx, p.ID)
	if err != nil {
		return InterpreterInfo{}, err
	}
	if err := settings.Set(a.settingsStore(), settingInterpreter, i.ID()); err != nil {
		return InterpreterInfo{}, Errorf(CodeFailed, "saving selection: %w", err)
	}
	info := InterpreterInfo{ID: i.ID(), Label: i.Label(), Kind: string(i.Kind()), Selected: true}
	a.bus.Publish(TopicInterpSelect, info)
	return info, nil
}

func (a *App) interpVersion(ctx context.Context, p InterpVersionParams) (InterpVersionResult, error) {
	id := p.ID
	if id == "" {
		id = settings.Get(a.settingsStore(), settingInterpreter, "")
		if id == "" {
			return InterpVersionResult{}, Errorf(CodeInvalidParams, "no interpreter selected; pass id")
		}
	}
	i, err := a.interpreter(ctx, id)
	if err != nil {
		return InterpVersionResult{}, err
	}
	v, err := i.Version(ctx)
	if err != nil {
		return InterpVersionResult{}, Errorf(CodeUnavailable, "php version: %w", err)
	}
	res := InterpVersionResult{ID: i.ID(), Version: v}
	if p.Modules {
		if res.Modules, err = i.Modules(ctx); err != nil {
			return res, Errorf(CodeUnavailable, "php modules: %w", err)
		}
	}
	if p.Ini {
		ini, err := i.Ini(ctx)
		if err != nil {
			return res, Errorf(CodeUnavailable, "php ini: %w", err)
		}
		res.Ini = ini
	}
	return res, nil
}

// ComposeParams are the params of "compose.detect".
type ComposeParams struct {
	// Dir is the directory with the compose files; empty means the project
	// root.
	Dir      string   `json:"dir,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
}

// HintInfo is a diagnostic about the Xdebug setup.
type HintInfo struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// MappingInfo is a host/container path mapping.
type MappingInfo struct {
	Host   string `json:"host"`
	Remote string `json:"remote"`
}

// CandidateInfo is a compose service that probably runs PHP.
type CandidateInfo struct {
	Service     string        `json:"service"`
	Score       int           `json:"score"`
	Reasons     []string      `json:"reasons"`
	Image       string        `json:"image,omitempty"`
	Mappings    []MappingInfo `json:"mappings"`
	XdebugHints []HintInfo    `json:"xdebugHints"`
}

// ComposeResult is the result of "compose.detect".
type ComposeResult struct {
	Found      bool            `json:"found"`
	Dir        string          `json:"dir"`
	Name       string          `json:"name,omitempty"`
	Files      []string        `json:"files,omitempty"`
	Services   []string        `json:"services,omitempty"`
	Candidates []CandidateInfo `json:"candidates"`
}

func (a *App) registerCompose() {
	mustHandle(a, "compose.detect", "Compose PHP candidates with scores, reasons, path mappings and Xdebug hints", a.composeDetect)
}

func (a *App) composeDetect(ctx context.Context, p ComposeParams) (ComposeResult, error) {
	dir := p.Dir
	if dir == "" {
		dir = a.ProjectRoot()
	}
	if dir == "" {
		return ComposeResult{}, Errorf(CodeNoProject, "no project open and no dir given")
	}
	abs, err := a.resolvePath(dir)
	if err != nil {
		return ComposeResult{}, err
	}
	res := ComposeResult{Dir: abs, Candidates: []CandidateInfo{}}
	proj, err := compose.Load(ctx, abs, compose.LoadOptions{Env: a.opts.ComposeEnv, Profiles: p.Profiles})
	if errors.Is(err, compose.ErrNoComposeFile) {
		return res, nil
	}
	if err != nil {
		return res, Errorf(CodeFailed, "compose: %w", err)
	}
	res.Found = true
	res.Name, res.Files, res.Services = proj.Name, proj.Files, proj.ServiceNames()
	for _, c := range compose.Detect(proj) {
		ci := CandidateInfo{Service: c.Service, Score: c.Score, Reasons: c.Reasons, Image: c.Image, Mappings: []MappingInfo{}, XdebugHints: []HintInfo{}}
		if ci.Reasons == nil {
			ci.Reasons = []string{}
		}
		for _, m := range c.Mappings {
			ci.Mappings = append(ci.Mappings, MappingInfo{Host: m.Host, Remote: m.Remote})
		}
		for _, h := range compose.XdebugHints(proj, c.Service, a.opts.GOOS) {
			ci.XdebugHints = append(ci.XdebugHints, HintInfo{Severity: h.Severity.String(), Code: h.Code, Message: h.Message})
		}
		res.Candidates = append(res.Candidates, ci)
	}
	return res, nil
}

// LSPServersParams are the params of "lsp.servers".
type LSPServersParams struct {
	// All includes proprietary servers (Intelephense).
	All bool `json:"all,omitempty"`
}

// LSPServerInfo is a detected language server.
type LSPServerInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Args        []string `json:"args"`
	License     string   `json:"license"`
	Proprietary bool     `json:"proprietary,omitempty"`
}

func (a *App) registerLSP() {
	mustHandle(a, "lsp.servers", "Installed PHP language servers (no downloads)", a.lspServers)
	mustHandle(a, "lsp.known", "Supported PHP language servers", func(context.Context, struct{}) ([]LSPServerInfo, error) {
		out := []LSPServerInfo{}
		for _, d := range servers.Known() {
			out = append(out, LSPServerInfo{ID: d.ID, Name: d.Name, Args: d.Args, License: d.License, Proprietary: d.Proprietary})
		}
		return out, nil
	})
}

func (a *App) lspServers(_ context.Context, p LSPServersParams) ([]LSPServerInfo, error) {
	opts := a.opts.LSP
	if root := a.ProjectRoot(); root != "" {
		opts.ProjectDir = root
	}
	opts.IncludeProprietary = opts.IncludeProprietary || p.All
	out := []LSPServerInfo{}
	for _, s := range servers.Detect(opts) {
		args := s.Args
		if args == nil {
			args = []string{}
		}
		out = append(out, LSPServerInfo{ID: s.ID, Name: s.Name, Path: s.Path, Args: args, License: s.License, Proprietary: s.Proprietary})
	}
	return out, nil
}
