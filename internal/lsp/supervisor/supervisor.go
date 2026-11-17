// Package supervisor runs one language server natively (no container,
// decision D192) and keeps it alive: it checks the executable before the
// start, waits for the initialize handshake, watches the process (exit and
// hung requests), restarts it with exponential backoff and gives up after
// a number of crashes in a row instead of looping. Every state change is
// reported through Options.OnState; stderr, supervisor events and the
// optional JSON-RPC trace go to a bounded Log.
//
// The package is a backend package: it imports neither Gio nor the API
// layer. Module lang-php owns a Supervisor and publishes its states as
// lsp.state events.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// State is the supervisor state.
type State string

// States.
const (
	// StateOff: no language server is wanted (setting "none", nothing
	// selected yet, or the project was closed).
	StateOff State = "off"
	// StateNotFound: the selected server is not installed or not
	// executable; Status.Reason and Status.Searched explain why.
	StateNotFound State = "not-found"
	// StateStarting: the process runs, the initialize handshake is in
	// progress.
	StateStarting State = "starting"
	// StateRunning: the server is initialized and answers.
	StateRunning State = "running"
	// StateCrashed: the server died or hung; a restart is scheduled.
	StateCrashed State = "crashed"
	// StateStopped: the server was stopped on request or after too many
	// crashes in a row (Status.Reason); only an explicit start or restart
	// brings it back.
	StateStopped State = "stopped"
)

// Target is the server to run.
type Target struct {
	// ID and Name identify the server ("phpactor", "Phpactor").
	ID   string
	Name string
	// Command is the executable (absolute path or a name looked up in
	// PATH); Args its arguments.
	Command string
	Args    []string
	// Searched are the directories that were searched for Command (shown
	// when it cannot be started).
	Searched []string
	// Config is the client configuration template. Command and Args are
	// taken from the Target; the supervisor installs its own Stderr,
	// Trace, OnTimeout and OnDiagnostics callbacks.
	Config lsp.Config
}

// key identifies a target for "already running" checks.
func (t Target) key() string {
	return strings.Join(append([]string{t.ID, t.Command, t.Config.RootDir}, t.Args...), "\x00")
}

// Status is a snapshot of the supervisor.
type Status struct {
	State   State    `json:"state"`
	Server  string   `json:"server,omitempty"`
	Name    string   `json:"name,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	PID     int      `json:"pid,omitempty"`
	// Reason explains not-found, crashed and stopped states.
	Reason string `json:"reason,omitempty"`
	// Searched lists the directories searched for the executable
	// (not-found).
	Searched []string `json:"searched,omitempty"`
	// Crashes counts crashes in a row; Restarts automatic restarts since
	// the last explicit start.
	Crashes  int `json:"crashes"`
	Restarts int `json:"restarts"`
	// Generation counts server starts.
	Generation int `json:"generation"`
	// ServerInfo is the name/version the server reports.
	ServerInfo string `json:"serverInfo,omitempty"`
	// Since is the time of the last state change.
	Since time.Time `json:"since"`
	// RetryAt is the time of the scheduled restart (crashed).
	RetryAt *time.Time `json:"retryAt,omitempty"`
	// Trace tells whether the JSON-RPC trace is written to the log.
	Trace bool `json:"trace"`
}

// Instance is one started server process.
type Instance struct {
	Target Target
	// Gen is the generation (Status.Generation) of this start.
	Gen    int
	client atomic.Pointer[lsp.Client]
	// closing marks an intentional stop (no crash handling).
	closing atomic.Bool
	// hung records why the supervisor killed the server.
	hung    atomic.Value // string
	started time.Time
}

// Client returns the LSP client (nil until initialize completed).
func (i *Instance) Client() *lsp.Client { return i.client.Load() }

// Closed reports whether the instance was stopped intentionally.
func (i *Instance) Closed() bool { return i.closing.Load() }

// Options configure a Supervisor. Zero durations take the defaults.
type Options struct {
	// Connect starts and initializes a server; nil means lsp.Start.
	Connect func(ctx context.Context, cfg lsp.Config) (*lsp.Client, error)
	// Check verifies the executable before a start and returns its
	// absolute path. Nil checks presence and execute permission (with
	// Connect == nil only; custom connectors skip the check).
	Check func(command string, searched []string) (string, error)
	// InitTimeout bounds the initialize handshake (default 30s).
	InitTimeout time.Duration
	// RequestTimeout bounds every request (default 30s, negative: off).
	RequestTimeout time.Duration
	// HungLimit is the number of timed out requests in a row after which
	// the server counts as hung and is restarted (default 3).
	HungLimit int
	// MaxCrashes is the number of crashes in a row after which the
	// supervisor stops restarting (default 5).
	MaxCrashes int
	// Backoff returns the delay before the n-th restart in a row
	// (n >= 1). Default: 500ms doubled per crash, at most 30s.
	Backoff func(n int) time.Duration
	// StableAfter is the run time after which a crash no longer counts as
	// "in a row" (default 2m).
	StableAfter time.Duration
	// ShutdownTimeout bounds the shutdown/exit handshake (default 5s).
	ShutdownTimeout time.Duration
	// OnState is called after every state change, in order, from the
	// goroutine that caused it. It must not call Start, Stop, Restart or
	// Close synchronously.
	OnState func(Status)
	// OnReady is called after a (re)start completed initialize, before
	// the running state is reported; lang-php re-synchronizes the open
	// documents here. Same restrictions as OnState.
	OnReady func(*Instance)
	// OnDiagnostics receives publishDiagnostics of the current instance.
	OnDiagnostics func(*Instance, protocol.PublishDiagnosticsParams)
	// Log receives stderr lines, events and the trace (nil: a new log
	// with 2000 entries).
	Log *Log
	// Logger receives warnings (nil: discarded).
	Logger *slog.Logger
}

// Supervisor runs at most one language server.
type Supervisor struct {
	opts Options
	log  *Log

	// opMu serialises lifecycle operations (start, stop, restart).
	opMu sync.Mutex
	// pubMu keeps OnState calls in order.
	pubMu sync.Mutex

	mu          sync.Mutex
	st          Status
	target      *Target
	inst        *Instance
	epoch       int // bumped by explicit operations; cancels scheduled restarts
	timer       *time.Timer
	startCancel context.CancelFunc
	closed      bool
	trace       atomic.Bool
}

// ErrClosed is returned after Close.
var ErrClosed = errors.New("supervisor: closed")

// New returns a supervisor in state off.
func New(opts Options) *Supervisor {
	if opts.InitTimeout <= 0 {
		opts.InitTimeout = 30 * time.Second
	}
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 30 * time.Second
	}
	if opts.HungLimit <= 0 {
		opts.HungLimit = 3
	}
	if opts.MaxCrashes <= 0 {
		opts.MaxCrashes = 5
	}
	if opts.Backoff == nil {
		opts.Backoff = DefaultBackoff
	}
	if opts.StableAfter <= 0 {
		opts.StableAfter = 2 * time.Minute
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	l := opts.Log
	if l == nil {
		l = NewLog(2000, nil)
	}
	s := &Supervisor{opts: opts, log: l}
	s.st = Status{State: StateOff, Since: time.Now()}
	return s
}

// DefaultBackoff is 500ms doubled per crash in a row, capped at 30s.
func DefaultBackoff(n int) time.Duration {
	d := 500 * time.Millisecond
	for i := 1; i < n && d < 30*time.Second; i++ {
		d *= 2
	}
	return min(d, 30*time.Second)
}

// Log returns the server log.
func (s *Supervisor) Log() *Log { return s.log }

// SetTrace switches the JSON-RPC trace into the log on or off. It applies
// to the running server immediately.
func (s *Supervisor) SetTrace(on bool) {
	s.trace.Store(on)
	s.mu.Lock()
	s.st.Trace = on
	s.mu.Unlock()
}

// Status returns the current status.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Supervisor) statusLocked() Status {
	st := s.st
	st.Args = append([]string(nil), st.Args...)
	st.Searched = append([]string(nil), st.Searched...)
	st.Trace = s.trace.Load()
	if s.inst != nil {
		if c := s.inst.Client(); c != nil {
			st.PID = c.PID()
			if si := c.ServerInfo(); si != nil {
				st.ServerInfo = strings.TrimSpace(si.Name + " " + si.Version)
			}
		}
	}
	return st
}

// Current returns the running instance, or nil unless the state is
// running.
func (s *Supervisor) Current() *Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.State != StateRunning || s.inst == nil {
		return nil
	}
	return s.inst
}

// Target returns the current target (the server that runs, starts, or
// waits for a restart).
func (s *Supervisor) Target() (Target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.target == nil {
		return Target{}, false
	}
	return *s.target, true
}

// setState changes the state and reports it. Called with mu held; the
// callback runs after mu is released (see publish).
func (s *Supervisor) setStateLocked(state State, reason string) {
	s.st.State, s.st.Reason, s.st.Since = state, reason, time.Now()
	if state != StateCrashed {
		s.st.RetryAt = nil
	}
	if state != StateNotFound {
		s.st.Searched = nil
	}
}

func (s *Supervisor) publish() {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	st := s.Status()
	if s.opts.OnState != nil {
		s.opts.OnState(st)
	}
}

func (s *Supervisor) event(server, format string, args ...any) {
	s.log.Append(KindEvent, server, fmt.Sprintf(format, args...))
}

// Start starts t, replacing (shutting down) a different running server.
// Starting the target that already runs or starts is a no-op. An explicit
// start resets the crash counter. If the executable is missing the state
// becomes not-found; if initialize fails it counts as crash (restart with
// backoff). The error describes the failure in both cases.
func (s *Supervisor) Start(ctx context.Context, t Target) error { return s.start(ctx, t, false) }

// Reload starts t like Start but also restarts it when it already runs
// (e.g. because its initialization options changed).
func (s *Supervisor) Reload(ctx context.Context, t Target) error { return s.start(ctx, t, true) }

// Same reports whether t is the current target (same server, command,
// arguments and root).
func (s *Supervisor) Same(t Target) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target != nil && s.target.key() == t.key()
}

func (s *Supervisor) start(ctx context.Context, t Target, force bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if !force && s.target != nil && s.target.key() == t.key() && (s.st.State == StateRunning || s.st.State == StateStarting) {
		s.mu.Unlock()
		return nil
	}
	s.cancelPendingLocked()
	s.mu.Unlock()

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if !force && s.target != nil && s.target.key() == t.key() && s.st.State == StateRunning {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	s.shutdownLocked()
	s.mu.Lock()
	s.epoch++
	s.st.Crashes, s.st.Restarts = 0, 0
	tt := t
	s.target = &tt
	epoch := s.epoch
	s.mu.Unlock()
	return s.launch(ctx, epoch)
}

// Restart restarts the current target (also after a crash loop) and
// resets the crash counter. Without a target it returns an error.
func (s *Supervisor) Restart(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.target == nil {
		s.mu.Unlock()
		return errors.New("supervisor: no language server selected")
	}
	s.cancelPendingLocked()
	s.mu.Unlock()

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.shutdownLocked()
	s.mu.Lock()
	if s.target == nil { // stopped meanwhile
		s.mu.Unlock()
		return errors.New("supervisor: no language server selected")
	}
	s.epoch++
	s.st.Crashes, s.st.Restarts = 0, 0
	epoch := s.epoch
	s.mu.Unlock()
	return s.launch(ctx, epoch)
}

// Stop shuts the server down cleanly (shutdown/exit, kill after the
// timeout) and enters state, which must be StateOff, StateStopped or
// StateNotFound; reason and searched are reported with it. A scheduled
// restart is cancelled; a start in progress is aborted.
func (s *Supervisor) Stop(state State, reason string, searched []string) {
	s.mu.Lock()
	s.cancelPendingLocked()
	s.mu.Unlock()

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.shutdownLocked()
	s.mu.Lock()
	s.epoch++
	if s.target != nil {
		s.st.Server, s.st.Name = s.target.ID, s.target.Name
	}
	if state != StateStopped {
		s.target = nil
		s.st.Server, s.st.Name, s.st.Command, s.st.Args = "", "", "", nil
	}
	s.setStateLocked(state, reason)
	s.st.Searched = append([]string(nil), searched...)
	s.mu.Unlock()
	s.event("", "state %s: %s", state, reason)
	s.publish()
}

// NotFound records that target t cannot be started (missing executable,
// disabled plugin, …) without trying: the state becomes not-found with
// reason and the searched directories.
func (s *Supervisor) NotFound(t Target, reason string) {
	s.mu.Lock()
	s.cancelPendingLocked()
	s.mu.Unlock()

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.shutdownLocked()
	s.mu.Lock()
	s.epoch++
	tt := t
	s.target = &tt
	s.st.Server, s.st.Name, s.st.Command, s.st.Args = t.ID, t.Name, t.Command, append([]string(nil), t.Args...)
	s.setStateLocked(StateNotFound, reason)
	s.st.Searched = append([]string(nil), t.Searched...)
	s.mu.Unlock()
	s.event(t.ID, "not found: %s", reason)
	s.publish()
}

// Close stops the server and refuses further starts.
func (s *Supervisor) Close() {
	s.Stop(StateOff, "closed", nil)
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// cancelPendingLocked cancels a scheduled restart and aborts a start in
// progress (mu held).
func (s *Supervisor) cancelPendingLocked() {
	s.epoch++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.startCancel != nil {
		s.startCancel()
	}
}

// shutdownLocked stops the current instance cleanly (opMu held).
func (s *Supervisor) shutdownLocked() {
	s.mu.Lock()
	inst := s.inst
	s.inst = nil
	s.mu.Unlock()
	if inst == nil {
		return
	}
	inst.closing.Store(true)
	c := inst.Client()
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
	defer cancel()
	if err := c.Shutdown(ctx); err != nil {
		c.Close()
		s.event(inst.Target.ID, "shutdown failed, killed: %v", err)
		return
	}
	s.event(inst.Target.ID, "shut down")
}

// launch starts s.target (opMu held). epoch guards against concurrent
// explicit operations.
func (s *Supervisor) launch(ctx context.Context, epoch int) error {
	s.mu.Lock()
	if s.epoch != epoch || s.target == nil {
		s.mu.Unlock()
		return nil
	}
	t := *s.target
	s.st.Server, s.st.Name, s.st.Command, s.st.Args = t.ID, t.Name, t.Command, append([]string(nil), t.Args...)
	s.mu.Unlock()

	// Presence and execute permission first.
	command := t.Command
	check := s.opts.Check
	if check == nil && s.opts.Connect == nil {
		check = defaultCheck
	}
	if check != nil {
		p, err := check(command, t.Searched)
		if err != nil {
			s.mu.Lock()
			if s.epoch == epoch {
				s.setStateLocked(StateNotFound, err.Error())
				s.st.Searched = append([]string(nil), t.Searched...)
			}
			s.mu.Unlock()
			s.event(t.ID, "cannot start: %v", err)
			s.publish()
			return fmt.Errorf("supervisor: %w", err)
		}
		command = p
	}

	ictx, cancel := context.WithTimeout(ctx, s.opts.InitTimeout)
	defer cancel()
	s.mu.Lock()
	if s.epoch != epoch {
		s.mu.Unlock()
		return context.Canceled
	}
	s.st.Generation++
	inst := &Instance{Target: t, Gen: s.st.Generation, started: time.Now()}
	s.inst = inst
	s.startCancel = cancel
	s.setStateLocked(StateStarting, "")
	s.mu.Unlock()
	s.event(t.ID, "starting %s %s", command, strings.Join(t.Args, " "))
	s.publish()

	cfg := s.config(inst, command)
	connect := s.opts.Connect
	if connect == nil {
		connect = lsp.Start
	}
	c, err := connect(ictx, cfg)

	s.mu.Lock()
	s.startCancel = nil
	aborted := s.epoch != epoch || s.inst != inst
	s.mu.Unlock()
	if aborted {
		// Aborted by Stop or Start, which report their own state.
		if c != nil {
			c.Close()
		}
		return fmt.Errorf("supervisor: start of %s aborted", t.ID)
	}
	if err != nil {
		s.mu.Lock()
		if s.inst == inst {
			s.inst = nil
		}
		s.mu.Unlock()
		reason := fmt.Sprintf("initialize failed: %v", err)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			reason = fmt.Sprintf("no answer to initialize within %s", s.opts.InitTimeout)
		}
		s.crashed(inst, epoch, reason)
		return fmt.Errorf("supervisor: starting %s: %w", t.ID, err)
	}
	inst.client.Store(c)
	if s.opts.OnReady != nil {
		s.opts.OnReady(inst)
	}
	s.mu.Lock()
	if s.inst != inst || s.epoch != epoch {
		s.mu.Unlock()
		return nil
	}
	s.setStateLocked(StateRunning, "")
	s.mu.Unlock()
	s.event(t.ID, "running (pid %d)", c.PID())
	s.publish()
	go s.watch(inst, epoch)
	return nil
}

func defaultCheck(command string, searched []string) (string, error) {
	dirs := append(filepath.SplitList(os.Getenv("PATH")), searched...)
	p, err := servers.CheckExecutable(command, dirs)
	if err != nil {
		return "", fmt.Errorf("executable %w", err)
	}
	return p, nil
}

// config builds the client configuration of an instance.
func (s *Supervisor) config(inst *Instance, command string) lsp.Config {
	t := inst.Target
	cfg := t.Config
	cfg.Command, cfg.Args = command, append([]string(nil), t.Args...)
	if cfg.Logger == nil {
		cfg.Logger = s.opts.Logger
	}
	id := t.ID
	userStderr := cfg.Stderr
	cfg.Stderr = func(line string) {
		s.log.Append(KindStderr, id, line)
		if userStderr != nil {
			userStderr(line)
		}
	}
	cfg.Trace = func(out bool, msg []byte) {
		if !s.trace.Load() {
			return
		}
		kind := KindRecv
		if out {
			kind = KindSend
		}
		s.log.Append(kind, id, string(msg))
	}
	if cfg.RequestTimeout == 0 && s.opts.RequestTimeout > 0 {
		cfg.RequestTimeout = s.opts.RequestTimeout
	}
	cfg.OnTimeout = func(method string, n int) {
		s.event(id, "%s: no answer within %s (%d in a row)", method, cfg.RequestTimeout, n)
		if n >= s.opts.HungLimit && !inst.Closed() {
			inst.hung.Store(fmt.Sprintf("not responding (%d requests timed out, last %s)", n, method))
			if c := inst.Client(); c != nil {
				go c.Close() // the watcher restarts it
			}
		}
	}
	cfg.OnDiagnostics = func(p protocol.PublishDiagnosticsParams) {
		if s.opts.OnDiagnostics != nil && inst.Client() != nil && !inst.Closed() {
			s.opts.OnDiagnostics(inst, p)
		}
	}
	return cfg
}

// watch handles the end of a running instance.
func (s *Supervisor) watch(inst *Instance, epoch int) {
	c := inst.Client()
	<-c.Done()
	if inst.Closed() {
		return
	}
	reason := "exited"
	if h, ok := inst.hung.Load().(string); ok && h != "" {
		reason = h
	} else if err := c.Err(); err != nil {
		reason = fmt.Sprintf("exited: %v", err)
	}
	c.Close() // reap the process
	s.mu.Lock()
	if s.inst != inst {
		s.mu.Unlock()
		return
	}
	s.inst = nil
	if time.Since(inst.started) >= s.opts.StableAfter {
		s.st.Crashes = 0 // ran long enough: a fresh crash series
	}
	s.mu.Unlock()
	s.crashed(inst, epoch, reason)
}

// crashed counts a crash and schedules a restart or gives up.
func (s *Supervisor) crashed(inst *Instance, epoch int, reason string) {
	id := inst.Target.ID
	s.mu.Lock()
	if s.epoch != epoch {
		s.mu.Unlock()
		return
	}
	s.st.Crashes++
	n := s.st.Crashes
	if n >= s.opts.MaxCrashes {
		s.setStateLocked(StateStopped, fmt.Sprintf("crashed %d times in a row, not restarting (last: %s)", n, reason))
		s.mu.Unlock()
		s.event(id, "crashed (%s); giving up after %d crashes in a row", reason, n)
		s.publish()
		return
	}
	d := s.opts.Backoff(n)
	at := time.Now().Add(d)
	s.setStateLocked(StateCrashed, reason)
	s.st.RetryAt = &at
	s.timer = time.AfterFunc(d, func() { s.autoRestart(epoch) })
	s.mu.Unlock()
	s.event(id, "crashed (%s); restart %d in %s", reason, n, d)
	s.publish()
}

func (s *Supervisor) autoRestart(epoch int) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.epoch != epoch || s.closed || s.st.State != StateCrashed {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	s.st.Restarts++
	s.mu.Unlock()
	if err := s.launch(context.Background(), epoch); err != nil {
		s.opts.Logger.Warn("lsp supervisor: restart failed", "err", err)
	}
}
