// Package client is the Go client of the Tiegel API server
// (`tiegel --headless serve`). It implements api.Caller, so code written
// against api.Caller (the GUI, docsync) works unchanged with an in-process
// App (api.InProcess) or a remote backend, e.g. inside WSL.
//
// Connect with Dial (socket, token authentication) or Spawn (child process
// over stdio, e.g. `wsl.exe -d Ubuntu -- tiegel --headless serve`):
//
//	c, err := client.Dial(ctx, "unix:/run/user/1000/tiegel.sock", token)
//	st, err := api.Call[api.DocState](ctx, c, "doc.insert", api.DocInsertParams{...})
//	cancel, err := c.Subscribe(ctx, []string{"doc.*"}, func(e api.Event) { ... })
//
// Errors returned by the server are *api.Error with the server's code and
// data (use api.ErrorCode). When the connection is gone, every call fails
// with an error matching ErrDisconnected and Done is closed; the client does
// not reconnect by itself – create a new one and subscribe again.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/server"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
)

// ErrDisconnected reports that the connection to the server is closed.
var ErrDisconnected = errors.New("client: disconnected")

// Option configures a Client.
type Option func(*options)

type options struct {
	framing jsonrpc2.Framing
	stderr  io.Writer
	wait    time.Duration
}

// WithFraming selects the message framing (default newline-delimited JSON,
// matching `serve` without --framing).
func WithFraming(f jsonrpc2.Framing) Option { return func(o *options) { o.framing = f } }

// WithStderr forwards the standard error of a spawned server (default:
// discarded).
func WithStderr(w io.Writer) Option { return func(o *options) { o.stderr = w } }

// WithExitTimeout sets how long Close waits for a spawned server to exit
// after closing its stdin before killing it (default 3 s).
func WithExitTimeout(d time.Duration) Option { return func(o *options) { o.wait = d } }

func buildOptions(opts []Option) options {
	o := options{framing: jsonrpc2.LineFraming, wait: 3 * time.Second}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// Client is a connection to an API server. It is safe for concurrent use.
type Client struct {
	conn *jsonrpc2.Conn
	opts options

	cmd     *exec.Cmd
	exited  chan struct{}
	waitErr error

	subMu  sync.Mutex
	subs   map[*subscription]struct{}
	remote map[string]int // server-side pattern -> number of local subscriptions

	closeOnce sync.Once
}

type subscription struct {
	patterns []string
	fn       func(api.Event)
}

var _ api.Caller = (*Client)(nil)

// New runs the client protocol over rwc (e.g. one end of a pipe). No
// authentication is performed; see Auth.
func New(rwc io.ReadWriteCloser, opts ...Option) *Client {
	o := buildOptions(opts)
	c := &Client{
		conn:   jsonrpc2.NewConnFraming(rwc, o.framing),
		opts:   o,
		subs:   make(map[*subscription]struct{}),
		remote: make(map[string]int),
	}
	c.conn.Handle(api.NotifyMethod, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		var ev api.Event
		if err := json.Unmarshal(req.Params, &ev); err == nil {
			c.dispatch(ev)
		}
		return nil, nil
	})
	c.conn.Start()
	return c
}

// Dial connects to a server started with `serve --listen addr` and
// authenticates with token. addr has the form "unix:/path" or
// "tcp:host:port" (see server.ParseAddr).
func Dial(ctx context.Context, addr, token string, opts ...Option) (*Client, error) {
	network, address, err := server.ParseAddr(addr)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("client: dial %s: %w", addr, err)
	}
	c := New(nc, opts...)
	if _, err := c.Auth(ctx, token); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Spawn starts a server as a child process and talks to it over its stdin
// and stdout. argv is the full command line, for example
//
//	client.Spawn(ctx, client.ServeCommand("tiegel")...)
//	client.Spawn(ctx, client.WSLServeCommand("Ubuntu", "tiegel")...)
//
// ctx only bounds the start; Close ends the process (stdin is closed so the
// server finishes cleanly, then it is killed after the exit timeout).
func Spawn(ctx context.Context, argv []string, opts ...Option) (*Client, error) {
	if len(argv) == 0 {
		return nil, errors.New("client: empty command")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := buildOptions(opts)
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("client: spawn: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("client: spawn: %w", err)
	}
	if o.stderr != nil {
		cmd.Stderr = o.stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("client: spawn %s: %w", argv[0], err)
	}
	c := New(&pipeRWC{r: stdout, w: stdin}, opts...)
	c.cmd = cmd
	c.exited = make(chan struct{})
	go func() {
		c.waitErr = cmd.Wait()
		close(c.exited)
		_ = c.conn.Close()
	}()
	return c, nil
}

// ServeCommand returns the command line of a stdio server: bin
// --headless serve plus extra flags (e.g. "--project", dir).
func ServeCommand(bin string, extra ...string) []string {
	return append([]string{bin, "--headless", "serve"}, extra...)
}

// WSLServeCommand runs the server inside a WSL distribution:
// wsl.exe -d distro -- bin --headless serve extra...
func WSLServeCommand(distro, bin string, extra ...string) []string {
	return append([]string{"wsl.exe", "-d", distro, "--"}, ServeCommand(bin, extra...)...)
}

type pipeRWC struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *pipeRWC) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRWC) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRWC) Close() error                { return errors.Join(p.w.Close(), p.r.Close()) }

// Auth sends the "auth" request. Dial does this already.
func (c *Client) Auth(ctx context.Context, token string) (api.AuthResult, error) {
	var res api.AuthResult
	err := c.Call(ctx, api.AuthMethod, api.AuthParams{Token: token}, &res)
	return res, err
}

// Call implements api.Caller.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.wrap(c.conn.Call(ctx, method, params, result))
}

// wrap converts transport errors: JSON-RPC errors become *api.Error, a
// closed connection matches ErrDisconnected.
func (c *Client) wrap(err error) error {
	if err == nil {
		return nil
	}
	var re *jsonrpc2.Error
	if errors.As(err, &re) {
		e := &api.Error{Code: int(re.Code), Message: re.Message}
		if len(re.Data) > 0 {
			e.Data = re.Data
		}
		return e
	}
	if errors.Is(err, jsonrpc2.ErrClosed) {
		return fmt.Errorf("%w: %w", ErrDisconnected, err)
	}
	return err
}

// Subscribe implements api.Caller. The server is asked for every pattern
// not yet subscribed by another local subscription; cancel unsubscribes
// patterns no longer needed.
func (c *Client) Subscribe(ctx context.Context, topics []string, fn func(api.Event)) (func(), error) {
	s := &subscription{fn: fn}
	for _, t := range topics {
		if t = strings.TrimSpace(t); t != "" {
			s.patterns = append(s.patterns, t)
		}
	}
	if len(s.patterns) == 0 {
		return nil, api.Errorf(api.CodeInvalidParams, "topics is required")
	}
	if s.fn == nil {
		s.fn = func(api.Event) {}
	}
	c.subMu.Lock()
	var add []string
	for _, p := range s.patterns {
		if c.remote[p] == 0 {
			add = append(add, p)
		}
		c.remote[p]++
	}
	c.subs[s] = struct{}{}
	c.subMu.Unlock()
	if len(add) > 0 {
		if err := c.Call(ctx, "events.subscribe", api.TopicsParams{Topics: add}, nil); err != nil {
			c.unsubscribe(context.Background(), s, false)
			return nil, err
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() { c.unsubscribe(context.Background(), s, true) })
	}, nil
}

func (c *Client) unsubscribe(ctx context.Context, s *subscription, tell bool) {
	c.subMu.Lock()
	if _, ok := c.subs[s]; !ok {
		c.subMu.Unlock()
		return
	}
	delete(c.subs, s)
	var drop []string
	for _, p := range s.patterns {
		c.remote[p]--
		if c.remote[p] <= 0 {
			delete(c.remote, p)
			drop = append(drop, p)
		}
	}
	c.subMu.Unlock()
	if tell && len(drop) > 0 {
		select {
		case <-c.conn.Done():
		default:
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = c.Call(ctx, "events.unsubscribe", api.TopicsParams{Topics: drop}, nil)
		}
	}
}

// dispatch hands an event to every matching local subscription. It runs on
// the connection's notification goroutine, so events keep their order.
func (c *Client) dispatch(ev api.Event) {
	c.subMu.Lock()
	var fns []func(api.Event)
	for s := range c.subs {
		for _, p := range s.patterns {
			if bus.Match(p, ev.Topic) {
				fns = append(fns, s.fn)
				break
			}
		}
	}
	c.subMu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
}

// Done is closed when the connection is gone.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// Err returns why the connection ended (nil while it is open).
func (c *Client) Err() error {
	select {
	case <-c.conn.Done():
	default:
		return nil
	}
	return c.wrap(jsonrpc2.ErrClosed)
}

// Close closes the connection. For a spawned server it closes the server's
// stdin, waits for it to exit (killing it after the exit timeout) and
// returns its exit error, if any.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		_ = c.conn.Close()
		if c.cmd == nil {
			return
		}
		select {
		case <-c.exited:
		case <-time.After(c.opts.wait):
			_ = c.cmd.Process.Kill()
			<-c.exited
		}
		var ee *exec.ExitError
		if c.waitErr != nil && !errors.As(c.waitErr, &ee) {
			err = c.waitErr
		}
	})
	return err
}
