// Package server serves the Tiegel API (internal/api) on a socket so that
// several clients – the GUI on Windows talking to the backend inside WSL,
// scripts, tests – can use one backend at the same time
// (`tiegel --headless serve --listen ...`).
//
// Addresses are "unix:/path/to/sock" or "tcp:HOST:PORT". Security rules:
//
//   - Every connection must authenticate first with the request "auth"
//     {"token": ...} (api.AuthMethod); the token is random per start unless
//     given, and is written to a token file readable only by the user (0600).
//   - Unix sockets are created with mode 0600.
//   - TCP listens on loopback only; any other host (including 0.0.0.0 and
//     "::") needs Config.AllowRemote and logs a warning, because the
//     protocol is neither encrypted nor meant for untrusted networks.
//
// Each connection is an independent api.Session with its own event
// subscriptions. Serve stops accepting when its context ends, closes all
// connections and removes the socket and the token file it created.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// TokenEnv is the environment variable that may carry the token for both the
// server (instead of generating one) and clients.
const TokenEnv = "TIEGEL_TOKEN"

// ParseAddr splits an address into network and address for net.Listen and
// net.Dial. Accepted forms: "unix:/path", "tcp:host:port", a bare absolute
// path (unix) or a bare "host:port" (tcp).
func ParseAddr(s string) (network, address string, err error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "unix:"):
		network, address = "unix", strings.TrimPrefix(s, "unix:")
	case strings.HasPrefix(s, "tcp:"):
		network, address = "tcp", strings.TrimPrefix(s, "tcp:")
	case filepath.IsAbs(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./"):
		network, address = "unix", s
	case strings.Contains(s, ":"):
		network, address = "tcp", s
	default:
		return "", "", fmt.Errorf("server: invalid address %q (want unix:/path or tcp:host:port)", s)
	}
	if address == "" {
		return "", "", fmt.Errorf("server: empty address in %q", s)
	}
	if network == "tcp" {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return "", "", fmt.Errorf("server: invalid tcp address %q: %w", address, err)
		}
	}
	return network, address, nil
}

// FormatAddr is the inverse of ParseAddr.
func FormatAddr(network, address string) string { return network + ":" + address }

// IsLoopback reports whether a TCP host only accepts local connections:
// "localhost" or a loopback IP. Empty hosts, 0.0.0.0 and "::" are not.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// GenerateToken returns 32 random bytes, hex encoded.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("server: token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// WriteTokenFile writes token to path with mode 0600 (creating the directory
// with 0700). An existing file is replaced atomically.
func WriteTokenFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("server: token dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return fmt.Errorf("server: token file: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(token + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o600)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("server: token file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("server: token file: %w", err)
	}
	return nil
}

// ReadTokenFile reads a token written by WriteTokenFile.
func ReadTokenFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("server: token file: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("server: token file %s is empty", path)
	}
	return t, nil
}

// Config configures Listen.
type Config struct {
	// Addr is "unix:/path" or "tcp:host:port" (port 0 picks a free one).
	Addr string
	// AllowRemote permits TCP hosts other than loopback. Use with care:
	// the connection is not encrypted.
	AllowRemote bool
	// Token is the shared secret; empty generates a random one.
	Token string
	// TokenFile receives a generated token (mode 0600) and is removed
	// again by Close. Ignored when Token is set. Required otherwise, since
	// clients would have no way to learn the token.
	TokenFile string
	// Serve configures each connection (framing, concurrency). Its Token
	// is set from Token/TokenFile.
	Serve api.ServeOptions
	// Logf receives notices and warnings (nil: discarded).
	Logf func(format string, args ...any)
}

// Server is a listening API endpoint.
type Server struct {
	cfg       Config
	ln        net.Listener
	addr      string
	token     string
	tokenFile string // written by us, removed on Close
	sockPath  string

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
}

// Listen binds the address and prepares the token. Serve starts accepting.
func Listen(cfg Config) (*Server, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	network, address, err := ParseAddr(cfg.Addr)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, conns: make(map[net.Conn]struct{})}
	switch network {
	case "tcp":
		host, _, _ := net.SplitHostPort(address)
		if !IsLoopback(host) {
			if !cfg.AllowRemote {
				return nil, fmt.Errorf("server: refusing to listen on non-loopback host %q (use 127.0.0.1, or allow it explicitly with --allow-remote)", host)
			}
			cfg.Logf("WARNING: listening on %s – reachable from other machines; traffic is not encrypted, only the token protects it", address)
		}
		s.ln, err = net.Listen("tcp", address)
	case "unix":
		s.ln, err = listenUnix(address)
		s.sockPath = address
	}
	if err != nil {
		return nil, fmt.Errorf("server: listen %s: %w", cfg.Addr, err)
	}
	s.addr = FormatAddr(network, s.ln.Addr().String())
	if network == "unix" {
		s.addr = FormatAddr(network, address)
	}
	if err := s.initToken(); err != nil {
		_ = s.ln.Close()
		return nil, err
	}
	return s, nil
}

func listenUnix(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode().Type()&fs.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		// A socket file: in use, or left over from a crashed server.
		if c, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("%s is in use by another server", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func (s *Server) initToken() error {
	if s.cfg.Token != "" {
		s.token = s.cfg.Token
		return nil
	}
	if s.cfg.TokenFile == "" {
		return errors.New("server: a token or a token file is required")
	}
	t, err := GenerateToken()
	if err != nil {
		return err
	}
	if err := WriteTokenFile(s.cfg.TokenFile, t); err != nil {
		return err
	}
	s.token, s.tokenFile = t, s.cfg.TokenFile
	return nil
}

// Addr returns the bound address in ParseAddr form (with the real port for
// "tcp:host:0").
func (s *Server) Addr() string { return s.addr }

// Token returns the token clients must send.
func (s *Server) Token() string { return s.token }

// TokenFile returns the token file written by the server ("" if the token
// was given).
func (s *Server) TokenFile() string { return s.tokenFile }

// Serve accepts connections and serves app on each of them until ctx ends
// or Close is called. It then closes all connections, waits for their
// handlers and removes the socket and token files. The result is nil after
// a regular shutdown.
func (s *Server) Serve(ctx context.Context, app *api.App) error {
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	opts := s.cfg.Serve
	opts.Token = s.token
	var err error
	for {
		c, aerr := s.ln.Accept()
		if aerr != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if !closed {
				err = fmt.Errorf("server: accept: %w", aerr)
			}
			break
		}
		if !s.track(c) {
			_ = c.Close()
			break
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrack(c)
			cctx, cancel := context.WithCancel(ctx)
			defer cancel()
			if err := app.Serve(cctx, c, opts); err != nil && cctx.Err() == nil {
				s.cfg.Logf("connection %s: %v", c.RemoteAddr(), err)
			}
		}()
	}
	_ = s.Close()
	s.wg.Wait()
	return err
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

// Close stops the listener, closes all connections and removes the files
// the server created. It is safe to call more than once and concurrently;
// every call returns after the cleanup is done.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		conns := make([]net.Conn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.mu.Unlock()
		err := s.ln.Close()
		for _, c := range conns {
			_ = c.Close()
		}
		if s.tokenFile != "" {
			_ = os.Remove(s.tokenFile)
		}
		if s.sockPath != "" {
			_ = os.Remove(s.sockPath)
		}
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		s.closeErr = err
	})
	return s.closeErr
}
