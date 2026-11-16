package jsonrpc2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func pair(t *testing.T) (client, server *Conn) {
	t.Helper()
	a, b := net.Pipe()
	client, server = NewConn(a), NewConn(b)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCallResult(t *testing.T) {
	c, s := pair(t)
	s.Handle("add", func(_ context.Context, req *Request) (any, error) {
		var p [2]int
		if err := req.UnmarshalParams(&p); err != nil {
			return nil, err
		}
		return p[0] + p[1], nil
	})
	s.Start()
	c.Start()
	var sum int
	if err := c.Call(ctxT(t), "add", [2]int{2, 3}, &sum); err != nil {
		t.Fatal(err)
	}
	if sum != 5 {
		t.Fatalf("sum = %d", sum)
	}
	// nil result is ok and raw params pass through
	if err := c.Call(ctxT(t), "add", json.RawMessage(`[1,1]`), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCallErrors(t *testing.T) {
	c, s := pair(t)
	s.Handle("fail", func(context.Context, *Request) (any, error) {
		return nil, NewError(CodeInvalidParams, "bad")
	})
	s.Handle("plain", func(context.Context, *Request) (any, error) { return nil, errors.New("boom") })
	s.Handle("unmarshalable", func(context.Context, *Request) (any, error) { return make(chan int), nil })
	s.Handle("params", func(_ context.Context, req *Request) (any, error) {
		var v int
		return nil, req.UnmarshalParams(&v)
	})
	s.Handle("wrongresult", func(context.Context, *Request) (any, error) { return "str", nil })
	s.Start()
	c.Start()
	ctx := ctxT(t)

	var e *Error
	if err := c.Call(ctx, "fail", nil, nil); !errors.As(err, &e) || e.Code != CodeInvalidParams || !strings.Contains(e.Error(), "bad") {
		t.Fatalf("fail: %v", err)
	}
	if err := c.Call(ctx, "plain", nil, nil); !errors.As(err, &e) || e.Code != CodeInternalError {
		t.Fatalf("plain: %v", err)
	}
	if err := c.Call(ctx, "unknown", nil, nil); !errors.As(err, &e) || e.Code != CodeMethodNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if err := c.Call(ctx, "unmarshalable", nil, nil); !errors.As(err, &e) || e.Code != CodeInternalError {
		t.Fatalf("unmarshalable: %v", err)
	}
	if err := c.Call(ctx, "params", "x", nil); !errors.As(err, &e) || e.Code != CodeInvalidParams {
		t.Fatalf("params: %v", err)
	}
	var n int
	if err := c.Call(ctx, "wrongresult", nil, &n); err == nil {
		t.Fatal("expected decode error")
	}
	if err := c.Call(ctx, "x", make(chan int), nil); err == nil {
		t.Fatal("expected marshal error")
	}
	if err := c.Notify(ctx, "x", make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestDefaultHandlerAndUnregister(t *testing.T) {
	c, s := pair(t)
	s.HandleDefault(func(_ context.Context, req *Request) (any, error) { return req.Method, nil })
	s.Handle("x", func(context.Context, *Request) (any, error) { return "x", nil })
	s.Handle("x", nil)
	s.Start()
	s.Start() // no-op
	c.Start()
	var got string
	if err := c.Call(ctxT(t), "x", nil, &got); err != nil || got != "x" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNotificationsInOrder(t *testing.T) {
	c, s := pair(t)
	var mu sync.Mutex
	var got []int
	done := make(chan struct{})
	s.Handle("n", func(_ context.Context, req *Request) (any, error) {
		if !req.IsNotification() {
			t.Error("expected notification")
		}
		var v int
		_ = req.UnmarshalParams(&v)
		mu.Lock()
		got = append(got, v)
		if len(got) == 100 {
			close(done)
		}
		mu.Unlock()
		return nil, nil
	})
	s.Start()
	c.Start()
	for i := 0; i < 100; i++ {
		if err := c.Notify(ctxT(t), "n", i); err != nil {
			t.Fatal(err)
		}
	}
	// Unknown notifications are dropped silently.
	if err := c.Notify(ctxT(t), "unknown", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	mu.Lock()
	defer mu.Unlock()
	for i, v := range got {
		if v != i {
			t.Fatalf("out of order at %d: %v", i, got)
		}
	}
}

func TestCancelSendsCancelRequest(t *testing.T) {
	c, s := pair(t)
	cancelled := make(chan struct{})
	s.Handle("slow", func(ctx context.Context, _ *Request) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	s.Start()
	c.Start()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- c.Call(ctx, "slow", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("server handler not cancelled")
	}
	// Connection still usable afterwards; late response is dropped.
	s.Handle("ping", func(context.Context, *Request) (any, error) { return "pong", nil })
	var r string
	if err := c.Call(ctxT(t), "ping", nil, &r); err != nil || r != "pong" {
		t.Fatalf("ping: %q %v", r, err)
	}
}

func TestCancelHandlerObservesNotification(t *testing.T) {
	c, s := pair(t)
	seen := make(chan struct{}, 1)
	s.Handle(CancelMethod, func(context.Context, *Request) (any, error) {
		seen <- struct{}{}
		return nil, nil
	})
	s.Start()
	c.Start()
	if err := c.Notify(ctxT(t), CancelMethod, map[string]any{"id": "unknown"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel notification not delivered")
	}
}

func TestCloseFailsPending(t *testing.T) {
	c, s := pair(t)
	s.Handle("hang", func(ctx context.Context, _ *Request) (any, error) {
		<-ctx.Done()
		return nil, nil
	})
	s.Start()
	c.Start()
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "hang", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	_ = c.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call hangs after close")
	}
	<-c.Done()
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err = %v", c.Err())
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
	if err := c.Notify(context.Background(), "x", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("notify after close: %v", err)
	}
}

func TestPeerGoneFailsPending(t *testing.T) {
	c, s := pair(t)
	got := make(chan struct{})
	s.Handle("hang", func(context.Context, *Request) (any, error) {
		close(got)
		select {}
	})
	s.Start()
	c.Start()
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "hang", nil, nil) }()
	<-got
	_ = s.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) || !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call hangs after peer close")
	}
}

func TestNotifyCancelledContext(t *testing.T) {
	c, _ := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Notify(ctx, "x", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// rawPeer lets tests speak raw frames to a Conn.
type rawPeer struct {
	r *bufio.Reader
	w io.Writer
}

func (p rawPeer) send(t *testing.T, body string) {
	t.Helper()
	if err := writeMessage(p.w, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func (p rawPeer) recv(t *testing.T) map[string]any {
	t.Helper()
	b, err := readMessage(p.r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStringIDsAndBadMessages(t *testing.T) {
	a, b := net.Pipe()
	c := NewConn(a)
	defer c.Close()
	defer b.Close()
	c.Handle("echo", func(_ context.Context, req *Request) (any, error) { return req.ID.String(), nil })
	c.Start()
	p := rawPeer{r: bufio.NewReader(b), w: b}

	p.send(t, `{"jsonrpc":"2.0","id":"abc","method":"echo"}`)
	m := p.recv(t)
	if m["id"] != "abc" || m["result"] != `"abc"` {
		t.Fatalf("got %v", m)
	}
	p.send(t, `{"jsonrpc":"2.0","id":7,"method":"echo"}`)
	if m := p.recv(t); m["id"] != float64(7) || m["result"] != "7" {
		t.Fatalf("got %v", m)
	}
	p.send(t, `not json`)
	if m := p.recv(t); m["error"] == nil {
		t.Fatalf("expected parse error, got %v", m)
	}
	// Responses with unknown or string ids are ignored.
	p.send(t, `{"jsonrpc":"2.0","id":"zzz","result":1}`)
	p.send(t, `{"jsonrpc":"2.0","id":12345,"result":1}`)
	p.send(t, `{"jsonrpc":"2.0","id":{},"method":"echo"}`)
	if m := p.recv(t); m["error"] == nil {
		t.Fatalf("expected parse error for bad id, got %v", m)
	}
	p.send(t, `{"jsonrpc":"2.0","id":8,"method":"echo"}`)
	if m := p.recv(t); m["id"] != float64(8) {
		t.Fatalf("got %v", m)
	}
}

func TestIDMarshal(t *testing.T) {
	for _, id := range []ID{NumberID(3), StringID("x")} {
		b, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var back ID
		if err := json.Unmarshal(b, &back); err != nil || back != id {
			t.Fatalf("roundtrip %v: %v %v", id, back, err)
		}
	}
}

func TestFraming(t *testing.T) {
	cases := map[string]string{
		"missing length":  "X-Foo: 1\r\n\r\n{}",
		"bad length":      "Content-Length: abc\r\n\r\n",
		"negative length": "Content-Length: -1\r\n\r\n",
		"malformed":       "garbage\r\n\r\n",
		"short body":      "Content-Length: 10\r\n\r\n{}",
		"eof in header":   "Content-Length: 2",
		"too large":       "Content-Length: 999999999999\r\n\r\n",
	}
	for name, in := range cases {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	in := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\ncontent-length: 2\r\n\r\n{}"
	b, err := readMessage(bufio.NewReader(strings.NewReader(in)))
	if err != nil || string(b) != "{}" {
		t.Fatalf("got %q %v", b, err)
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader(""))); err != io.EOF {
		t.Fatalf("empty: %v", err)
	}
	var buf bytes.Buffer
	if err := writeMessage(&buf, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "Content-Length: 7\r\n\r\n{\"a\":1}" {
		t.Fatalf("frame %q", buf.String())
	}
}

type failWriter struct{ io.ReadWriteCloser }

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestSendFailure(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := NewConn(failWriter{a})
	defer c.Close()
	c.Start()
	if err := c.Call(ctxT(t), "x", nil, nil); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("err = %v", err)
	}
	if err := c.Notify(ctxT(t), "x", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestLineFraming(t *testing.T) {
	in := "\n{\"a\":1}\r\n  \n{\"b\":2}"
	r := bufio.NewReaderSize(strings.NewReader(in), 16)
	b, err := LineFraming.ReadMessage(r)
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("first: %q %v", b, err)
	}
	b, err = LineFraming.ReadMessage(r)
	if err != nil || string(b) != `{"b":2}` {
		t.Fatalf("second: %q %v", b, err)
	}
	if _, err := LineFraming.ReadMessage(r); err != io.EOF {
		t.Fatalf("eof: %v", err)
	}
	long := strings.Repeat("x", 100)
	b, err = LineFraming.ReadMessage(bufio.NewReaderSize(strings.NewReader(long+"\n"), 16))
	if err != nil || string(b) != long {
		t.Fatalf("long line: %d %v", len(b), err)
	}
	var buf bytes.Buffer
	if err := LineFraming.WriteMessage(&buf, []byte(`{}`)); err != nil || buf.String() != "{}\n" {
		t.Fatalf("write %q %v", buf.String(), err)
	}
	buf.Reset()
	if err := HeaderFraming.WriteMessage(&buf, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if b, err := HeaderFraming.ReadMessage(bufio.NewReader(&buf)); err != nil || string(b) != "{}" {
		t.Fatalf("header roundtrip %q %v", b, err)
	}
}

func TestLineFramingConn(t *testing.T) {
	a, b := net.Pipe()
	c := NewConnFraming(a, LineFraming)
	defer c.Close()
	defer b.Close()
	c.Handle("echo", func(_ context.Context, req *Request) (any, error) { return string(req.Params), nil })
	c.Start()
	go func() { _, _ = b.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"echo","params":[1]}` + "\n")) }()
	line, err := bufio.NewReader(b).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, `"result":"[1]"`) {
		t.Fatalf("got %q", line)
	}
	if NewConnFraming(a, nil).framing != HeaderFraming {
		t.Fatal("nil framing should default to header framing")
	}
}
