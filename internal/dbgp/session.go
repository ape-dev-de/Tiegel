package dbgp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Session is one connection of a debugger engine (one PHP request or CLI
// run). All methods are safe for concurrent use. Commands block until the
// engine responds, the context is done or the session is closed.
type Session struct {
	id     string
	conn   net.Conn
	r      *bufio.Reader
	info   InitInfo
	mapper func() PathMapper
	events Events
	queue  *eventQueue
	// onClose is called once after the session was shut down.
	onClose func(*Session)

	txid atomic.Int64
	wmu  sync.Mutex // serializes writes

	mu        sync.Mutex
	pending   map[int]chan *xmlResponse
	status    Status
	lastBreak BreakInfo
	closed    bool
	err       error
	done      chan struct{}
}

func newSession(id string, conn net.Conn, r *bufio.Reader, init *xmlInit, mapper func() PathMapper, ev Events) *Session {
	s := &Session{
		id:      id,
		conn:    conn,
		r:       r,
		mapper:  mapper,
		events:  ev,
		queue:   newEventQueue(),
		pending: make(map[int]chan *xmlResponse),
		status:  StatusStarting,
		done:    make(chan struct{}),
	}
	s.info = InitInfo{
		FileURI:         init.FileURI,
		IDEKey:          init.IDEKey,
		AppID:           init.AppID,
		Session:         init.Session,
		Thread:          init.Thread,
		Parent:          init.Parent,
		Language:        init.Language,
		LanguageVersion: init.LanguageVersion,
		ProtocolVersion: init.ProtocolVersion,
		Engine:          trimSpace(init.Engine.Name),
		EngineVersion:   init.Engine.Version,
	}
	if init.FileURI != "" {
		s.info.File = s.toHost(init.FileURI)
	}
	return s
}

// ID returns the server-assigned session ID (unique per server).
func (s *Session) ID() string { return s.id }

// Info returns the data of the init packet.
func (s *Session) Info() InitInfo { return s.info }

// RemoteAddr returns the address of the engine.
func (s *Session) RemoteAddr() net.Addr { return s.conn.RemoteAddr() }

// CurrentStatus returns the last known execution status without contacting
// the engine.
func (s *Session) CurrentStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// LastBreak returns the location of the most recent break.
func (s *Session) LastBreak() BreakInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBreak
}

// Done is closed when the session has ended.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err returns the error that ended the session, or nil for a regular end or
// while the session is active.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the connection without notifying the engine. Prefer
// [Session.Stop] or [Session.Detach] for a clean end.
func (s *Session) Close() error {
	s.shutdown(nil)
	return nil
}

func (s *Session) toHost(uri string) string {
	if m := s.mapper(); m != nil {
		return m.RemoteToHost(uri)
	}
	return URIToPath(uri)
}

func (s *Session) toRemote(path string) string {
	if m := s.mapper(); m != nil {
		return m.HostToRemote(path)
	}
	return PathToURI(path)
}

// readLoop reads packets until the connection fails.
func (s *Session) readLoop() {
	for {
		pkt, err := readPacket(s.r)
		if err != nil {
			s.shutdown(err)
			return
		}
		s.dispatch(pkt)
	}
}

func (s *Session) dispatch(pkt []byte) {
	_, v, err := decodePacket(pkt)
	if err != nil {
		return // malformed or unknown packets are ignored
	}
	switch p := v.(type) {
	case *xmlResponse:
		s.handleResponse(p)
	case *xmlNotify:
		s.handleNotify(p)
	case *xmlStream:
		st := Stream{Type: p.Type, Data: decodeText(p.Text, p.Encoding)}
		if f := s.events.Output; f != nil {
			s.queue.push(func() { f(s, st) })
		}
	}
}

func (s *Session) handleResponse(r *xmlResponse) {
	if r.Status != "" {
		s.updateStatus(r)
	}
	id, err := strconv.Atoi(r.TransactionID)
	if err != nil {
		return
	}
	s.mu.Lock()
	ch := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

func (s *Session) handleNotify(n *xmlNotify) {
	if n.Name == "breakpoint_resolved" && n.Breakpoint != nil {
		bp := s.convertBreakpoint(n.Breakpoint)
		if f := s.events.BreakpointResolved; f != nil {
			s.queue.push(func() { f(s, bp) })
		}
		return
	}
	note := Notification{Name: n.Name, Message: trimSpace(n.Text)}
	if m := n.Message; m != nil {
		note.FileURI = m.Filename
		if m.Filename != "" {
			note.File = s.toHost(m.Filename)
		}
		note.Line = atoi(m.Lineno)
		note.Type = m.Type
		note.Code = m.Code
		note.Message = trimSpace(m.Text)
	}
	if f := s.events.Notification; f != nil {
		s.queue.push(func() { f(s, note) })
	}
}

func (s *Session) breakInfo(r *xmlResponse) BreakInfo {
	bi := BreakInfo{Status: Status(r.Status), Reason: r.Reason}
	if m := r.Message; m != nil {
		bi.FileURI = m.Filename
		if m.Filename != "" {
			bi.File = s.toHost(m.Filename)
		}
		bi.Line = atoi(m.Lineno)
		bi.Exception = m.Exception
		bi.ExceptionCode = m.Code
		bi.Message = trimSpace(m.Text)
	}
	return bi
}

func (s *Session) updateStatus(r *xmlResponse) {
	bi := s.breakInfo(r)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	prev := s.status
	s.status = bi.Status
	if bi.Status == StatusBreak {
		s.lastBreak = bi
	}
	s.mu.Unlock()
	if prev != bi.Status || isContinuation(r.Command) {
		s.emitStatus(StatusEvent{Previous: prev, Command: r.Command, Info: bi})
	}
}

// setRunning marks the session as running before a continuation command is
// sent.
func (s *Session) setRunning(cmd string) {
	s.mu.Lock()
	prev := s.status
	s.status = StatusRunning
	s.mu.Unlock()
	if prev != StatusRunning {
		s.emitStatus(StatusEvent{Previous: prev, Command: cmd, Info: BreakInfo{Status: StatusRunning}})
	}
}

func (s *Session) emitStatus(ev StatusEvent) {
	if f := s.events.StatusChanged; f != nil {
		s.queue.push(func() { f(s, ev) })
	}
}

// shutdown closes the session once. err is the cause (nil, EOF and closed
// connections count as a regular end).
func (s *Session) shutdown(err error) {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.err = err
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
	prev := s.status
	s.status = StatusStopped
	close(s.done)
	s.mu.Unlock()

	_ = s.conn.Close()
	if prev != StatusStopped {
		s.emitStatus(StatusEvent{Previous: prev, Info: BreakInfo{Status: StatusStopped}})
	}
	if f := s.events.SessionEnded; f != nil {
		s.queue.push(func() { f(s, err) })
	}
	s.queue.close()
	if s.onClose != nil {
		s.onClose(s)
	}
}

// send transmits a command and waits for its response.
func (s *Session) send(ctx context.Context, name string, args []string, data []byte) (*xmlResponse, error) {
	id := int(s.txid.Add(1))
	pkt, err := encodeCommand(name, id, args, data)
	if err != nil {
		return nil, err
	}
	ch := make(chan *xmlResponse, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSessionClosed
	}
	s.pending[id] = ch
	s.mu.Unlock()

	switch name {
	case "run", "step_into", "step_over", "step_out":
		s.setRunning(name)
	}

	s.wmu.Lock()
	if dl, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(dl)
	}
	_, err = s.conn.Write(pkt)
	_ = s.conn.SetWriteDeadline(time.Time{})
	s.wmu.Unlock()
	if err != nil {
		s.forget(id)
		if s.isClosed() {
			return nil, ErrSessionClosed
		}
		return nil, fmt.Errorf("dbgp: send %s: %w", name, err)
	}

	select {
	case r, ok := <-ch:
		if !ok {
			return nil, ErrSessionClosed
		}
		if r.Error != nil {
			return r, &Error{Command: name, Code: atoi(r.Error.Code), Message: trimSpace(r.Error.Message)}
		}
		return r, nil
	case <-ctx.Done():
		s.forget(id)
		return nil, fmt.Errorf("dbgp: %s: %w", name, ctx.Err())
	}
}

func (s *Session) forget(id int) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Send sends an arbitrary DBGp command and returns the raw response. args
// are flag/value pairs without the transaction ID, e.g.
// []string{"-n", "max_depth", "-v", "2"}; data is sent base64-encoded after
// "--" if non-nil. Engine errors are returned as *[Error].
func (s *Session) Send(ctx context.Context, command string, args []string, data []byte) (*Response, error) {
	r, err := s.send(ctx, command, args, data)
	if r == nil {
		return nil, err
	}
	return &Response{
		Command:       r.Command,
		TransactionID: r.TransactionID,
		Status:        Status(r.Status),
		Reason:        r.Reason,
		Success:       r.Success == "1",
		Raw:           r.raw,
	}, err
}
