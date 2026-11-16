package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// recorder collects bus events.
type recorder struct {
	mu     sync.Mutex
	events []bus.Event
}

func record(t *testing.T, a *App, pattern string) *recorder {
	r := &recorder{}
	t.Cleanup(a.Bus().Subscribe(pattern, func(e bus.Event) {
		r.mu.Lock()
		r.events = append(r.events, e)
		r.mu.Unlock()
	}))
	return r
}

func (r *recorder) of(topic string) []bus.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []bus.Event
	for _, e := range r.events {
		if e.Topic == topic {
			out = append(out, e)
		}
	}
	return out
}

func (r *recorder) wait(t *testing.T, topic string, n int) []bus.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if evs := r.of(topic); len(evs) >= n {
			return evs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %d × %s (have %d)", n, topic, len(r.of(topic)))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// demoModule provides "demo.echo" and claims "demo.*".
type demoModule struct {
	failStart bool
}

func (demoModule) ID() string          { return "demo" }
func (demoModule) DependsOn() []string { return nil }
func (demoModule) Claims() []string    { return []string{"demo.*", "other.exact"} }
func (m demoModule) Start(ctx module.Context) error {
	if m.failStart {
		return errors.New("boom")
	}
	p := NewProvider(ctx)
	Add(p, "demo.echo", "Echo", func(_ context.Context, e echo) (echo, error) { return e, nil })
	Add(p, "demo.echo", "duplicate", func(_ context.Context, e echo) (echo, error) { return e, nil })
	Add(p, "demo.never", "skipped after the first error", func(_ context.Context, e echo) (echo, error) { return e, nil })
	if p.Err() == nil {
		return errors.New("duplicate not reported")
	}
	return nil
}
func (demoModule) Stop() error { return nil }

func TestModuleMethodsGating(t *testing.T) {
	a := newTestApp(t)
	if err := a.AddModule(demoModule{}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddModule(demoModule{}); err == nil {
		t.Fatal("duplicate module accepted")
	}
	var e echo
	call(t, a, "demo.echo", echo{Text: "hi"}, &e)
	if e.Text != "hi" {
		t.Fatal(e)
	}
	var info []MethodInfo
	call(t, a, "app.methods", nil, &info)
	found := false
	for _, m := range info {
		if m.Name == "demo.echo" {
			found = m.Module == "demo" && !m.Disabled
		}
	}
	if !found {
		t.Fatalf("method info %+v", info)
	}
	call(t, a, "modules.disable", ModuleParams{ID: "demo"}, nil)
	err := tryCall(a, "demo.echo", echo{}, nil)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeModuleDisabled || ae.Data.(map[string]string)["module"] != "demo" {
		t.Fatalf("disabled: %v", err)
	}
	// Claimed but never provided (module did not run this method).
	wantCode(t, a, "demo.never", nil, CodeModuleDisabled)
	wantCode(t, a, "other.exact", nil, CodeModuleDisabled)
	wantCode(t, a, "other.nope", nil, CodeMethodNotFound)
	call(t, a, "modules.enable", ModuleParams{ID: "demo"}, nil)
	call(t, a, "demo.echo", echo{Text: "again"}, &e)
	if e.Text != "again" {
		t.Fatal(e)
	}
	// JSON-RPC carries the error data.
	call(t, a, "modules.disable", ModuleParams{ID: "demo"}, nil)
	rpcErr := toRPCError(tryCall(a, "demo.echo", nil, nil))
	if rpcErr.Code != CodeModuleDisabled || !strings.Contains(string(rpcErr.Data), `"module":"demo"`) {
		t.Fatalf("rpc %+v", rpcErr)
	}
	// A failing module is reported but does not fail AddModule.
	b := newTestApp(t)
	if err := b.AddModule(failing{}); err != nil {
		t.Fatal(err)
	}
	var list []ModuleInfo
	call(t, b, "modules.list", nil, &list)
	if len(list) != 1 || list[0].Running || list[0].Error == "" {
		t.Fatalf("failing %+v", list)
	}
	if err := b.AddModule(failing{id: "bad id"}); err == nil {
		t.Fatal("invalid id accepted")
	}
	// Provide outside an App context fails.
	if err := Provide(plainCtx{}, "x.y", "", func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }); err == nil {
		t.Fatal("Provide without App")
	}
	if _, ok := AppFrom(plainCtx{}); ok {
		t.Fatal("AppFrom on plain context")
	}
}

type failing struct{ id string }

func (f failing) ID() string {
	if f.id != "" {
		return f.id
	}
	return "failing"
}
func (failing) DependsOn() []string            { return nil }
func (failing) Start(ctx module.Context) error { return errors.New("cannot start") }
func (failing) Stop() error                    { return nil }

type plainCtx struct{ module.Context }

func (plainCtx) ModuleID() string { return "plain" }

func TestDocEditEvents(t *testing.T) {
	a := newTestApp(t)
	rec := record(t, a, "*")
	openProject(t, a, map[string]string{"a.php": "<?php\n$ä = '😀x';\nend\n"})
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	fo := rec.wait(t, TopicFileOpened, 1)[0].Payload.(FileOpenedEvent)
	if fo.Path != "a.php" || fo.Language != "php" || !filepath.IsAbs(fo.File) {
		t.Fatalf("file.opened %+v", fo)
	}
	text := "<?php\n$ä = '😀x';\nend\n"
	apply := func(ev DocEditEvent) {
		var err error
		text, err = protocol.ApplyChanges(text, []protocol.TextDocumentContentChangeEvent{{
			Range: &protocol.Range{
				Start: protocol.Position{Line: ev.Range.Start.Line, Character: ev.Range.Start.Character},
				End:   protocol.Position{Line: ev.Range.End.Line, Character: ev.Range.End.Character},
			},
			Text: ev.Text,
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Replace "x" after the emoji (UTF-16 column 9), then a multi-line range.
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Start: &Pos{Line: 2, Col: 9}, End: &Pos{Line: 2, Col: 10}, Text: "y\nz"}, nil)
	call(t, a, "doc.delete", DocDeleteParams{Path: "a.php", Start: Pos{Line: 2, Col: 7}, End: Pos{Line: 4, Col: 2}}, nil)
	call(t, a, "doc.undo", PathParams{Path: "a.php"}, nil)
	// GUI path: direct buffer edit under the document lock.
	d, err := a.Document("a.php")
	if err != nil {
		t.Fatal(err)
	}
	d.Lock()
	_, _ = d.Buffer().Insert(0, []byte("#!\n"))
	d.Unlock()
	evs := rec.wait(t, TopicDocEdited, 4)
	for i, e := range evs {
		ev := e.Payload.(DocEditEvent)
		if ev.Version != uint64(i+1) || ev.Path != "a.php" {
			t.Fatalf("edit %d: %+v", i, ev)
		}
		apply(ev)
	}
	if got := string(d.Snapshot().Text); text != got {
		t.Fatalf("LSP replay %q != buffer %q", text, got)
	}
	if len(rec.wait(t, TopicDocChanged, 4)) < 4 {
		t.Fatal("doc.changed per edit")
	}
	if d.Language() != "php" || d.Path() == "" {
		t.Fatal("doc accessors")
	}

	// file.saved for manual saves and autosave (commit.before guard).
	call(t, a, "doc.save", PathParams{Path: "a.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{}, Text: "x"}, nil)
	if err := HookCommitBefore.Check(context.Background(), a.Hooks(), CommitRequest{Message: "m"}); err != nil {
		t.Fatal(err)
	}
	saved := rec.wait(t, TopicFileSaved, 2)
	if s := saved[1].Payload.(FileSavedEvent); s.Trigger != "beforeCommit" || s.Path != "a.php" {
		t.Fatalf("autosave %+v", s)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{}, Text: "y"}, nil)
	if err := HookDebugBefore.Check(context.Background(), a.Hooks(), DebugRequest{}); err != nil {
		t.Fatal(err)
	}
	if s := rec.wait(t, TopicFileSaved, 3)[2].Payload.(FileSavedEvent); s.Trigger != "beforeDebug" {
		t.Fatalf("debug autosave %+v", s)
	}
	// Closing the project closes documents with events.
	call(t, a, "project.close", nil, nil)
	if c := rec.wait(t, TopicDocClosed, 1)[0].Payload.(DocCloseResult); c.Discarded {
		t.Fatalf("closed %+v", c)
	}
	if p := rec.wait(t, TopicProjectClosed, 1)[0].Payload.(ProjectEvent); p.Name == "" {
		t.Fatalf("project event %+v", p)
	}
}

func TestHostHelpers(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.RequireProject(); ErrorCode(err) != CodeNoProject {
		t.Fatal(err)
	}
	if a.SettingsStore() != a.Settings() || a.Options().AppVersion != "test" {
		t.Fatal("global store / options")
	}
	root := openProject(t, a, map[string]string{"a.txt": "one\ntwo ä\n"})
	if r, err := a.RequireProject(); err != nil || r != root {
		t.Fatal(r, err)
	}
	if a.SettingsStore() != a.ProjectSettings() {
		t.Fatal("project store")
	}
	if p, _ := a.ResolvePath("a.txt"); a.DisplayPath(p) != "a.txt" {
		t.Fatal(p)
	}
	text, open, err := a.ReadText("a.txt")
	if err != nil || open || string(text) != "one\ntwo ä\n" {
		t.Fatal(string(text), open, err)
	}
	if _, _, err := a.ReadText("missing"); ErrorCode(err) != CodeNotFound {
		t.Fatal(err)
	}
	if _, _, err := a.ReadText(""); ErrorCode(err) != CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := a.Document("a.txt"); ErrorCode(err) != CodeNotFound {
		t.Fatal(err)
	}
	call(t, a, "doc.open", DocOpenParams{Path: "a.txt"}, nil)
	if _, open, _ := a.ReadText("a.txt"); !open {
		t.Fatal("open doc not used")
	}
	st, err := a.EditDocument("a.txt", 0, 0, 3, "ONE")
	if err != nil || st.Version != 1 {
		t.Fatal(st, err)
	}
	if st, _ := a.EditDocument("a.txt", 0, 0, 3, "ONE"); st.Version != 1 {
		t.Fatal("no-op edit changed the version")
	}
	if _, err := a.EditDocument("a.txt", 0, 5, 2, ""); ErrorCode(err) != CodeInvalidParams {
		t.Fatal(err)
	}
	if _, err := a.EditDocument("a.txt", 7, 0, 0, "x"); ErrorCode(err) != CodeConflict {
		t.Fatal(err)
	}
	call(t, a, "doc.undo", PathParams{Path: "a.txt"}, nil)
	if docs := a.Documents(); len(docs) != 1 || string(docs[0].Text) != "one\ntwo ä\n" || docs[0].Path != "a.txt" {
		t.Fatalf("documents %+v", docs)
	}

	src := []byte("one\ntwo ä!\nend")
	for _, c := range []struct {
		p    Pos
		want int
		code int
	}{
		{Pos{Offset: 2}, 2, 0},
		{Pos{Line: 2, Col: 6}, 10, 0},
		{Pos{Line: 2}, 4, 0},
		{Pos{Line: 3, Col: 99}, 15, 0},
		{Pos{Line: 4}, 0, CodeInvalidParams},
		{Pos{Offset: 99}, 0, CodeInvalidParams},
	} {
		got, err := c.p.ByteOffset(src)
		if c.code != 0 {
			if ErrorCode(err) != c.code {
				t.Errorf("%+v: err %v", c.p, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%+v: %d %v, want %d", c.p, got, err, c.want)
		}
	}
	if p := PosAt(src, 10); p != (TextPos{Offset: 10, Line: 2, Col: 6}) {
		t.Fatalf("PosAt %+v", p)
	}
	if p := PosAt(src, -1); p.Offset != 0 || p.Line != 1 {
		t.Fatal(p)
	}
	if p := PosAt(src, 99); p.Offset != len(src) || p.Line != 3 {
		t.Fatal(p)
	}
	if utf16Len("a😀") != 3 {
		t.Fatal("utf16Len")
	}
	_ = hooks.DefaultTimeout
}
