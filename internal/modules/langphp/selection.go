package langphp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/lsp"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
)

func secondsDuration(n int) time.Duration { return time.Duration(n) * time.Second }

// lookupOK returns the project value of key, else the global one, and
// where it was found.
func lookupOK[T any](a *api.App, key string) (T, string, bool) {
	var zero T
	if ps := a.ProjectSettings(); ps != nil {
		if v, ok, err := settings.Lookup[T](ps, key); ok && err == nil {
			return v, "project", true
		}
	}
	if v, ok, err := settings.Lookup[T](a.Settings(), key); ok && err == nil {
		return v, "global", true
	}
	return zero, "", false
}

func (m *Module) lookupInt(key string, def int) int {
	if m.app == nil {
		return def
	}
	if v, _, ok := lookupOK[int](m.app, key); ok {
		return v
	}
	return def
}

// selection returns the effective lang-php.server value and where it
// comes from (project, global, default).
func (m *Module) selection() (string, string) {
	for _, k := range []string{SettingServer, legacyServer} {
		if v, src, ok := lookupOK[string](m.app, k); ok && v != "" {
			return v, src
		}
	}
	return SelectAuto, "default"
}

// customCommand returns lang-php.command (or the former lsp.command).
func (m *Module) customCommand() (string, []string) {
	for _, pair := range [][2]string{{SettingCommand, SettingArgs}, {legacyCommand, legacyArgs}} {
		if c, _, ok := lookupOK[string](m.app, pair[0]); ok && c != "" {
			args, _, _ := lookupOK[[]string](m.app, pair[1])
			return c, args
		}
	}
	return "", nil
}

// decls returns the servers of the running lsp-* plugins, by Order.
func (m *Module) decls(ctx context.Context) []api.LSPServerDecl {
	list, err := api.HookLSPServers.Apply(ctx, m.app.Hooks(), nil)
	if err != nil {
		m.app.Logger().Warn("lang-php: lsp.servers hook", "err", err)
	}
	list = slices.Clone(list)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Order < list[j].Order })
	return list
}

// searchDirs returns the directories searched for server executables.
func (m *Module) searchDirs() []string {
	opts := m.app.Options().LSP
	if root := m.app.ProjectRoot(); root != "" {
		opts.ProjectDir = root
	}
	return servers.SearchDirs(opts)
}

// locate finds the executable of d: the configured path, else a search.
func locate(d api.LSPServerDecl, dirs []string) (string, error) {
	if d.Path != "" {
		p, err := servers.CheckExecutable(d.Path, dirs)
		if err != nil {
			return "", fmt.Errorf("configured path (%s.path): %w", d.Plugin, err)
		}
		return p, nil
	}
	if s, ok := servers.Locate(d.Definition, dirs); ok {
		return s.Path, nil
	}
	return "", fmt.Errorf("%s is not installed (looked for %s in %d directories)", d.Name, strings.Join(d.Binaries, ", "), len(dirs))
}

// resolution is the outcome of a server selection.
type resolution struct {
	off      bool
	notFound bool
	reason   string
	target   supervisor.Target
	php      string
	note     string
}

// resolve picks the server for p (explicit parameters, else settings and
// plugins) without starting anything.
func (m *Module) resolve(ctx context.Context, p StartParams) (resolution, error) {
	if p.Server == "" && p.Command == "" {
		p = m.pinnedParams()
	}
	decls := m.decls(ctx)
	byID := map[string]api.LSPServerDecl{}
	var names []string
	for _, d := range decls {
		byID[d.ID] = d
		names = append(names, d.Name)
	}
	dirs := m.searchDirs()
	sel := p.Server
	if p.Command == "" && sel == "" {
		// "none" wins over a custom command: switching off must stop
		// whatever would otherwise run.
		sel, _ = m.selection()
		if sel != SelectNone {
			if c, a := m.customCommand(); c != "" {
				p.Command, p.Args, sel = c, a, ""
			}
		}
	}
	php := m.phpShort(ctx)
	if p.Command != "" {
		id := p.Server
		if id == "" {
			id = "custom"
		}
		d, ok := byID[p.Server]
		if !ok {
			// Unknown server: pass the PHP version the common way.
			d = api.LSPServerDecl{Definition: servers.Definition{ID: id, Name: id, PHPVersionOption: "php.version"}}
		}
		return resolution{target: m.target(d, id, p.Command, p.Args, dirs, php), php: php}, nil
	}
	switch sel {
	case SelectNone:
		return resolution{off: true, reason: "language server switched off (" + SettingServer + " = none); navigation uses the index"}, nil
	case SelectAuto, "":
		var cands []api.LSPServerDecl
		for _, d := range decls {
			if d.AutoSelect {
				cands = append(cands, d)
			}
		}
		if len(cands) == 0 {
			return resolution{notFound: true, target: supervisor.Target{ID: "", Searched: dirs},
				reason: "no PHP language server plugin is enabled (enable one under Plugins, e.g. lsp-phpactor)"}, nil
		}
		for _, d := range cands {
			if path, err := locate(d, dirs); err == nil {
				return resolution{target: m.target(d, d.ID, path, d.EffectiveArgs(), dirs, php), php: php, note: d.Note}, nil
			}
		}
		first := cands[0]
		return resolution{notFound: true, target: m.target(first, first.ID, "", first.EffectiveArgs(), dirs, php),
			reason: fmt.Sprintf("no PHP language server found (enabled: %s); install one or choose its path", strings.Join(names, ", "))}, nil
	}
	d, ok := byID[sel]
	if !ok {
		if !slices.Contains(knownServerIDs, sel) {
			return resolution{}, api.Errorf(api.CodeInvalidParams, "unknown language server %q (see lsp.known)", sel)
		}
		return resolution{notFound: true, target: supervisor.Target{ID: sel, Name: sel, Searched: dirs},
			reason: fmt.Sprintf("plugin lsp-%s is disabled; enable it to use %s", sel, sel)}, nil
	}
	path, err := locate(d, dirs)
	if err != nil {
		return resolution{notFound: true, target: m.target(d, d.ID, "", d.EffectiveArgs(), dirs, php), reason: err.Error(), note: d.Note}, nil
	}
	return resolution{target: m.target(d, d.ID, path, d.EffectiveArgs(), dirs, php), php: php, note: d.Note}, nil
}

// phpShort resolves the project's PHP version ("8.3"; "" unknown).
func (m *Module) phpShort(ctx context.Context) string {
	q, _ := api.HookPHPVersion.Apply(ctx, m.app.Hooks(), api.PHPVersionQuery{})
	if q.Short != "" {
		return q.Short
	}
	return q.Version
}

// target builds the supervisor target of a server.
func (m *Module) target(d api.LSPServerDecl, id, command string, args []string, dirs []string, php string) supervisor.Target {
	if args == nil {
		args = d.EffectiveArgs()
	}
	initOpts, cfgSettings := d.Options(php)
	maps.Copy(initOpts, d.InitOptions)
	name := d.Name
	if name == "" {
		name = id
	}
	return supervisor.Target{
		ID: id, Name: name, Command: command, Args: append([]string(nil), args...), Searched: dirs,
		Config: lsp.Config{
			RootDir:               m.app.ProjectRoot(),
			InitializationOptions: initOpts, Settings: cfgSettings,
			ClientName: "Tiegel", ClientVersion: m.app.Options().AppVersion,
			Logger:      m.app.Logger().With("module", ID),
			OnApplyEdit: m.onApplyEdit,
		},
	}
}

// Apply modes.
type applyMode int

const (
	// applyAuto keeps a server that is already set up for the same target
	// (also a crash-looped one) and starts a changed selection.
	applyAuto applyMode = iota
	// applyExplicit starts the target unless it runs (user action).
	applyExplicit
	// applyForce restarts the target even if it runs.
	applyForce
)

// apply resolves the selection and brings the supervisor there.
func (m *Module) apply(ctx context.Context, p StartParams, mode applyMode) (Status, error) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if mode != applyExplicit && !m.isActive() {
		return m.currentStatus(), nil // stopped meanwhile (lsp.stop, project closed)
	}
	res, err := m.resolve(ctx, p)
	if err != nil {
		return m.currentStatus(), err
	}
	cur := m.sup.Status()
	switch {
	case res.off:
		if cur.State != supervisor.StateOff || cur.Reason != res.reason {
			m.sup.Stop(supervisor.StateOff, res.reason, nil)
		}
		return m.currentStatus(), api.Errorf(api.CodeUnavailable, "%s", res.reason)
	case res.notFound:
		if mode != applyAuto || cur.State != supervisor.StateNotFound || cur.Reason != res.reason {
			m.sup.NotFound(res.target, res.reason)
		}
		return m.currentStatus(), api.Errorf(api.CodeUnavailable, "%s", res.reason)
	}
	if mode == applyAuto && m.sup.Same(res.target) && cur.State != supervisor.StateNotFound && cur.State != supervisor.StateOff {
		return m.currentStatus(), nil // unchanged (running, starting, crashed or given up)
	}
	m.mu.Lock()
	m.phpVersion = res.php
	m.mu.Unlock()
	if res.note != "" {
		m.app.Logger().Info("lang-php: "+res.target.Name, "note", res.note)
	}
	start := m.sup.Start
	if mode == applyForce {
		start = m.sup.Reload
	}
	if err := start(ctx, res.target); err != nil {
		return m.currentStatus(), api.Errorf(api.CodeUnavailable, "starting %s: %w", res.target.Command, err)
	}
	return m.currentStatus(), nil
}

// SelectParams are the params of "lsp.select".
type SelectParams struct {
	// Server is "auto", "none" or a server ID.
	Server string `json:"server"`
	// Scope is "project" (default with an open project) or "global".
	Scope string `json:"scope,omitempty"`
}

// ScopeParams are the params of "lsp.disable".
type ScopeParams struct {
	Scope string `json:"scope,omitempty"`
}

func (m *Module) settingsScope(scope string) (*settings.Store, error) {
	switch scope {
	case "":
		if ps := m.app.ProjectSettings(); ps != nil {
			return ps, nil
		}
		return m.app.Settings(), nil
	case "global":
		return m.app.Settings(), nil
	case "project":
		ps := m.app.ProjectSettings()
		if ps == nil {
			return nil, api.Errorf(api.CodeNoProject, "no project open")
		}
		return ps, nil
	}
	return nil, api.Errorf(api.CodeInvalidParams, "unknown scope %q (project, global)", scope)
}

func (m *Module) lspSelect(ctx context.Context, p SelectParams) (Status, error) {
	ok := p.Server == SelectAuto || p.Server == SelectNone || slices.Contains(knownServerIDs, p.Server)
	for _, d := range m.decls(ctx) {
		ok = ok || d.ID == p.Server
	}
	if !ok {
		return Status{}, api.Errorf(api.CodeInvalidParams, "unknown language server %q (auto, none, %s)", p.Server, strings.Join(knownServerIDs, ", "))
	}
	st, err := m.settingsScope(p.Scope)
	if err != nil {
		return Status{}, err
	}
	if err := settings.Set(st, SettingServer, p.Server); err != nil {
		return Status{}, api.Errorf(api.CodeFailed, "%w", err)
	}
	m.setActive(true)
	m.pin(StartParams{})
	status, err := m.apply(ctx, StartParams{}, applyExplicit)
	if p.Server == SelectNone {
		return status, nil // off is what was asked for
	}
	if err != nil && api.ErrorCode(err) == api.CodeUnavailable {
		return status, nil // selected; the status tells why it does not run
	}
	return status, err
}

// SetPathParams are the params of "lsp.setPath".
type SetPathParams struct {
	// Server is the server ID; Path its executable ("" clears it).
	Server string `json:"server"`
	Path   string `json:"path"`
	Scope  string `json:"scope,omitempty"`
}

// lspSetPath stores the executable of a server in its plugin's setting
// "<plugin>.path" (the plugin reads it, D190), selects the server and
// starts it.
func (m *Module) lspSetPath(ctx context.Context, p SetPathParams) (Status, error) {
	var plugin string
	for _, d := range m.decls(ctx) {
		if d.ID == p.Server {
			plugin = d.Plugin
		}
	}
	if plugin == "" {
		return Status{}, api.Errorf(api.CodeInvalidParams, "no enabled plugin provides language server %q", p.Server)
	}
	st, err := m.settingsScope(p.Scope)
	if err != nil {
		return Status{}, err
	}
	key := plugin + ".path"
	if p.Path == "" {
		err = st.Delete(key)
	} else {
		if _, cerr := servers.CheckExecutable(p.Path, nil); cerr != nil {
			return Status{}, api.Errorf(api.CodeInvalidParams, "%w", cerr)
		}
		err = settings.Set(st, key, p.Path)
	}
	if err != nil {
		return Status{}, api.Errorf(api.CodeFailed, "%w", err)
	}
	return m.lspSelect(ctx, SelectParams{Server: p.Server, Scope: p.Scope})
}

func (m *Module) lspDisable(ctx context.Context, p ScopeParams) (Status, error) {
	return m.lspSelect(ctx, SelectParams{Server: SelectNone, Scope: p.Scope})
}

// LSPServersParams are the params of "lsp.servers".
type LSPServersParams struct {
	// All also lists servers that are not installed.
	All bool `json:"all,omitempty"`
}

// LSPServerInfo is a language server of an enabled plugin.
type LSPServerInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Plugin      string   `json:"plugin,omitempty"`
	Path        string   `json:"path"`
	Args        []string `json:"args"`
	License     string   `json:"license"`
	Proprietary bool     `json:"proprietary,omitempty"`
	// Found tells whether the executable exists; Error why not.
	Found bool   `json:"found"`
	Error string `json:"error,omitempty"`
	// Selected marks the server lang-php.server names (or "auto" picks).
	Selected bool   `json:"selected,omitempty"`
	Note     string `json:"note,omitempty"`
	// AutoSelect tells whether "auto" may pick the server.
	AutoSelect bool `json:"autoSelect"`
}

func (m *Module) serverInfos(ctx context.Context, detect bool) []LSPServerInfo {
	dirs := m.searchDirs()
	sel, _ := m.selection()
	out := []LSPServerInfo{}
	autoPicked := false
	for _, d := range m.decls(ctx) {
		args := d.EffectiveArgs()
		info := LSPServerInfo{ID: d.ID, Name: d.Name, Plugin: d.Plugin, Args: append([]string{}, args...), License: d.License,
			Proprietary: d.Proprietary, Note: d.Note, AutoSelect: d.AutoSelect, Path: d.Path}
		if detect {
			path, err := locate(d, dirs)
			info.Found, info.Path = err == nil, path
			if err != nil {
				info.Error = err.Error()
			}
		}
		switch {
		case sel == d.ID:
			info.Selected = true
		case sel == SelectAuto && d.AutoSelect && info.Found && !autoPicked:
			info.Selected, autoPicked = true, true
		}
		out = append(out, info)
	}
	return out
}

func (m *Module) lspKnown(ctx context.Context, _ struct{}) ([]LSPServerInfo, error) {
	return m.serverInfos(ctx, false), nil
}

func (m *Module) lspServers(ctx context.Context, p LSPServersParams) ([]LSPServerInfo, error) {
	all := m.serverInfos(ctx, true)
	if p.All {
		return all, nil
	}
	out := []LSPServerInfo{}
	for _, s := range all {
		if s.Found {
			out = append(out, s)
		}
	}
	return out, nil
}

// Doctor is the result of "lsp.doctor".
type Doctor struct {
	// Selected is the effective lang-php.server value and Source where it
	// comes from (project, global, default).
	Selected string `json:"selected"`
	Source   string `json:"source"`
	// Command is a custom command overriding the plugins.
	Command string `json:"command,omitempty"`
	// Status is the supervisor state.
	Status Status `json:"status"`
	// Resolved is the server the selection resolves to now ("" if none),
	// Path its executable; Reason why nothing can run.
	Resolved string `json:"resolved,omitempty"`
	Path     string `json:"path,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Servers are the servers of the enabled plugins with findings.
	Servers []LSPServerInfo `json:"servers"`
	// Searched are the directories searched, in order.
	Searched []string `json:"searched"`
	// InstallPlugin is the plugin to offer "Install…" for.
	InstallPlugin string `json:"installPlugin,omitempty"`
}

func (m *Module) lspDoctor(ctx context.Context, _ struct{}) (Doctor, error) {
	sel, src := m.selection()
	cmd, _ := m.customCommand()
	d := Doctor{Selected: sel, Source: src, Command: cmd, Status: m.currentStatus(), Servers: m.serverInfos(ctx, true), Searched: m.searchDirs()}
	res, err := m.resolve(ctx, StartParams{})
	switch {
	case err != nil:
		d.Reason = err.Error()
	case res.off || res.notFound:
		d.Reason = res.reason
		if res.notFound && res.target.ID != "" {
			d.InstallPlugin = "lsp-" + res.target.ID
		}
	default:
		d.Resolved, d.Path = res.target.ID, res.target.Command
	}
	return d, nil
}

// LogParams are the params of "lsp.log".
type LogParams struct {
	// After returns entries with a larger sequence number only.
	After int64 `json:"after,omitempty"`
	// Limit keeps the newest entries (0: all kept).
	Limit int `json:"limit,omitempty"`
}

func (m *Module) lspLog(_ context.Context, p LogParams) ([]supervisor.Entry, error) {
	return m.sup.Log().Since(p.After, p.Limit), nil
}

// TraceParams are the params of "lsp.trace".
type TraceParams struct {
	// On switches the trace; nil only reads it.
	On *bool `json:"on,omitempty"`
}

// TraceResult is the result of "lsp.trace".
type TraceResult struct {
	Trace bool `json:"trace"`
}

func (m *Module) lspTrace(_ context.Context, p TraceParams) (TraceResult, error) {
	if p.On != nil {
		m.sup.SetTrace(*p.On)
	}
	return TraceResult{Trace: m.sup.Status().Trace}, nil
}
