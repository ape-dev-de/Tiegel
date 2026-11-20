// Package terminal runs interactive programs (shells) in pseudo
// terminals for the embedded terminal (AP15.5): sessions with a VT
// emulator (package vt) that keeps the screen and the scrollback, input
// and key encoding, resize, and teardown of the whole process tree. It
// has no UI; the kernel's terminal.* API (internal/api) serves it to the
// GUI and the headless CLI.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// Kinds of sessions. Plugins add their own (e.g. "docker.service").
const KindShell = "shell"

// Info describes a session.
type Info struct {
	ID string `json:"id"`
	// Name is the tab name (renamable; Renamed after a rename); Title
	// the window title the program set (OSC 0/2).
	Name    string `json:"name"`
	Renamed bool   `json:"renamed,omitempty"`
	Title   string `json:"title,omitempty"`
	// Process is the name of the job in the foreground of the terminal
	// when it is not the shell itself (e.g. "vim"); Busy is set then, a
	// client asks before closing the tab.
	Process string `json:"process,omitempty"`
	Busy    bool   `json:"busy,omitempty"`
	// Kind is KindShell or the kind of a plugin launch; Target its
	// subject (e.g. the compose service).
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	// Command is the started program with its arguments.
	Command []string `json:"command"`
	// Dir is the start directory, Cwd the working directory the shell
	// reported last (OSC 7).
	Dir     string `json:"dir,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Project string `json:"project,omitempty"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	PID     int    `json:"pid,omitempty"`
	// RemotePID is the PID the program reported from the remote side
	// (ReportPIDArgs), for sessions with Options.StopRemote.
	RemotePID int  `json:"remotePid,omitempty"`
	Running   bool `json:"running"`
	// ExitCode is valid when Running is false (-1: killed by a signal).
	ExitCode int        `json:"exitCode"`
	Error    string     `json:"error,omitempty"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
}

// Options describe a new session.
type Options struct {
	Command
	Name, Kind, Target, Project string
	// Scrollback is the number of lines kept above the screen (0: none,
	// negative: vt.DefaultScrollback).
	Scrollback int
	// Clipboard lets programs write the clipboard (OSC 52) through
	// Manager.OnClipboard; off by default (privacy).
	Clipboard bool
	// StopRemote ends the processes on the remote side (a shell inside a
	// container) on Close, which killing the local client does not. It
	// gets the PID reported through ReportPIDArgs and is not called
	// without one.
	StopRemote StopRemoteFunc
}

// Output is a batch of program output of a session (event payload).
// Data is the raw output as text (escape sequences included); Seq
// increases with every batch and screen change. An empty Data reports
// a screen change without output (resize).
type Output struct {
	ID   string `json:"id"`
	Seq  uint64 `json:"seq"`
	Data string `json:"data"`
}

// Errors.
var (
	ErrNotFound = errors.New("terminal: no such session")
	ErrExited   = errors.New("terminal: the program has exited")
)

// RemoteStopTimeout bounds Options.StopRemote on Close.
const RemoteStopTimeout = 15 * time.Second

// Default sizes of a session opened without a size.
const (
	DefaultCols = 120
	DefaultRows = 30
)

// Manager owns the sessions. Set the callbacks before the first Open;
// they run on session goroutines and must not block for long.
type Manager struct {
	// Start starts programs (nil: StartPTY).
	Start Starter
	// Interval batches output events (0: 16 ms).
	Interval time.Duration

	OnOpen   func(Info)
	OnOutput func(Output)
	// OnChange reports a changed name, title or working directory.
	OnChange func(Info)
	OnExit   func(Info)
	OnClose  func(id string)
	// OnClipboard receives clipboard writes of programs in sessions
	// opened with Options.Clipboard.
	OnClipboard func(id, text string)

	// colors answer color queries of programs (SetColors).
	colors *vt.Colors

	mu       sync.Mutex
	sessions map[string]*Session
	order    []string
	next     int
}

// Session is one running (or exited) program.
type Session struct {
	m          *Manager
	proc       Process
	stopRemote StopRemoteFunc

	mu      sync.Mutex
	info    Info
	term    *vt.Terminal
	pending []byte
	changed bool
	closing bool

	// syncSince is when the program started a synchronized update
	// (mode 2026) that has not ended yet; zero otherwise.
	syncSince time.Time
	// fgCheck is when the foreground job was looked up last.
	fgCheck time.Time
	// clip holds clipboard writes (OSC 52) for the next flush.
	clip []string

	notify   chan struct{}
	input    chan []byte
	readDone chan struct{}
	done     chan struct{} // the program exited and its output was read
	stop     chan struct{} // Close: ends writer and notifier
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// Open starts a session.
func (m *Manager) Open(o Options) (Info, error) {
	if o.Program == "" {
		return Info{}, fmt.Errorf("terminal: no program")
	}
	if o.Cols <= 0 {
		o.Cols = DefaultCols
	}
	if o.Rows <= 0 {
		o.Rows = DefaultRows
	}
	if o.Kind == "" {
		o.Kind = KindShell
	}
	start := m.Start
	if start == nil {
		start = StartPTY
	}
	proc, err := start(o.Command)
	if err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = map[string]*Session{}
	}
	m.next++
	id := "t" + strconv.Itoa(m.next)
	name := o.Name
	if name == "" {
		name = "Local"
		if o.Kind != KindShell {
			name = o.Target
		}
		if n := m.countNamed(name); n > 0 {
			name += " (" + strconv.Itoa(n+1) + ")"
		}
	}
	s := &Session{m: m, proc: proc, stopRemote: o.StopRemote,
		info: Info{ID: id, Name: name, Kind: o.Kind, Target: o.Target, Command: append([]string{o.Program}, o.Args...),
			Dir: o.Dir, Project: o.Project, Cols: o.Cols, Rows: o.Rows, PID: proc.Pid(), Running: true, Started: time.Now()},
		term:     vt.New(o.Cols, o.Rows, o.Scrollback),
		notify:   make(chan struct{}, 1),
		input:    make(chan []byte, 256),
		readDone: make(chan struct{}),
		done:     make(chan struct{}),
		stop:     make(chan struct{}),
	}
	s.term.Reply = s.reply
	s.term.OSC = s.osc
	if m.colors != nil {
		s.term.Colors = *m.colors
	}
	if o.Clipboard {
		// Called with s.mu held: flush delivers outside the lock.
		s.term.Clipboard = func(_, text string) {
			if len(s.clip) < 8 {
				s.clip = append(s.clip, text)
				s.changed = true
			}
		}
	}
	m.sessions[id] = s
	m.order = append(m.order, id)
	info := s.info
	m.mu.Unlock()

	if m.OnOpen != nil {
		m.OnOpen(info)
	}
	s.wg.Add(3)
	go s.read()
	go s.write()
	go s.publish()
	go s.wait()
	return info, nil
}

// countNamed counts sessions whose name starts with name (m.mu held).
func (m *Manager) countNamed(name string) int {
	n := 0
	for _, s := range m.sessions {
		s.mu.Lock()
		nm := s.info.Name
		s.mu.Unlock()
		if nm == name || len(nm) > len(name)+2 && nm[:len(name)+2] == name+" (" {
			n++
		}
	}
	return n
}

// SetColors sets the colors reported to programs that query them
// (OSC 10/11/4) for all sessions, also future ones.
func (m *Manager) SetColors(c vt.Colors) {
	m.mu.Lock()
	m.colors = &c
	ss := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		ss = append(ss, s)
	}
	m.mu.Unlock()
	for _, s := range ss {
		s.mu.Lock()
		s.term.Colors = c
		s.mu.Unlock()
	}
}

// Get returns session id.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return s, nil
}

// List returns the sessions in creation order.
func (m *Manager) List() []Info {
	m.mu.Lock()
	ss := make([]*Session, 0, len(m.order))
	for _, id := range m.order {
		ss = append(ss, m.sessions[id])
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Info())
	}
	return out
}

// Close ends session id (hangup, then kill of the process tree; remote
// processes through Options.StopRemote) and forgets it. The session is
// gone also when the error reports a failed remote stop.
func (m *Manager) Close(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	stopErr := s.shutdown()
	m.mu.Lock()
	_, ok := m.sessions[id]
	delete(m.sessions, id)
	for i, x := range m.order {
		if x == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	if ok && m.OnClose != nil {
		m.OnClose(id)
	}
	if stopErr != nil {
		return fmt.Errorf("terminal: stop remote processes: %w", stopErr)
	}
	return nil
}

// CloseAll closes every session.
func (m *Manager) CloseAll() {
	m.CloseMatching(func(Info) bool { return true })
}

// CloseMatching closes the sessions match selects, in parallel, and
// returns their IDs.
func (m *Manager) CloseMatching(match func(Info) bool) []string {
	var (
		ids []string
		wg  sync.WaitGroup
	)
	for _, i := range m.List() {
		if !match(i) {
			continue
		}
		ids = append(ids, i.ID)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = m.Close(id)
		}(i.ID)
	}
	wg.Wait()
	return ids
}

// ---- session ---------------------------------------------------------------

// Info returns the session's state.
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkForeground(time.Now())
	return s.infoLocked()
}

func (s *Session) infoLocked() Info {
	i := s.info
	i.Title, i.Cwd = s.term.Title(), s.term.Dir()
	i.Command = append([]string(nil), i.Command...)
	return i
}

// foregrounder is implemented by processes that know the job in the
// foreground of their terminal.
type foregrounder interface {
	// Foreground returns the name of the foreground job when it is not
	// the program itself, "" otherwise.
	Foreground() string
}

// checkForeground updates Info.Process (s.mu held) at most every
// fgInterval; it reports a change.
func (s *Session) checkForeground(now time.Time) bool {
	f, ok := s.proc.(foregrounder)
	if !ok || !s.info.Running || now.Sub(s.fgCheck) < fgInterval {
		return false
	}
	s.fgCheck = now
	name := f.Foreground()
	if name == s.info.Process {
		return false
	}
	s.info.Process, s.info.Busy = name, name != ""
	return true
}

// fgInterval throttles foreground job lookups.
const fgInterval = 250 * time.Millisecond

// maxSync bounds how long a synchronized update (mode 2026) holds back
// output events.
const maxSync = 150 * time.Millisecond

// Done is closed when the program exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// read feeds the program output into the emulator.
func (s *Session) read() {
	defer s.wg.Done()
	defer close(s.readDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.proc.Read(buf)
		if n > 0 {
			s.mu.Lock()
			title, cwd := s.term.Title(), s.term.Dir()
			_, _ = s.term.Write(buf[:n])
			if s.term.Title() != title || s.term.Dir() != cwd {
				s.changed = true
			}
			switch sync := s.term.Modes().SyncOutput; {
			case sync && s.syncSince.IsZero():
				s.syncSince = time.Now()
			case !sync:
				s.syncSince = time.Time{}
			}
			if len(s.pending) < 1<<20 {
				s.pending = append(s.pending, buf[:n]...)
			}
			s.mu.Unlock()
			s.signal()
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// reply answers device queries of the program (called with s.mu held).
func (s *Session) reply(b []byte) {
	select {
	case s.input <- append([]byte(nil), b...):
	default:
	}
}

// osc receives operating system commands the emulator does not handle
// (called with s.mu held): the first PID report of a remote session.
func (s *Session) osc(num, text string) {
	if num != pidOSC || s.stopRemote == nil || s.info.RemotePID != 0 {
		return
	}
	if pid := parsePIDReport(text); pid > 0 {
		s.info.RemotePID = pid
		s.changed = true
	}
}

// write sends input to the program in order.
func (s *Session) write() {
	defer s.wg.Done()
	for {
		select {
		case b := <-s.input:
			if _, err := s.proc.Write(b); err != nil {
				// The program is gone; drop further input.
				continue
			}
		case <-s.stop:
			return
		}
	}
}

// publish sends batched output events.
func (s *Session) publish() {
	defer s.wg.Done()
	interval := s.m.Interval
	if interval <= 0 {
		interval = 16 * time.Millisecond
	}
	for {
		select {
		case <-s.notify:
		case <-s.stop:
			s.flush()
			return
		}
		// A synchronized update holds the events back until the frame
		// is complete (or maxSync passed), so clients never fetch a
		// half-drawn screen.
		for {
			s.mu.Lock()
			since := s.syncSince
			s.mu.Unlock()
			wait := maxSync - time.Since(since)
			if since.IsZero() || wait <= 0 {
				break
			}
			select {
			case <-s.notify:
			case <-time.After(min(wait, interval)):
			case <-s.stop:
				s.flush()
				return
			}
		}
		s.flush()
		select {
		case <-time.After(interval):
		case <-s.stop:
			s.flush()
			return
		}
	}
}

// flush publishes pending output (and a pending change).
func (s *Session) flush() {
	s.mu.Lock()
	data := s.pending
	// Keep an incomplete UTF-8 sequence for the next batch.
	cut := len(data)
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				cut = i
			}
			break
		}
	}
	s.pending = append([]byte(nil), data[cut:]...)
	data = data[:cut]
	out := Output{ID: s.info.ID, Seq: s.term.Seq(), Data: string(data)}
	if s.checkForeground(time.Now()) {
		s.changed = true
	}
	changed := s.changed
	s.changed = false
	info := s.infoLocked()
	clip := s.clip
	s.clip = nil
	s.mu.Unlock()
	if s.m.OnClipboard != nil {
		for _, c := range clip {
			s.m.OnClipboard(info.ID, c)
		}
	}
	if s.m.OnOutput != nil {
		s.m.OnOutput(out)
	}
	if changed && s.m.OnChange != nil {
		s.m.OnChange(info)
	}
}

// wait records the exit of the program.
func (s *Session) wait() {
	code, err := s.proc.Wait()
	// Output written just before the exit is still in the pty: read it
	// (a background job holding the terminal must not block the exit).
	select {
	case <-s.readDone:
	case <-time.After(500 * time.Millisecond):
	}
	now := time.Now()
	s.mu.Lock()
	s.info.Running = false
	s.info.Process, s.info.Busy = "", false
	s.info.ExitCode = code
	s.info.Finished = &now
	if err != nil {
		s.info.Error = err.Error()
	}
	info := s.infoLocked()
	closing := s.closing
	s.mu.Unlock()
	// Closed after Running is false: Resize no longer reaches it (on
	// Windows the pseudo console handle is freed here).
	_ = s.proc.Close()
	s.signal()
	// OnExit runs before done closes, so a returned wait implies the exit
	// was reported.
	if s.m.OnExit != nil && !closing {
		s.m.OnExit(info)
	}
	close(s.done)
}

// shutdown ends the program and the goroutines; it returns the error of
// a remote stop of a running program.
func (s *Session) shutdown() error {
	s.mu.Lock()
	s.closing = true
	pid := s.info.RemotePID
	s.mu.Unlock()
	exited := false
	select {
	case <-s.done:
		exited = true
	default:
	}
	var stopErr error
	if s.stopRemote != nil && pid > 0 {
		// First on the remote side: the client keeps no process there
		// alive, but once it is gone the remote shell is out of reach.
		ctx, cancel := context.WithTimeout(context.Background(), RemoteStopTimeout)
		stopErr = s.stopRemote(ctx, pid)
		cancel()
		if exited {
			// The container may be gone with the program: nothing to
			// report.
			stopErr = nil
		}
	}
	select {
	case <-s.done:
		// The program exited; members of its process groups may be
		// left (D334): kill them.
		_ = s.proc.Kill()
	default:
		// Hangup first (shells save their history), then kill what is
		// left: the shell if it did not end, and jobs ignoring SIGHUP.
		_ = s.proc.Hangup()
		select {
		case <-s.done:
		case <-time.After(time.Second):
		}
		_ = s.proc.Kill()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			_ = s.proc.Close()
		}
	}
	s.stopOnce.Do(func() { close(s.stop) })
	ch := make(chan struct{})
	go func() { s.wg.Wait(); close(ch) }()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
	}
	return stopErr
}

// Input sends raw bytes to the program.
func (s *Session) Input(ctx context.Context, b []byte) error {
	if len(b) == 0 {
		return nil
	}
	s.mu.Lock()
	running := s.info.Running && !s.closing
	s.mu.Unlock()
	if !running {
		return ErrExited
	}
	select {
	case s.input <- append([]byte(nil), b...):
		return nil
	case <-s.done:
		return ErrExited
	case <-ctx.Done():
		return fmt.Errorf("terminal: input: %w", ctx.Err())
	}
}

// Key sends a key press encoded for the program's current modes.
func (s *Session) Key(ctx context.Context, k vt.Key) error {
	s.mu.Lock()
	b := vt.EncodeKey(k, s.term.Modes())
	s.mu.Unlock()
	if b == nil {
		return fmt.Errorf("terminal: cannot encode key %q", k.Name)
	}
	return s.Input(ctx, b)
}

// Mouse sends a mouse event when the program asked for mouse reports;
// reported tells whether it did.
func (s *Session) Mouse(ctx context.Context, e vt.Mouse) (reported bool, err error) {
	s.mu.Lock()
	b := vt.EncodeMouse(e, s.term.Modes())
	s.mu.Unlock()
	if b == nil {
		return false, nil
	}
	return true, s.Input(ctx, b)
}

// Focus reports a focus change to the program when it asked for focus
// events.
func (s *Session) Focus(ctx context.Context, focused bool) error {
	s.mu.Lock()
	b := vt.EncodeFocus(focused, s.term.Modes())
	s.mu.Unlock()
	if b == nil {
		return nil
	}
	return s.Input(ctx, b)
}

// Paste sends pasted text (bracketed when the program asked for it).
func (s *Session) Paste(ctx context.Context, text string) error {
	s.mu.Lock()
	b := vt.EncodePaste(text, s.term.Modes())
	s.mu.Unlock()
	return s.Input(ctx, b)
}

// Resize changes the terminal size.
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("terminal: invalid size %dx%d", cols, rows)
	}
	cols, rows = min(cols, 1000), min(rows, 500)
	s.mu.Lock()
	if s.info.Cols == cols && s.info.Rows == rows {
		s.mu.Unlock()
		return nil
	}
	s.term.Resize(cols, rows)
	s.info.Cols, s.info.Rows = cols, rows
	running := s.info.Running
	s.mu.Unlock()
	s.signal()
	if !running {
		return nil
	}
	if err := s.proc.Resize(cols, rows); err != nil {
		if !s.Info().Running {
			// The program exited meanwhile (the terminal is closed).
			return nil
		}
		return fmt.Errorf("terminal: resize: %w", err)
	}
	return nil
}

// Rename sets the tab name.
func (s *Session) Rename(name string) Info {
	s.mu.Lock()
	s.info.Name, s.info.Renamed = name, true
	info := s.infoLocked()
	s.mu.Unlock()
	if s.m.OnChange != nil {
		s.m.OnChange(info)
	}
	return info
}

// Wait waits until the program exited (or ctx ends) and returns the
// session's state.
func (s *Session) Wait(ctx context.Context) (Info, error) {
	select {
	case <-s.done:
		return s.Info(), nil
	case <-ctx.Done():
		return s.Info(), fmt.Errorf("terminal: wait: %w", ctx.Err())
	}
}

// Text returns the text between two positions (absolute line index into
// scrollback plus screen, column; end column exclusive).
func (s *Session) Text(line1, col1, line2, col2 int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.Text(line1, col1, line2, col2)
}

// ScreenText returns the visible screen as text.
func (s *Session) ScreenText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.ScreenText()
}

// AllText returns scrollback and screen as text.
func (s *Session) AllText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return trimTrailingLines(s.term.Text(0, 0, s.term.Total()-1, s.info.Cols))
}

func trimTrailingLines(t string) string {
	for len(t) > 0 && t[len(t)-1] == '\n' {
		t = t[:len(t)-1]
	}
	return t
}
