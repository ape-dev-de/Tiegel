package client_test

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/apitest"
)

// The same suite runs against every api.Caller implementation.
func TestCallerSuite(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": "<?php\necho 1;\n"})
	callers := apitest.Callers(t, a)
	names := make([]string, 0, len(callers))
	for n := range callers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		c := callers[name]
		t.Run(name, func(t *testing.T) {
			t.Run("calls", func(t *testing.T) { suiteCalls(t, c) })
			t.Run("errors", func(t *testing.T) { suiteErrors(t, c) })
			t.Run("events", func(t *testing.T) { suiteEvents(t, c) })
		})
	}
}

func suiteCalls(t *testing.T, c api.Caller) {
	ctx := context.Background()
	vi, err := api.Call[api.VersionInfo](ctx, c, "app.version", struct{}{})
	if err != nil || vi.APIVersion != api.Version {
		t.Fatalf("app.version: %+v %v", vi, err)
	}
	// Pointer params, map result, raw JSON params and result, nil result.
	var m map[string]any
	if err := c.Call(ctx, "app.version", nil, &m); err != nil || m["apiVersion"] != api.Version {
		t.Fatalf("map result: %v %v", m, err)
	}
	var raw json.RawMessage
	if err := c.Call(ctx, "doc.open", json.RawMessage(`{"path":"a.php"}`), &raw); err != nil || len(raw) == 0 {
		t.Fatalf("raw: %s %v", raw, err)
	}
	if err := c.Call(ctx, "doc.list", nil, nil); err != nil {
		t.Fatal(err)
	}
	open, err := api.Call[api.DocOpenResult](ctx, c, "doc.open", &api.DocOpenParams{Path: "a.php", Text: true})
	if err != nil || open.Text != "<?php\necho 1;\n" || !open.AlreadyOpen {
		t.Fatalf("doc.open: %+v %v", open, err)
	}
	st, err := api.Call[api.DocState](ctx, c, "doc.insert", api.DocInsertParams{
		Path: "a.php", At: api.Pos{Offset: 0}, Text: "#", Version: api.VersionOf(open.Version),
	})
	if err != nil || st.Version != open.Version+1 {
		t.Fatalf("doc.insert: %+v %v", st, err)
	}
	txt, err := api.Call[api.DocText](ctx, c, "doc.text", api.DocTextParams{Path: "a.php"})
	if err != nil || txt.Text[0] != '#' || txt.Version != st.Version {
		t.Fatalf("doc.text: %+v %v", txt, err)
	}
	if _, err := api.Call[api.DocState](ctx, c, "doc.delete", api.DocDeleteParams{Path: "a.php", Start: api.Pos{Offset: 0}, End: api.Pos{Offset: 1}}); err != nil {
		t.Fatal(err)
	}
}

func suiteErrors(t *testing.T, c api.Caller) {
	ctx := context.Background()
	if err := c.Call(ctx, "doc.open", api.DocOpenParams{Path: "a.php"}, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method string
		params any
		code   int
	}{
		{"nope.nothing", nil, api.CodeMethodNotFound},
		{"doc.open", map[string]any{"bogus": 1}, api.CodeInvalidParams},
		{"doc.text", api.DocTextParams{Path: "missing.php"}, api.CodeNotFound},
		{"doc.insert", api.DocInsertParams{Path: "a.php", Text: "x", Version: api.VersionOf(999)}, api.CodeConflict},
	}
	for _, tc := range cases {
		err := c.Call(ctx, tc.method, tc.params, nil)
		if api.ErrorCode(err) != tc.code {
			t.Errorf("%s: got %v (code %d), want %d", tc.method, err, api.ErrorCode(err), tc.code)
		}
	}
	// Conflict errors carry the current version.
	err := c.Call(ctx, "doc.insert", api.DocInsertParams{Path: "a.php", Text: "x", Version: api.VersionOf(999)}, nil)
	var e *api.Error
	if !asError(err, &e) || e.Data == nil {
		t.Fatalf("conflict data: %#v", err)
	}
	// A result of the wrong shape is a decoding error, not a panic.
	var wrong []int
	if err := c.Call(ctx, "app.version", nil, &wrong); err == nil {
		t.Fatal("decoding into []int should fail")
	}
}

func asError(err error, target **api.Error) bool {
	e, ok := err.(*api.Error)
	if ok {
		*target = e
	}
	return ok
}

// recorder collects events of one subscription.
type recorder struct {
	mu  sync.Mutex
	got []api.Event
}

func (r *recorder) add(e api.Event) {
	r.mu.Lock()
	r.got = append(r.got, e)
	r.mu.Unlock()
}

func (r *recorder) topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.got))
	for i, e := range r.got {
		out[i] = e.Topic
	}
	return out
}

func (r *recorder) waitFor(t *testing.T, n int) []api.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.got) >= n {
			out := append([]api.Event(nil), r.got...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout: got %v, want %d events", r.topics(), n)
	return nil
}

func suiteEvents(t *testing.T, c api.Caller) {
	ctx := context.Background()
	if _, err := c.Subscribe(ctx, []string{" "}, nil); api.ErrorCode(err) != api.CodeInvalidParams {
		t.Fatalf("empty topics: %v", err)
	}
	var all, edited recorder
	// Overlapping patterns within one subscription: each event once.
	cancelAll, err := c.Subscribe(ctx, []string{"demo.*", "demo.a", "*"}, all.add)
	if err != nil {
		t.Fatal(err)
	}
	cancelEdited, err := c.Subscribe(ctx, []string{"demo.a"}, edited.add)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		topic := "demo.a"
		if i%2 == 1 {
			topic = "demo.b"
		}
		payload, _ := json.Marshal(map[string]int{"n": i})
		if err := c.Call(ctx, "events.publish", api.PublishParams{Topic: topic, Payload: payload}, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := all.waitFor(t, 20)
	for i, e := range got[:20] {
		var p struct{ N int }
		if err := json.Unmarshal(e.Payload, &p); err != nil || p.N != i {
			t.Fatalf("event %d out of order: %s %s", i, e.Topic, e.Payload)
		}
	}
	edited.waitFor(t, 10)
	cancelEdited()
	cancelEdited() // idempotent
	if err := c.Call(ctx, "events.publish", api.PublishParams{Topic: "demo.a"}, nil); err != nil {
		t.Fatal(err)
	}
	all.waitFor(t, 21)
	time.Sleep(20 * time.Millisecond)
	if n := len(edited.topics()); n != 10 {
		t.Fatalf("cancelled subscription got %d events", n)
	}
	cancelAll()
}
