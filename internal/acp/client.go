package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/proctree"
)

// Handler serves the agent's calls into the client. Methods may block
// (a permission request waits for the user); ctx ends with the
// connection.
type Handler interface {
	// SessionUpdate receives session/update notifications in order.
	SessionUpdate(n SessionNotification)
	// RequestPermission decides about a tool call.
	RequestPermission(ctx context.Context, r RequestPermissionRequest) (RequestPermissionResponse, error)
	// ReadTextFile returns a file's content (fs/read_text_file).
	ReadTextFile(ctx context.Context, r ReadTextFileRequest) (ReadTextFileResponse, error)
	// WriteTextFile writes a file (fs/write_text_file).
	WriteTextFile(ctx context.Context, r WriteTextFileRequest) error
}

// Client is the client side of an ACP connection.
type Client struct {
	conn *Conn
	h    Handler
}

// NewClient connects to an agent reading r and writing w.
func NewClient(r io.Reader, w io.Writer, h Handler) *Client {
	c := &Client{h: h}
	c.conn = NewConn(r, w, c.serve, c.notify)
	return c
}

// Conn returns the underlying connection.
func (c *Client) Conn() *Conn { return c.conn }

// Done is closed when the connection ended.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

func (c *Client) notify(method string, params json.RawMessage) {
	if method != MethodSessionUpdate {
		return
	}
	var n SessionNotification
	if json.Unmarshal(params, &n) == nil {
		c.h.SessionUpdate(n)
	}
}

func decode[T any](params json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(params, &v); err != nil {
		return v, &Error{Code: CodeInvalidParams, Message: err.Error()}
	}
	return v, nil
}

func (c *Client) serve(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case MethodRequestPermission:
		r, err := decode[RequestPermissionRequest](params)
		if err != nil {
			return nil, err
		}
		return c.h.RequestPermission(ctx, r)
	case MethodReadTextFile:
		r, err := decode[ReadTextFileRequest](params)
		if err != nil {
			return nil, err
		}
		return c.h.ReadTextFile(ctx, r)
	case MethodWriteTextFile:
		r, err := decode[WriteTextFileRequest](params)
		if err != nil {
			return nil, err
		}
		return nil, c.h.WriteTextFile(ctx, r)
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not supported by the client: " + method}
}

// Initialize negotiates the protocol version and capabilities.
func (c *Client) Initialize(ctx context.Context, r InitializeRequest) (InitializeResponse, error) {
	var res InitializeResponse
	err := c.conn.Call(ctx, MethodInitialize, r, &res)
	return res, err
}

// Authenticate runs an auth method of the agent.
func (c *Client) Authenticate(ctx context.Context, methodID string) error {
	return c.conn.Call(ctx, MethodAuthenticate, AuthenticateRequest{MethodID: methodID}, nil)
}

// NewSession creates a session working in cwd.
func (c *Client) NewSession(ctx context.Context, cwd string) (NewSessionResponse, error) {
	var res NewSessionResponse
	err := c.conn.Call(ctx, MethodSessionNew, NewSessionRequest{Cwd: cwd, MCPServers: []any{}}, &res)
	return res, err
}

// Prompt sends a user turn and waits until the agent ended it.
func (c *Client) Prompt(ctx context.Context, sessionID string, prompt []ContentBlock) (PromptResponse, error) {
	var res PromptResponse
	err := c.conn.Call(ctx, MethodSessionPrompt, PromptRequest{SessionID: sessionID, Prompt: prompt}, &res)
	return res, err
}

// Cancel asks the agent to end the running turn (session/cancel).
func (c *Client) Cancel(sessionID string) error {
	return c.conn.Notify(MethodSessionCancel, CancelNotification{SessionID: sessionID})
}

// SetConfigOption changes a session option (e.g. the model).
func (c *Client) SetConfigOption(ctx context.Context, sessionID, id, value string) ([]ConfigOption, error) {
	var res SetConfigOptionResponse
	err := c.conn.Call(ctx, MethodSetConfigOption, SetConfigOptionRequest{SessionID: sessionID, ConfigID: id, Value: value}, &res)
	return res.ConfigOptions, err
}

// Command describes the agent process.
type Command struct {
	// Path is the executable, Args its arguments.
	Path string
	Args []string
	// Dir is the working directory.
	Dir string
	// Env is the complete environment (nil: the process environment).
	Env []string
}

// Process is an agent child process with its client.
type Process struct {
	*Client
	cmd    *exec.Cmd
	tree   *proctree.Tree
	stdin  io.WriteCloser
	stderr *tailBuffer

	waitOnce sync.Once
	waitErr  error
	exited   chan struct{}
}

// Start runs the agent and connects to it. The process lives until Close
// (or until it exits); ctx only bounds the start.
func Start(ctx context.Context, c Command, h Handler) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout: %w", err)
	}
	tail := &tailBuffer{max: 16 << 10}
	cmd.Stderr = tail
	tree := proctree.New(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start %s: %w", c.Path, err)
	}
	tree.Attach()
	p := &Process{cmd: cmd, tree: tree, stdin: stdin, stderr: tail, exited: make(chan struct{})}
	p.Client = NewClient(stdout, stdin, h)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
		p.conn.shutdown(errors.New("agent exited"))
	}()
	return p, nil
}

// Attach wraps an agent that is reachable over r and w but is no child
// process (e.g. an in-process fake in tests) as a Process. done is closed
// when the agent ended; Close closes w and waits for it.
func Attach(r io.Reader, w io.WriteCloser, h Handler, done <-chan struct{}) *Process {
	p := &Process{stdin: w, stderr: &tailBuffer{max: 16 << 10}, exited: make(chan struct{})}
	p.Client = NewClient(r, w, h)
	go func() {
		<-done
		close(p.exited)
		p.conn.shutdown(errors.New("agent exited"))
	}()
	return p
}

// Exited is closed when the process ended.
func (p *Process) Exited() <-chan struct{} { return p.exited }

// ExitErr returns the exit error after Exited.
func (p *Process) ExitErr() error {
	select {
	case <-p.exited:
		return p.waitErr
	default:
		return nil
	}
}

// Stderr returns the last output of the agent on stderr.
func (p *Process) Stderr() string { return p.stderr.String() }

// Close ends the agent: stdin is closed (agents exit on EOF), after a
// grace period the process group is killed.
func (p *Process) Close() error {
	p.waitOnce.Do(func() {
		_ = p.stdin.Close()
		select {
		case <-p.exited:
		case <-time.After(2 * time.Second):
		}
		if p.tree != nil {
			// Also ends what the agent started (shell commands, servers).
			_ = p.tree.Kill()
			<-p.exited
			p.tree.Release()
		}
		_ = p.conn.Close()
	})
	return nil
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
		if i := bytes.IndexByte(t.buf, '\n'); i >= 0 && i < len(t.buf)-1 {
			t.buf = t.buf[i+1:]
		}
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
