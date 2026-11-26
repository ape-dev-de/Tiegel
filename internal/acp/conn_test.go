package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// pipePair connects two Conns.
func pipePair(t *testing.T, aReq RequestHandler, aNote NotificationHandler, bReq RequestHandler, bNote NotificationHandler) (*Conn, *Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a := NewConn(ar, aw, aReq, aNote)
	b := NewConn(br, bw, bReq, bNote)
	t.Cleanup(func() {
		_ = aw.Close()
		_ = bw.Close()
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

func TestConnErrorsAndNotifications(t *testing.T) {
	notes := make(chan string, 4)
	a, b := pipePair(t, nil, nil, func(_ context.Context, method string, p json.RawMessage) (any, error) {
		switch method {
		case "plain":
			return nil, errors.New("boom")
		case "unmarshalable":
			return make(chan int), nil
		case "echo":
			return p, nil
		}
		return nil, &Error{Code: 7, Message: "custom", Data: json.RawMessage(`{"x":1}`)}
	}, func(method string, _ json.RawMessage) { notes <- method })

	err := a.Call(context.Background(), "plain", nil, nil)
	if ErrorCode(err) != CodeInternalError || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("plain: %v", err)
	}
	if err := a.Call(context.Background(), "unmarshalable", nil, nil); ErrorCode(err) != CodeInternalError {
		t.Fatalf("unmarshalable: %v", err)
	}
	err = a.Call(context.Background(), "other", nil, nil)
	if ErrorCode(err) != 7 || !strings.Contains(err.Error(), `{"x":1}`) {
		t.Fatalf("custom: %v", err)
	}
	var out map[string]int
	if err := a.Call(context.Background(), "echo", map[string]int{"n": 3}, &out); err != nil || out["n"] != 3 {
		t.Fatalf("echo %v %v", err, out)
	}
	var wrong []string
	if err := a.Call(context.Background(), "echo", map[string]int{"n": 3}, &wrong); err == nil {
		t.Fatal("decode error expected")
	}
	if err := a.Call(context.Background(), "echo", make(chan int), nil); err == nil {
		t.Fatal("encode error expected")
	}
	if err := a.Notify("hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Notify("x", make(chan int)); err == nil {
		t.Fatal("encode error expected")
	}
	select {
	case m := <-notes:
		if m != "hello" {
			t.Fatal(m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification lost")
	}
	// b has no request handler: method not found.
	if err := b.Call(context.Background(), "x", nil, nil); ErrorCode(err) != CodeMethodNotFound {
		t.Fatalf("no handler: %v", err)
	}
	if ErrorCode(errors.New("x")) != 0 {
		t.Fatal("plain error code")
	}
	if (&Error{Code: 1, Message: "m"}).Error() != "m (code 1)" {
		t.Fatal("error text")
	}
}

func TestConnSkipsGarbageAndEnds(t *testing.T) {
	in := strings.NewReader("\nnot json\n{\"jsonrpc\":\"2.0\",\"id\":99,\"result\":1}\n")
	var out bytes.Buffer
	c := NewConn(in, &out, nil, nil)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("conn did not end")
	}
	if !errors.Is(c.Err(), io.EOF) {
		t.Fatalf("err %v", c.Err())
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after end: %v", err)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestConnWriteFailure(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	c := NewConn(r, failWriter{}, nil, nil)
	defer c.Close()
	if err := c.Call(context.Background(), "x", nil, nil); err == nil || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("err %v", err)
	}
}

func TestPeerExitFailsPending(t *testing.T) {
	r, w := io.Pipe()
	pr, pw := io.Pipe()
	c := NewConn(r, pw, nil, nil)
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "x", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	_ = w.CloseWithError(errors.New("agent crashed"))
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), "agent crashed") {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call not failed")
	}
}

func TestValueAndTail(t *testing.T) {
	var v struct {
		A, B, C Value
	}
	if err := json.Unmarshal([]byte(`{"A":"x","B":true,"C":null}`), &v); err != nil || v.A != "x" || v.B != "true" || v.C != "" {
		t.Fatalf("%v %+v", err, v)
	}
	tb := &tailBuffer{max: 10}
	_, _ = tb.Write([]byte("line one\nline two\n"))
	if got := tb.String(); got != "line two\n" {
		t.Fatalf("tail %q", got)
	}
	var u SessionUpdate
	if err := json.Unmarshal([]byte(`[1]`), &u); err == nil {
		t.Fatal("array accepted")
	}
}
