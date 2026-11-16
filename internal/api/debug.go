package api

import (
	"context"
	"errors"

	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
)

type debugState struct {
	srv *dbgp.Server
}

// DebugListenParams are the params of "debug.listen".
type DebugListenParams struct {
	// Addr is the listen address; empty means 127.0.0.1:9003. Use port 0
	// for a free port.
	Addr string `json:"addr,omitempty"`
	// Mappings translate host paths to engine (container) paths.
	Mappings []MappingInfo `json:"mappings,omitempty"`
}

// DebugListenResult is the result of "debug.listen" and "debug.stop".
type DebugListenResult struct {
	Listening bool   `json:"listening"`
	Addr      string `json:"addr,omitempty"`
}

// DebugSessionInfo describes a debug session. It is also the payload of
// "debug.session.started".
type DebugSessionInfo struct {
	ID              string `json:"id"`
	File            string `json:"file,omitempty"`
	IDEKey          string `json:"ideKey,omitempty"`
	Engine          string `json:"engine,omitempty"`
	EngineVersion   string `json:"engineVersion,omitempty"`
	Language        string `json:"language,omitempty"`
	LanguageVersion string `json:"languageVersion,omitempty"`
	Status          string `json:"status"`
}

// DebugStatusEvent is the payload of "debug.status" and the result of
// "debug.continue".
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

// DebugEndedEvent is the payload of "debug.session.ended".
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
	Session string `json:"session"`
}

// DebugContinueParams are the params of "debug.continue".
type DebugContinueParams struct {
	Session string `json:"session"`
	// Action is one of run, stepInto, stepOver, stepOut, stop, detach.
	Action string `json:"action"`
}

// DebugEvalParams are the params of "debug.eval".
type DebugEvalParams struct {
	Session string `json:"session"`
	Expr    string `json:"expr"`
}

// DebugBreakpointParams are the params of "debug.setBreakpoint".
type DebugBreakpointParams struct {
	Session   string `json:"session"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Condition string `json:"condition,omitempty"`
}

func (a *App) registerDebug() {
	mustHandle(a, "debug.listen", "Start the Xdebug (DBGp) listener; events debug.* follow", a.debugListen)
	mustHandle(a, "debug.stop", "Stop the listener and end all sessions", a.debugStop)
	mustHandle(a, "debug.sessions", "Active debug sessions", a.debugSessions)
	mustHandle(a, "debug.continue", "run/stepInto/stepOver/stepOut/stop/detach a session", a.debugContinue)
	mustHandle(a, "debug.stack", "Call stack of a session", a.debugStack)
	mustHandle(a, "debug.eval", "Evaluate a PHP expression in the current frame", a.debugEval)
	mustHandle(a, "debug.setBreakpoint", "Set a line breakpoint (optionally conditional)", a.debugSetBreakpoint)
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

func (a *App) debugListen(ctx context.Context, p DebugListenParams) (DebugListenResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.debug != nil {
		return DebugListenResult{Listening: true, Addr: a.debug.srv.Addr().String()}, nil
	}
	var mapper dbgp.PathMapper
	if len(p.Mappings) > 0 {
		ms := make([]dbgp.Mapping, 0, len(p.Mappings))
		for _, m := range p.Mappings {
			ms = append(ms, dbgp.Mapping{Host: m.Host, Remote: m.Remote})
		}
		mapper = dbgp.NewPrefixMapper(ms...)
	}
	b := a.bus
	srv := dbgp.NewServer(dbgp.Config{
		Addr:   p.Addr,
		Mapper: mapper,
		Events: dbgp.Events{
			SessionStarted: func(s *dbgp.Session) { b.Publish(TopicDebugStarted, sessionInfo(s)) },
			SessionEnded: func(s *dbgp.Session, err error) {
				ev := DebugEndedEvent{Session: s.ID()}
				if err != nil {
					ev.Error = err.Error()
				}
				b.Publish(TopicDebugEnded, ev)
			},
			StatusChanged: func(s *dbgp.Session, ev dbgp.StatusEvent) {
				b.Publish(TopicDebugStatus, statusEvent(s.ID(), ev, ev.Info.Status))
			},
			BreakpointResolved: func(s *dbgp.Session, bp dbgp.Breakpoint) {
				b.Publish(TopicDebugBreak, DebugBreakpointEvent{Session: s.ID(), Breakpoint: bp})
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
		return DebugListenResult{}, Errorf(CodeUnavailable, "debug listener: %w", err)
	}
	a.debug = &debugState{srv: srv}
	res := DebugListenResult{Listening: true, Addr: srv.Addr().String()}
	b.Publish(TopicDebugListen, res)
	return res, nil
}

func (a *App) stopDebug() error {
	a.mu.Lock()
	d := a.debug
	a.debug = nil
	a.mu.Unlock()
	if d == nil {
		return nil
	}
	err := d.srv.Stop()
	a.bus.Publish(TopicDebugStopped, DebugListenResult{})
	return err
}

func (a *App) debugStop(context.Context, struct{}) (DebugListenResult, error) {
	if err := a.stopDebug(); err != nil {
		return DebugListenResult{}, Errorf(CodeFailed, "debug stop: %w", err)
	}
	return DebugListenResult{}, nil
}

func (a *App) debugSessions(context.Context, struct{}) ([]DebugSessionInfo, error) {
	a.mu.Lock()
	d := a.debug
	a.mu.Unlock()
	out := []DebugSessionInfo{}
	if d == nil {
		return out, nil
	}
	for _, s := range d.srv.Sessions() {
		out = append(out, sessionInfo(s))
	}
	return out, nil
}

func (a *App) session(id string) (*dbgp.Session, error) {
	a.mu.Lock()
	d := a.debug
	a.mu.Unlock()
	if d == nil {
		return nil, Errorf(CodeUnavailable, "debug listener not running (use debug.listen)")
	}
	if id == "" {
		ss := d.srv.Sessions()
		if len(ss) == 1 {
			return ss[0], nil
		}
		return nil, Errorf(CodeInvalidParams, "session is required (%d active sessions)", len(ss))
	}
	s, ok := d.srv.Session(id)
	if !ok {
		return nil, Errorf(CodeNotFound, "no debug session %q", id)
	}
	return s, nil
}

func (a *App) debugContinue(ctx context.Context, p DebugContinueParams) (DebugStatusEvent, error) {
	s, err := a.session(p.Session)
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
		return DebugStatusEvent{}, Errorf(CodeInvalidParams, "unknown action %q (run, stepInto, stepOver, stepOut, stop, detach)", p.Action)
	}
	if err != nil {
		return DebugStatusEvent{}, debugErr(err)
	}
	return statusEvent(s.ID(), dbgp.StatusEvent{Command: p.Action, Info: bi}, bi.Status), nil
}

func debugErr(err error) error {
	if errors.Is(err, dbgp.ErrSessionClosed) {
		return Errorf(CodeNotFound, "debug: %w", err)
	}
	return Errorf(CodeFailed, "debug: %w", err)
}

func (a *App) debugStack(ctx context.Context, p DebugSessionParams) ([]dbgp.StackFrame, error) {
	s, err := a.session(p.Session)
	if err != nil {
		return nil, err
	}
	frames, err := s.Stack(ctx)
	if err != nil {
		return nil, debugErr(err)
	}
	return frames, nil
}

func (a *App) debugEval(ctx context.Context, p DebugEvalParams) (*dbgp.Property, error) {
	s, err := a.session(p.Session)
	if err != nil {
		return nil, err
	}
	prop, err := s.Eval(ctx, p.Expr)
	if err != nil {
		return nil, debugErr(err)
	}
	return prop, nil
}

func (a *App) debugSetBreakpoint(ctx context.Context, p DebugBreakpointParams) (dbgp.Breakpoint, error) {
	s, err := a.session(p.Session)
	if err != nil {
		return dbgp.Breakpoint{}, err
	}
	file, err := a.resolvePath(p.File)
	if err != nil {
		return dbgp.Breakpoint{}, err
	}
	if p.Line < 1 {
		return dbgp.Breakpoint{}, Errorf(CodeInvalidParams, "line must be >= 1")
	}
	bp, err := s.SetBreakpoint(ctx, dbgp.Breakpoint{File: file, Line: p.Line, Condition: p.Condition})
	if err != nil {
		return dbgp.Breakpoint{}, debugErr(err)
	}
	return bp, nil
}

// ModuleInfo describes a module for "modules.list".
type ModuleInfo struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"dependsOn"`
	Enabled   bool     `json:"enabled"`
	Running   bool     `json:"running"`
	Error     string   `json:"error,omitempty"`
}

// ModuleParams address a module.
type ModuleParams struct {
	ID string `json:"id"`
	// WithDependencies enables the transitive dependencies first.
	WithDependencies bool `json:"withDependencies,omitempty"`
}

func (a *App) registerModules() {
	mustHandle(a, "modules.list", "Registered modules with state", a.modulesList)
	mustHandle(a, "modules.enable", "Enable a module (persisted)", a.modulesEnable)
	mustHandle(a, "modules.disable", "Disable a module (persisted)", a.modulesDisable)
}

func (a *App) modulesList(context.Context, struct{}) ([]ModuleInfo, error) {
	out := []ModuleInfo{}
	for _, m := range a.modules.Modules() {
		mi := ModuleInfo{ID: m.ID, DependsOn: m.DependsOn, Enabled: m.Enabled, Running: m.Running}
		if mi.DependsOn == nil {
			mi.DependsOn = []string{}
		}
		if m.Err != nil {
			mi.Error = m.Err.Error()
		}
		out = append(out, mi)
	}
	return out, nil
}

func (a *App) moduleResult(id string) (ModuleInfo, error) {
	list, _ := a.modulesList(context.Background(), struct{}{})
	for _, m := range list {
		if m.ID == id {
			a.bus.Publish(TopicModuleChanged, m)
			return m, nil
		}
	}
	return ModuleInfo{}, Errorf(CodeNotFound, "module %q not registered", id)
}

func moduleErr(err error) error {
	if errors.Is(err, module.ErrUnknown) {
		return Errorf(CodeNotFound, "%w", err)
	}
	return Errorf(CodeFailed, "%w", err)
}

func (a *App) modulesEnable(_ context.Context, p ModuleParams) (ModuleInfo, error) {
	var err error
	if p.WithDependencies {
		err = a.modules.EnableWithDependencies(p.ID)
	} else {
		err = a.modules.Enable(p.ID)
	}
	if err != nil {
		return ModuleInfo{}, moduleErr(err)
	}
	return a.moduleResult(p.ID)
}

func (a *App) modulesDisable(_ context.Context, p ModuleParams) (ModuleInfo, error) {
	if err := a.modules.Disable(p.ID); err != nil {
		return ModuleInfo{}, moduleErr(err)
	}
	return a.moduleResult(p.ID)
}
