// Package debugxdebug is the debug-xdebug module: a DBGp listener for
// Xdebug with project breakpoints (persistent, conditional, set
// automatically in new sessions), stepping, variables, watches and path
// mapping from php-runtime.
//
// API: debug.listen/stop/sessions, debug.run/stepInto/stepOver/stepOut/
// stopSession/detach (and debug.continue; debug.stop ends the listener), debug.stack, debug.eval,
// debug.variables, debug.watches(.set), debug.setBreakpoint (session only),
// debug.breakpoints.list/set/remove/enable (project).
//
// Events: debug.listening, debug.stopped, debug.sessionStarted,
// debug.sessionEnded, debug.status, debug.break, debug.breakpointHit,
// debug.output, debug.notification, debug.breakpoint.resolved,
// debug.breakpoints.changed.
//
// The path mappings come from the runtime.pathMappings hook (php-runtime)
// unless debug.listen passes explicit ones; runtime.mappingsChanged updates
// a running listener. debug.listen runs the debug.before guard first, on
// which the kernel saves all modified documents.
package debugxdebug

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
)

// ID is the module ID.
const ID = "debug-xdebug"

// Topics of this module (see also api.TopicDebug*).
const (
	TopicDebugListen   = "debug.listening"
	TopicDebugStopped  = "debug.stopped"
	TopicDebugStatus   = "debug.status"
	TopicDebugOutput   = "debug.output"
	TopicDebugNotify   = "debug.notification"
	TopicDebugResolved = "debug.breakpoint.resolved"
)

// Options configure the module.
type Options struct{}

// Module is the debug-xdebug module.
type Module struct {
	opts Options
	app  *api.App

	mu    sync.Mutex
	debug *debugState
	// engineIDs maps session ID → project breakpoint ID → engine ID.
	engineIDs map[string]map[string]string
	bpMu      sync.Mutex // serialises breakpoint list changes
}

type debugState struct {
	srv *dbgp.Server
	// explicit is true if debug.listen got its own mappings; then
	// runtime.mappingsChanged is ignored.
	explicit bool
}

// New creates the module.
func New(opts Options) *Module { return &Module{opts: opts} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return []string{"php-runtime"} }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"debug.*"} }

// SettingPort is the loopback port debug.listen uses without an address.
const SettingPort = ID + ".port"

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingPort, Kind: settings.KindInt, Default: 9003, Min: settings.IntPtr(1), Max: settings.IntPtr(65535), Scope: settings.ScopeProject,
			Label: "Debug port", Help: "Port of the local listener for Xdebug connections (xdebug.client_port); bound to 127.0.0.1 only."},
	}
}

// Start registers the debug namespace.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("debug-xdebug: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.engineIDs = map[string]map[string]string{}
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "debug.listen", "Start the Xdebug (DBGp) listener (debug.before guard saves documents); events debug.* follow", m.debugListen)
	api.Add(p, "debug.stop", "Stop the listener and end all sessions", m.debugStop)
	api.Add(p, "debug.sessions", "Active debug sessions", m.debugSessions)
	api.Add(p, "debug.continue", "run/stepInto/stepOver/stepOut/stop/detach a session", m.debugContinue)
	// debug.stop stops the listener; ending the script is debug.stopSession.
	for method, action := range map[string]string{
		"run": "run", "stepInto": "stepInto", "stepOver": "stepOver", "stepOut": "stepOut",
		"stopSession": "stop", "detach": "detach",
	} {
		action := action
		api.Add(p, "debug."+method, "Debugger: "+action+" (session optional with one active session)", func(ctx context.Context, sp DebugSessionParams) (DebugStatusEvent, error) {
			return m.debugContinue(ctx, DebugContinueParams{Session: sp.Session, Action: action})
		})
	}
	api.Add(p, "debug.stack", "Call stack of a session", m.debugStack)
	api.Add(p, "debug.eval", "Evaluate a PHP expression in the current frame", m.debugEval)
	api.Add(p, "debug.variables", "Variables of a context (or children of a property via fullname), paged", m.debugVariables)
	api.Add(p, "debug.watches", "Evaluate the watch expressions in the current frame", m.debugWatches)
	api.Add(p, "debug.watches.set", "Replace the watch expressions (persisted per project)", m.debugWatchesSet)
	api.Add(p, "debug.setBreakpoint", "Set a line breakpoint in a session only (optionally conditional)", m.debugSetBreakpoint)
	api.Add(p, "debug.breakpoints.list", "Project breakpoints (persisted, also without a session)", m.bpList)
	api.Add(p, "debug.breakpoints.set", "Add or update a project breakpoint (file, line, condition, enabled)", m.bpSet)
	api.Add(p, "debug.breakpoints.remove", "Remove a project breakpoint (file+line or id)", m.bpRemove)
	api.Add(p, "debug.breakpoints.enable", "Enable or disable a project breakpoint", m.bpEnable)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicMappingsChanged, m.onMappingsChanged))
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectOpened, func(bus.Event) {
		app.Bus().Publish(api.TopicDebugBreakpoints, m.load())
	}))
	return nil
}

// Stop ends the listener.
func (m *Module) Stop() error { return m.stopDebug() }

// DebugListenParams are the params of "debug.listen".
type DebugListenParams struct {
	// Addr is the listen address; empty means 127.0.0.1 with the port of
	// the setting debug-xdebug.port (default 9003). Use port 0 for a free
	// port.
	Addr string `json:"addr,omitempty"`
	// Mappings translate host paths to engine (container) paths. Without
	// them the mappings of php-runtime (runtime.mappings.get) are used.
	Mappings []api.MappingInfo `json:"mappings,omitempty"`
}

// DebugListenResult is the result of "debug.listen" and "debug.stop".
type DebugListenResult struct {
	Listening bool              `json:"listening"`
	Addr      string            `json:"addr,omitempty"`
	Mappings  []api.MappingInfo `json:"mappings,omitempty"`
}

// DebugSessionInfo describes a debug session. It is also the payload of
// "debug.sessionStarted".
type DebugSessionInfo struct {
	ID              string `json:"id"`
	File            string `json:"file,omitempty"`
	IDEKey          string `json:"ideKey,omitempty"`
	Engine          string `json:"engine,omitempty"`
	EngineVersion   string `json:"engineVersion,omitempty"`
	Language        string `json:"language,omitempty"`
	LanguageVersion string `json:"languageVersion,omitempty"`
	Status          string `json:"status"`
	// Breakpoints is the number of project breakpoints set at start.
	Breakpoints int `json:"breakpoints,omitempty"`
}

// DebugStatusEvent is the payload of "debug.status" and the result of the
// stepping methods.
type DebugStatusEvent struct {
	Session   string `json:"session"`
	Previous  string `json:"previous,omitempty"`
	Status    string `json:"status"`
	Command   string `json:"command,omitempty"`
	Reason    string `json:"reason,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Exception string `json:"exception,omitempty"`
	Message   string `json:"message,omitempty"`
}

// DebugEndedEvent is the payload of "debug.sessionEnded".
type DebugEndedEvent struct {
	Session string `json:"session"`
	Error   string `json:"error,omitempty"`
}

// DebugOutputEvent is the payload of "debug.output".
type DebugOutputEvent struct {
	Session string `json:"session"`
	Stream  string `json:"stream"`
	Data    string `json:"data"`
}

// DebugNotificationEvent is the payload of "debug.notification".
type DebugNotificationEvent struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

// DebugBreakpointEvent is the payload of "debug.breakpoint.resolved".
type DebugBreakpointEvent struct {
	Session    string          `json:"session"`
	Breakpoint dbgp.Breakpoint `json:"breakpoint"`
}

// DebugSessionParams address a session.
type DebugSessionParams struct {
	Session string `json:"session,omitempty"`
}

// DebugContinueParams are the params of "debug.continue".
type DebugContinueParams struct {
	Session string `json:"session,omitempty"`
	// Action is one of run, stepInto, stepOver, stepOut, stop, detach.
	Action string `json:"action"`
}

// DebugEvalParams are the params of "debug.eval".
type DebugEvalParams struct {
	Session string `json:"session,omitempty"`
	Expr    string `json:"expr"`
}

// DebugBreakpointParams are the params of "debug.setBreakpoint".
type DebugBreakpointParams struct {
	Session   string `json:"session,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Condition string `json:"condition,omitempty"`
}

func sessionInfo(s *dbgp.Session) DebugSessionInfo {
	in := s.Info()
	return DebugSessionInfo{
		ID: s.ID(), File: in.File, IDEKey: in.IDEKey, Engine: in.Engine, EngineVersion: in.EngineVersion,
		Language: in.Language, LanguageVersion: in.LanguageVersion, Status: string(s.CurrentStatus()),
	}
}

func statusEvent(id string, ev dbgp.StatusEvent, status dbgp.Status) DebugStatusEvent {
	return DebugStatusEvent{
		Session: id, Previous: string(ev.Previous), Status: string(status), Command: ev.Command,
		Reason: ev.Info.Reason, File: ev.Info.File, Line: ev.Info.Line, Exception: ev.Info.Exception, Message: ev.Info.Message,
	}
}

func mapperOf(ms []api.MappingInfo) dbgp.PathMapper {
	if len(ms) == 0 {
		return nil
	}
	out := make([]dbgp.Mapping, 0, len(ms))
	for _, mp := range ms {
		out = append(out, dbgp.Mapping{Host: mp.Host, Remote: mp.Remote})
	}
	return dbgp.NewPrefixMapper(out...)
}

func (m *Module) debugListen(ctx context.Context, p DebugListenParams) (DebugListenResult, error) {
	m.mu.Lock()
	if d := m.debug; d != nil {
		m.mu.Unlock()
		return DebugListenResult{Listening: true, Addr: d.srv.Addr().String()}, nil
	}
	m.mu.Unlock()
	if err := api.HookDebugBefore.Check(ctx, m.app.Hooks(), api.DebugRequest{Addr: p.Addr}); err != nil {
		return DebugListenResult{}, api.Errorf(api.CodeFailed, "debug.before: %w", err)
	}
	mappings, explicit := p.Mappings, len(p.Mappings) > 0
	if !explicit {
		q, _ := api.HookPathMappings.Apply(ctx, m.app.Hooks(), api.PathMappingsQuery{})
		mappings = q.Mappings
	}
	addr := p.Addr
	if addr == "" {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(api.Setting(m.app, SettingPort, 9003)))
	}
	b := m.app.Bus()
	srv := dbgp.NewServer(dbgp.Config{
		Addr:   addr,
		Mapper: mapperOf(mappings),
		Events: dbgp.Events{
			SessionStarted: m.sessionStarted,
			SessionEnded: func(s *dbgp.Session, err error) {
				m.mu.Lock()
				delete(m.engineIDs, s.ID())
				m.mu.Unlock()
				ev := DebugEndedEvent{Session: s.ID()}
				if err != nil {
					ev.Error = err.Error()
				}
				b.Publish(api.TopicDebugSessionEnded, ev)
			},
			StatusChanged: func(s *dbgp.Session, ev dbgp.StatusEvent) {
				b.Publish(TopicDebugStatus, statusEvent(s.ID(), ev, ev.Info.Status))
				if ev.Info.Status == dbgp.StatusBreak {
					m.publishBreak(s.ID(), ev)
				}
			},
			BreakpointResolved: func(s *dbgp.Session, bp dbgp.Breakpoint) {
				b.Publish(TopicDebugResolved, DebugBreakpointEvent{Session: s.ID(), Breakpoint: bp})
			},
			Notification: func(s *dbgp.Session, n dbgp.Notification) {
				b.Publish(TopicDebugNotify, DebugNotificationEvent{Session: s.ID(), Name: n.Name, File: n.File, Line: n.Line, Type: n.Type, Message: n.Message})
			},
			Output: func(s *dbgp.Session, st dbgp.Stream) {
				b.Publish(TopicDebugOutput, DebugOutputEvent{Session: s.ID(), Stream: st.Type, Data: st.Data})
			},
		},
	})
	if err := srv.Start(ctx); err != nil {
		return DebugListenResult{}, api.Errorf(api.CodeUnavailable, "debug listener: %w", err)
	}
	m.mu.Lock()
	if d := m.debug; d != nil { // lost a race with a concurrent listen
		m.mu.Unlock()
		_ = srv.Stop()
		return DebugListenResult{Listening: true, Addr: d.srv.Addr().String()}, nil
	}
	m.debug = &debugState{srv: srv, explicit: explicit}
	m.mu.Unlock()
	res := DebugListenResult{Listening: true, Addr: srv.Addr().String(), Mappings: mappings}
	b.Publish(TopicDebugListen, res)
	return res, nil
}

// sessionStarted runs in the session's event queue before any status
// event: the engine waits in "starting" until the first continuation
// command, so the project breakpoints are in place before the script runs.
func (m *Module) sessionStarted(s *dbgp.Session) {
	n := m.applyAll(context.Background(), s)
	info := sessionInfo(s)
	info.Breakpoints = n
	m.app.Bus().Publish(api.TopicDebugSessionStarted, info)
}

func (m *Module) onMappingsChanged(e bus.Event) {
	ev, ok := e.Payload.(api.MappingsChangedEvent)
	if !ok {
		return
	}
	m.mu.Lock()
	d := m.debug
	m.mu.Unlock()
	if d != nil && !d.explicit {
		d.srv.SetMapper(mapperOf(ev.Mappings))
	}
}

func (m *Module) publishBreak(id string, ev dbgp.StatusEvent) {
	bi := ev.Info
	be := api.DebugBreakEvent{
		Session: id, File: bi.File, RemoteURI: bi.FileURI, Line: bi.Line, Reason: bi.Reason,
		Command: ev.Command, Exception: bi.Exception, Message: bi.Message,
	}
	if bi.File != "" {
		be.Path = m.app.DisplayPath(bi.File)
	}
	b := m.app.Bus()
	b.Publish(api.TopicDebugBreak, be)
	if bi.File == "" || bi.Line <= 0 {
		return
	}
	for _, bp := range m.load() {
		if bp.Enabled && bp.Line == bi.Line && m.abs(bp.Path) == bi.File {
			b.Publish(api.TopicDebugBreakpointHit, api.BreakpointHitEvent{DebugBreakEvent: be, Breakpoint: bp.ID, Condition: bp.Condition})
			return
		}
	}
}

func (m *Module) stopDebug() error {
	m.mu.Lock()
	d := m.debug
	m.debug = nil
	m.mu.Unlock()
	if d == nil {
		return nil
	}
	err := d.srv.Stop()
	m.app.Bus().Publish(TopicDebugStopped, DebugListenResult{})
	return err
}

func (m *Module) debugStop(context.Context, struct{}) (DebugListenResult, error) {
	if err := m.stopDebug(); err != nil {
		return DebugListenResult{}, api.Errorf(api.CodeFailed, "debug stop: %w", err)
	}
	return DebugListenResult{}, nil
}

func (m *Module) sessions() []*dbgp.Session {
	m.mu.Lock()
	d := m.debug
	m.mu.Unlock()
	if d == nil {
		return nil
	}
	return d.srv.Sessions()
}

func (m *Module) debugSessions(context.Context, struct{}) ([]DebugSessionInfo, error) {
	out := []DebugSessionInfo{}
	for _, s := range m.sessions() {
		out = append(out, sessionInfo(s))
	}
	return out, nil
}

func (m *Module) session(id string) (*dbgp.Session, error) {
	m.mu.Lock()
	d := m.debug
	m.mu.Unlock()
	if d == nil {
		return nil, api.Errorf(api.CodeUnavailable, "debug listener not running (use debug.listen)")
	}
	if id == "" {
		ss := d.srv.Sessions()
		if len(ss) == 1 {
			return ss[0], nil
		}
		return nil, api.Errorf(api.CodeInvalidParams, "session is required (%d active sessions)", len(ss))
	}
	s, ok := d.srv.Session(id)
	if !ok {
		return nil, api.Errorf(api.CodeNotFound, "no debug session %q", id)
	}
	return s, nil
}

func (m *Module) debugContinue(ctx context.Context, p DebugContinueParams) (DebugStatusEvent, error) {
	s, err := m.session(p.Session)
	if err != nil {
		return DebugStatusEvent{}, err
	}
	var bi dbgp.BreakInfo
	switch p.Action {
	case "run":
		bi, err = s.Run(ctx)
	case "stepInto":
		bi, err = s.StepInto(ctx)
	case "stepOver":
		bi, err = s.StepOver(ctx)
	case "stepOut":
		bi, err = s.StepOut(ctx)
	case "stop":
		bi, err = s.Stop(ctx)
	case "detach":
		err = s.Detach(ctx)
		bi.Status = s.CurrentStatus()
	default:
		return DebugStatusEvent{}, api.Errorf(api.CodeInvalidParams, "unknown action %q (run, stepInto, stepOver, stepOut, stop, detach)", p.Action)
	}
	if err != nil {
		return DebugStatusEvent{}, debugErr(err)
	}
	return statusEvent(s.ID(), dbgp.StatusEvent{Command: p.Action, Info: bi}, bi.Status), nil
}

func debugErr(err error) error {
	if errors.Is(err, dbgp.ErrSessionClosed) {
		return api.Errorf(api.CodeNotFound, "debug: %w", err)
	}
	return api.Errorf(api.CodeFailed, "debug: %w", err)
}

func (m *Module) debugStack(ctx context.Context, p DebugSessionParams) ([]dbgp.StackFrame, error) {
	s, err := m.session(p.Session)
	if err != nil {
		return nil, err
	}
	frames, err := s.Stack(ctx)
	if err != nil {
		return nil, debugErr(err)
	}
	return frames, nil
}

func (m *Module) debugEval(ctx context.Context, p DebugEvalParams) (*dbgp.Property, error) {
	s, err := m.session(p.Session)
	if err != nil {
		return nil, err
	}
	prop, err := s.Eval(ctx, p.Expr)
	if err != nil {
		return nil, debugErr(err)
	}
	return prop, nil
}

func (m *Module) debugSetBreakpoint(ctx context.Context, p DebugBreakpointParams) (dbgp.Breakpoint, error) {
	s, err := m.session(p.Session)
	if err != nil {
		return dbgp.Breakpoint{}, err
	}
	file, err := m.app.ResolvePath(p.File)
	if err != nil {
		return dbgp.Breakpoint{}, err
	}
	if p.Line < 1 {
		return dbgp.Breakpoint{}, api.Errorf(api.CodeInvalidParams, "line must be >= 1")
	}
	bp, err := s.SetBreakpoint(ctx, dbgp.Breakpoint{File: file, Line: p.Line, Condition: p.Condition})
	if err != nil {
		return dbgp.Breakpoint{}, debugErr(err)
	}
	return bp, nil
}
