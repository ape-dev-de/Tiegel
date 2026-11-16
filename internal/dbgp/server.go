package dbgp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// DefaultAddr is the default listen address. It is bound to loopback only;
// listening on all interfaces (e.g. "0.0.0.0:9003" for Docker setups without
// host.docker.internal) must be requested explicitly via [Config.Addr].
const DefaultAddr = "127.0.0.1:9003"

// Default timeouts.
const (
	DefaultInitTimeout    = 10 * time.Second
	DefaultFeatureTimeout = 5 * time.Second
)

// Config configures a [Server].
type Config struct {
	// Addr is the TCP listen address; empty means DefaultAddr.
	Addr string
	// Mapper translates file names; nil means identity mapping.
	Mapper PathMapper
	// Features are set on every new session before SessionStarted is
	// reported. nil means DefaultFeatures; use an empty slice for none.
	// Features the engine rejects are ignored.
	Features []Feature
	// Events receives session callbacks.
	Events Events
	// InitTimeout bounds the wait for the init packet (default 10 s).
	InitTimeout time.Duration
	// FeatureTimeout bounds feature negotiation (default 5 s).
	FeatureTimeout time.Duration
}

// Server accepts DBGp connections and manages their sessions.
type Server struct {
	cfg Config

	mu       sync.Mutex
	mapper   PathMapper
	ln       net.Listener
	conns    map[net.Conn]struct{}
	sessions map[string]*Session
	nextID   int
	wg       sync.WaitGroup
}

// NewServer returns a server for cfg. It does not listen until Start.
func NewServer(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Features == nil {
		cfg.Features = DefaultFeatures
	}
	if cfg.InitTimeout <= 0 {
		cfg.InitTimeout = DefaultInitTimeout
	}
	if cfg.FeatureTimeout <= 0 {
		cfg.FeatureTimeout = DefaultFeatureTimeout
	}
	return &Server{
		cfg:      cfg,
		mapper:   cfg.Mapper,
		conns:    make(map[net.Conn]struct{}),
		sessions: make(map[string]*Session),
	}
}

// Start opens the listener and accepts connections in the background.
func (srv *Server) Start(ctx context.Context) error {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.ln != nil {
		return errors.New("dbgp: server already running")
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", srv.cfg.Addr)
	if err != nil {
		return fmt.Errorf("dbgp: listen on %s: %w", srv.cfg.Addr, err)
	}
	srv.ln = ln
	srv.wg.Add(1)
	go srv.acceptLoop(ln)
	return nil
}

// Stop closes the listener and all sessions and waits until all connection
// goroutines have finished. Event callbacks may still be running when Stop
// returns. Stop on a stopped server is a no-op.
func (srv *Server) Stop() error {
	srv.mu.Lock()
	ln := srv.ln
	srv.ln = nil
	conns := make([]net.Conn, 0, len(srv.conns))
	for c := range srv.conns {
		conns = append(conns, c)
	}
	sessions := make([]*Session, 0, len(srv.sessions))
	for _, s := range srv.sessions {
		sessions = append(sessions, s)
	}
	srv.mu.Unlock()

	var err error
	if ln != nil {
		err = ln.Close()
	}
	for _, s := range sessions {
		_ = s.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	srv.wg.Wait()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("dbgp: close listener: %w", err)
	}
	return nil
}

// Running reports whether the server is listening.
func (srv *Server) Running() bool {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.ln != nil
}

// Addr returns the listen address, or nil if the server is not running.
func (srv *Server) Addr() net.Addr {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.ln == nil {
		return nil
	}
	return srv.ln.Addr()
}

// SetMapper replaces the path mapper; it applies to all sessions
// immediately. nil means identity mapping.
func (srv *Server) SetMapper(m PathMapper) {
	srv.mu.Lock()
	srv.mapper = m
	srv.mu.Unlock()
}

func (srv *Server) currentMapper() PathMapper {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.mapper
}

// Sessions returns the active sessions ordered by ID.
func (srv *Server) Sessions() []*Session {
	srv.mu.Lock()
	out := make([]*Session, 0, len(srv.sessions))
	for _, s := range srv.sessions {
		out = append(out, s)
	}
	srv.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].id)
		b, _ := strconv.Atoi(out[j].id)
		return a < b
	})
	return out
}

// Session returns the active session with the given ID.
func (srv *Server) Session(id string) (*Session, bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	s, ok := srv.sessions[id]
	return s, ok
}

func (srv *Server) acceptLoop(ln net.Listener) {
	defer srv.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// Unexpected listener error: back off briefly and retry
			// unless the listener was replaced or closed.
			time.Sleep(50 * time.Millisecond)
			srv.mu.Lock()
			current := srv.ln == ln
			srv.mu.Unlock()
			if !current {
				return
			}
			continue
		}
		srv.mu.Lock()
		if srv.ln != ln {
			srv.mu.Unlock()
			_ = conn.Close()
			return
		}
		srv.conns[conn] = struct{}{}
		srv.wg.Add(1)
		srv.mu.Unlock()
		go srv.handleConn(conn)
	}
}

func (srv *Server) dropConn(conn net.Conn) {
	srv.mu.Lock()
	delete(srv.conns, conn)
	srv.mu.Unlock()
}

func (srv *Server) handleConn(conn net.Conn) {
	defer srv.wg.Done()
	defer srv.dropConn(conn)

	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(srv.cfg.InitTimeout))
	pkt, err := readPacket(br)
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	root, v, err := decodePacket(pkt)
	init, ok := v.(*xmlInit)
	if err != nil || root != "init" || !ok {
		_ = conn.Close()
		return
	}

	srv.mu.Lock()
	srv.nextID++
	id := strconv.Itoa(srv.nextID)
	srv.mu.Unlock()
	s := newSession(id, conn, br, init, srv.currentMapper, srv.cfg.Events)
	s.onClose = srv.removeSession
	srv.mu.Lock()
	srv.sessions[id] = s
	srv.mu.Unlock()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		s.readLoop()
	}()

	if len(srv.cfg.Features) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), srv.cfg.FeatureTimeout)
		for _, f := range srv.cfg.Features {
			if err := s.FeatureSet(ctx, f.Name, f.Value); err != nil {
				if errors.Is(err, ErrSessionClosed) || ctx.Err() != nil {
					break
				}
			}
		}
		cancel()
	}

	if f := srv.cfg.Events.SessionStarted; f != nil {
		s.queue.pushFront(func() { f(s) })
	}
	s.queue.start()
	<-readDone
}

func (srv *Server) removeSession(s *Session) {
	srv.mu.Lock()
	delete(srv.sessions, s.id)
	srv.mu.Unlock()
}
