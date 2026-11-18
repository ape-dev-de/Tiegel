package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
)

// syncBuffer is a bytes.Buffer safe for concurrent log writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type panicParams struct {
	N int `json:"n"`
}

func TestCallRecoversHandlerPanic(t *testing.T) {
	a := newTestApp(t)
	var logs syncBuffer
	a.log = slog.New(slog.NewTextHandler(&logs, nil))
	if err := Handle(a, "test.panic", "panics", func(ctx context.Context, p panicParams) (int, error) {
		s := []int{1, 2}
		return s[p.N], nil // index out of range for n >= 2
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	check := func(name string, err error) {
		t.Helper()
		if ErrorCode(err) != CodeInternal || !strings.Contains(err.Error(), "test.panic") {
			t.Fatalf("%s: want internal error, got %v", name, err)
		}
	}
	_, err := a.Call(ctx, "test.panic", json.RawMessage(`{"n":5}`))
	check("Call", err)
	_, err = a.CallJSON(ctx, "test.panic", panicParams{N: 5})
	check("CallJSON", err)
	ip := NewInProcess(a)
	var n int
	check("InProcess direct", ip.Call(ctx, "test.panic", panicParams{N: 5}, &n))
	check("InProcess JSON", ip.Call(ctx, "test.panic", map[string]int{"n": 5}, &n))
	check("InProcess raw", ip.Call(ctx, "test.panic", json.RawMessage(`{"n":5}`), &n))
	sess := a.NewSession(nil)
	_, err = sess.Call(ctx, "test.panic", json.RawMessage(`{"n":5}`))
	check("Session", err)
	// The method still works afterwards.
	if err := ip.Call(ctx, "test.panic", panicParams{N: 1}, &n); err != nil || n != 2 {
		t.Fatalf("after panic: %d %v", n, err)
	}
	out := logs.String()
	if !strings.Contains(out, "method panicked") || !strings.Contains(out, "panic_test.go") {
		t.Fatalf("log lacks panic with stack: %s", out)
	}
}

func TestCallSurvivesPanickingHook(t *testing.T) {
	a := newTestApp(t)
	var logs syncBuffer
	a.log = slog.New(slog.NewTextHandler(&logs, nil))
	filter := hooks.NewFilter[string]("test.filter")
	filter.On(a.Hooks(), 0, func(ctx context.Context, v string) (string, error) {
		var m map[string]int
		m["x"] = 1 // nil map write panics
		return v, nil
	}, hooks.WithOwner("plugin-x"))
	if err := Handle(a, "test.hooked", "runs a filter", func(ctx context.Context, p panicParams) (string, error) {
		v, err := filter.Apply(ctx, a.Hooks(), "in")
		if err != nil {
			return v, Errorf(CodeFailed, "%v", err)
		}
		return v, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.CallJSON(context.Background(), "test.hooked", nil)
	if ErrorCode(err) != CodeFailed || !strings.Contains(err.Error(), "hook panicked") {
		t.Fatalf("want hook failure, got %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, "hook panicked") || !strings.Contains(out, "plugin-x") || !strings.Contains(out, "panic_test.go") {
		t.Fatalf("log lacks hook panic with stack: %s", out)
	}
}
