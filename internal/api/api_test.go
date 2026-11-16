package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/lsp/jsonrpc2"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// newTestApp returns an App that only touches t.TempDir() and does not look
// at the machine's PATH.
func newTestApp(t *testing.T, ext ...func(*App) error) *App {
	t.Helper()
	home := t.TempDir()
	a, err := New(Options{
		ConfigDir:  filepath.Join(t.TempDir(), "config"),
		Interp:     interpOptions(nil),
		LSP:        servers.DetectOptions{HomeDir: home, PathEnv: "", ComposerHome: filepath.Join(home, "composer"), NoSystemDirs: true},
		ComposeEnv: map[string]string{},
		GOOS:       "linux",
		AppVersion: "test",
		Extensions: ext,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// call invokes method with params and decodes the result into out.
func call(t *testing.T, a *App, method string, params, out any) {
	t.Helper()
	if err := tryCall(a, method, params, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func tryCall(a *App, method string, params, out any) error {
	res, err := a.CallJSON(context.Background(), method, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	// Reset out first: decoding into a reused slice keeps omitted fields.
	v := reflect.ValueOf(out).Elem()
	v.Set(reflect.Zero(v.Type()))
	return json.Unmarshal(b, out)
}

// wantCode asserts that calling method fails with code.
func wantCode(t *testing.T, a *App, method string, params any, code int) {
	t.Helper()
	err := tryCall(a, method, params, nil)
	if err == nil {
		t.Fatalf("%s: expected error code %d, got success", method, code)
	}
	if got := ErrorCode(err); got != code {
		t.Fatalf("%s: code = %d, want %d (%v)", method, got, code, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMethodTable(t *testing.T) {
	a := newTestApp(t)
	names := map[string]bool{}
	for _, m := range a.Methods() {
		names[m.Name] = true
		if m.Summary == "" {
			t.Errorf("%s has no summary", m.Name)
		}
	}
	for _, want := range []string{
		"app.info", "app.methods", "events.subscribe", "project.open", "project.info", "project.close",
		"files.list", "files.read", "files.detectLanguage", "doc.open", "doc.close", "doc.insert", "doc.delete",
		"doc.replace", "doc.undo", "doc.redo", "doc.save", "doc.text", "history.list", "history.restore",
		"search.inDoc", "interp.list", "interp.select", "interp.version", "compose.detect", "lsp.servers",
		"debug.listen", "debug.stop", "debug.sessions", "modules.list", "modules.enable", "modules.disable",
	} {
		if !names[want] {
			t.Errorf("method %s not registered", want)
		}
	}
	if err := a.Register(Method{Name: "app.info", Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}); err == nil {
		t.Error("duplicate registration accepted")
	}
	if err := a.Register(Method{Name: "nodot", Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}); err == nil {
		t.Error("name without namespace accepted")
	}
	if err := a.Register(Method{Name: "x.y"}); err == nil {
		t.Error("nil handler accepted")
	}
	var info AppInfo
	call(t, a, "app.info", nil, &info)
	if info.Version != "test" || info.APIVersion != Version || info.Methods != len(names) || info.Project != "" {
		t.Fatalf("info %+v", info)
	}
	var ms []MethodInfo
	call(t, a, "app.methods", nil, &ms)
	if len(ms) != len(names) {
		t.Fatalf("app.methods returned %d", len(ms))
	}
}

type echo struct {
	Text string `json:"text"`
}

func TestExtensionsAndErrors(t *testing.T) {
	a := newTestApp(t, func(a *App) error {
		return Handle(a, "demo.echo", "Echo", func(_ context.Context, p echo) (echo, error) {
			if p.Text == "fail" {
				return echo{}, errors.New("plain failure")
			}
			return p, nil
		})
	})
	var out echo
	call(t, a, "demo.echo", echo{Text: "hi"}, &out)
	if out.Text != "hi" {
		t.Fatalf("echo %+v", out)
	}
	wantCode(t, a, "demo.nope", nil, CodeMethodNotFound)
	if _, err := a.Call(context.Background(), "demo.echo", json.RawMessage(`{"txt":"typo"}`)); ErrorCode(err) != CodeInvalidParams || !IsUsageError(err) {
		t.Fatalf("unknown field: %v", err)
	}
	if _, err := a.Call(context.Background(), "demo.echo", json.RawMessage(`[1`)); ErrorCode(err) != CodeInvalidParams {
		t.Fatalf("bad json: %v", err)
	}
	if res, err := a.Call(context.Background(), "demo.echo", json.RawMessage(` null `)); err != nil || res.(echo).Text != "" {
		t.Fatalf("null params: %v %v", res, err)
	}
	_, err := a.CallJSON(context.Background(), "demo.echo", echo{Text: "fail"})
	if ErrorCode(err) != CodeFailed || IsUsageError(err) || err.Error() != "plain failure" {
		t.Fatalf("plain error: %v", err)
	}
	if _, err := a.CallJSON(context.Background(), "demo.echo", func() {}); ErrorCode(err) != CodeInvalidParams {
		t.Fatalf("unencodable params: %v", err)
	}
	inner := errors.New("inner")
	e := Errorf(CodeNotFound, "wrapped: %w", inner)
	if !errors.Is(e, inner) || e.Error() != "wrapped: inner" {
		t.Fatalf("Errorf: %v", e)
	}

	_, err = New(Options{ConfigDir: t.TempDir(), Extensions: []func(*App) error{
		func(*App) error { return errors.New("ext broken") },
	}})
	if err == nil || !strings.Contains(err.Error(), "ext broken") {
		t.Fatalf("failing extension: %v", err)
	}
}

func TestAccessors(t *testing.T) {
	a := newTestApp(t)
	if a.ConfigDir() == "" || a.Logger() == nil || a.Hooks() == nil || a.Bus() == nil || a.Settings() == nil || a.Modules() == nil {
		t.Fatal("accessors")
	}
	if a.ProjectRoot() != "" || a.ProjectSettings() != nil {
		t.Fatal("no project expected")
	}
}

func TestSessionEvents(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var got []Event
	s := a.NewSession(func(e Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	ctx := context.Background()
	if topics := s.Subscribe("demo.*", " ", "demo.*"); len(topics) != 1 || topics[0] != "demo.*" {
		t.Fatalf("topics %v", topics)
	}
	if _, err := s.Call(ctx, "events.subscribe", json.RawMessage(`{"topics":["other"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(ctx, "events.subscribe", json.RawMessage(`{}`)); ErrorCode(err) != CodeInvalidParams {
		t.Fatalf("empty topics: %v", err)
	}
	wantCode(t, a, "events.subscribe", TopicsParams{Topics: []string{"x"}}, CodeFailed)
	wantCode(t, a, "events.unsubscribe", TopicsParams{}, CodeFailed)
	wantCode(t, a, "events.publish", PublishParams{Topic: "a.*"}, CodeInvalidParams)

	call(t, a, "events.publish", PublishParams{Topic: "demo.x", Payload: json.RawMessage(`{"n":1}`)}, nil)
	call(t, a, "events.publish", PublishParams{Topic: "other"}, nil)
	a.Bus().Publish("demo.func", func() {}) // not JSON: sent as string
	a.Bus().Publish("ignored", 1)
	if err := a.Bus().Drain(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	// Different subscriptions deliver independently: compare by topic.
	byTopic := map[string]string{}
	for _, e := range got {
		byTopic[e.Topic] = string(e.Payload)
	}
	if len(got) != 3 || byTopic["demo.x"] != `{"n":1}` || byTopic["other"] != "" || !strings.HasPrefix(byTopic["demo.func"], `"`) {
		t.Fatalf("events %+v", got)
	}
	mu.Unlock()

	res, err := s.Call(ctx, "events.unsubscribe", json.RawMessage(`{"topics":["other"]}`))
	if err != nil || len(res.(TopicsResult).Topics) != 1 {
		t.Fatalf("unsubscribe: %v %v", res, err)
	}
	s.Close()
	if s.Subscribe("x") != nil {
		t.Fatal("subscribe after close")
	}
	if sess, ok := SessionFrom(ctx); ok || sess != nil {
		t.Fatal("no session expected")
	}
	a.NewSession(nil).notify(Event{}) // nil notify is replaced by a no-op
}

// rpcPeer drives Serve over a net.Pipe with line framing.
type rpcPeer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func (p *rpcPeer) send(line string) {
	p.t.Helper()
	if _, err := p.conn.Write([]byte(line + "\n")); err != nil {
		p.t.Fatal(err)
	}
}

func (p *rpcPeer) recv() map[string]any {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := p.r.ReadString('\n')
	if err != nil {
		p.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		p.t.Fatalf("%q: %v", line, err)
	}
	return m
}

// recvResponse skips notifications and returns the next response.
func (p *rpcPeer) recvResponse() map[string]any {
	p.t.Helper()
	for {
		m := p.recv()
		if _, ok := m["id"]; ok {
			return m
		}
	}
}

func TestServeLineFraming(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.php"), "<?php\n")
	c1, c2 := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, c1, ServeOptions{}) }()
	p := &rpcPeer{t: t, conn: c2, r: bufio.NewReader(c2)}

	p.send(`{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"topics":["doc.*"]}}`)
	if m := p.recvResponse(); m["error"] != nil {
		t.Fatalf("subscribe: %v", m)
	}
	p.send(`{"jsonrpc":"2.0","id":2,"method":"project.open","params":{"path":` + quote(dir) + `}}`)
	p.recvResponse()
	p.send(`{"jsonrpc":"2.0","id":3,"method":"doc.open","params":{"path":"a.php"}}`)
	sawEvent := false
	for i := 0; i < 2; i++ {
		m := p.recv()
		if m["method"] == NotifyMethod {
			sawEvent = m["params"].(map[string]any)["topic"] == TopicDocOpened
		}
	}
	if !sawEvent {
		t.Fatal("no doc.opened notification")
	}
	p.send(`{"jsonrpc":"2.0","id":4,"method":"doc.insert","params":{"path":"a.php","at":{"offset":99}}}`)
	if m := p.recvResponse(); m["error"].(map[string]any)["code"] != float64(CodeInvalidParams) {
		t.Fatalf("expected invalid params, got %v", m)
	}
	p.send(`{"jsonrpc":"2.0","id":5,"method":"no.such"}`)
	if m := p.recvResponse(); m["error"].(map[string]any)["code"] != float64(CodeMethodNotFound) {
		t.Fatalf("expected method not found, got %v", m)
	}
	p.send(`{"jsonrpc":"2.0","method":"events.publish","params":{"topic":"doc.x"}}`) // notification: no response
	if m := p.recv(); m["method"] != NotifyMethod {
		t.Fatalf("expected event, got %v", m)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("serve: %v", err)
	}
	_ = c2.Close()
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// bufRWC is a ReadWriteCloser over fixed input, collecting the output.
type bufRWC struct {
	io.Reader
	mu  sync.Mutex
	out bytes.Buffer
}

func (b *bufRWC) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Write(p)
}

func (b *bufRWC) Close() error { return nil }

func TestServePipelineWaitsForResponses(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.php"), "<?php\n")
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"project.open","params":{"path":` + quote(dir) + `}}`,
		`{"jsonrpc":"2.0","id":2,"method":"events.subscribe","params":{"topics":["doc.saved"]}}`,
		`{"jsonrpc":"2.0","id":3,"method":"doc.open","params":{"path":"a.php"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"doc.insert","params":{"path":"a.php","at":{"line":2},"text":"echo 1;\n"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"doc.save","params":{"path":"a.php"}}`,
		`garbage`,
	}, "\n") + "\n"
	rwc := &bufRWC{Reader: strings.NewReader(in)}
	if err := a.Serve(context.Background(), rwc, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	out := rwc.out.String()
	for i := 1; i <= 5; i++ {
		if !strings.Contains(out, `"id":`+string(rune('0'+i))+`,"result"`) {
			t.Errorf("response %d missing or failed in:\n%s", i, out)
		}
	}
	if !strings.Contains(out, `"code":-32700`) {
		t.Errorf("no parse error for garbage:\n%s", out)
	}
	if !strings.Contains(out, `"topic":"doc.saved"`) {
		t.Errorf("doc.saved event missing:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.php"))
	if string(data) != "<?php\necho 1;\n" {
		t.Fatalf("file %q", data)
	}
}

func TestServeHeaderFramingConcurrent(t *testing.T) {
	a := newTestApp(t)
	c1, c2 := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- a.Serve(context.Background(), c1, ServeOptions{Framing: jsonrpc2.HeaderFraming, Concurrent: true})
	}()
	client := jsonrpc2.NewConn(c2)
	client.Start()
	var info AppInfo
	if err := client.Call(context.Background(), "app.info", nil, &info); err != nil || info.APIVersion != Version {
		t.Fatalf("app.info: %+v %v", info, err)
	}
	err := client.Call(context.Background(), "doc.text", map[string]string{"path": "/nope"}, nil)
	var rpcErr *jsonrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Fatalf("doc.text: %v", err)
	}
	_ = client.Close()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestToRPCError(t *testing.T) {
	if e := toRPCError(context.Canceled); e.Code != jsonrpc2.CodeRequestCancelled {
		t.Fatal(e)
	}
	if e := toRPCError(errors.New("x")); e.Code != CodeFailed {
		t.Fatal(e)
	}
}
