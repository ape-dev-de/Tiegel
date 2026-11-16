package docsync_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/apitest"
	"github.com/ape-dev-de/tiegel/internal/api/docsync"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

const initial = "<?php\necho 1;\n"

func open(t *testing.T, c api.Caller, opts docsync.Options) *docsync.Mirror {
	t.Helper()
	m, err := docsync.Open(context.Background(), c, "a.php", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func serverText(t *testing.T, a *api.App) api.DocText {
	t.Helper()
	txt, err := api.Call[api.DocText](context.Background(), api.NewInProcess(a), "doc.text", api.DocTextParams{Path: "a.php"})
	if err != nil {
		t.Fatal(err)
	}
	return txt
}

func waitSynced(t *testing.T, a *api.App, ms ...*docsync.Mirror) {
	t.Helper()
	want := serverText(t, a)
	for _, m := range ms {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := m.WaitVersion(ctx, want.Version)
		cancel()
		if err != nil {
			t.Fatalf("wait %d: %v (at %d)", want.Version, err, m.Version())
		}
		if got := m.Text(); got != want.Text || m.Version() != want.Version {
			t.Fatalf("mirror %q@%d, server %q@%d", got, m.Version(), want.Text, want.Version)
		}
	}
}

// Mirrors over every transport edit the same document.
func TestMirrorsAcrossTransports(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	callers := apitest.Callers(t, a)
	names := make([]string, 0, len(callers))
	for n := range callers {
		names = append(names, n)
	}
	sort.Strings(names)
	var mirrors []*docsync.Mirror
	for _, n := range names {
		mirrors = append(mirrors, open(t, callers[n], docsync.Options{}))
	}
	ctx := context.Background()
	for i, m := range mirrors {
		waitSynced(t, a, mirrors...)
		if err := m.Insert(ctx, 6, fmt.Sprintf("// %s\n", names[i])); err != nil {
			t.Fatalf("%s: %v", names[i], err)
		}
	}
	waitSynced(t, a, mirrors...)
	if err := mirrors[0].Delete(ctx, 0, 6); err != nil {
		t.Fatal(err)
	}
	waitSynced(t, a, mirrors...)
	if err := mirrors[1].Replace(ctx, 0, 2, "XY"); err != nil {
		t.Fatal(err)
	}
	if err := mirrors[1].Replace(ctx, 0, 2, "XY"); err != nil { // no-op
		t.Fatal(err)
	}
	// A server-side edit (e.g. a formatter module) reaches every mirror.
	if _, err := a.EditDocument("a.php", 0, 0, 0, "<?php\n"); err != nil {
		t.Fatal(err)
	}
	waitSynced(t, a, mirrors...)
}

// Concurrent random edits from several mirrors converge to the server text.
func TestConcurrentEditsConverge(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	callers := []api.Caller{apitest.NewInProcess(t, a), apitest.NewPipe(t, a), apitest.NewSocket(t, a, "unix")}
	var mirrors []*docsync.Mirror
	for _, c := range callers {
		mirrors = append(mirrors, open(t, c, docsync.Options{}))
	}
	var wg sync.WaitGroup
	var conflicts sync.Map
	for i, m := range mirrors {
		wg.Add(1)
		go func(i int, m *docsync.Mirror) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(i)))
			for n := 0; n < 40; n++ {
				var err error
				var l int
				m.Read(func(b *buffer.Buffer, _ uint64) { l = b.Len() })
				off := r.Intn(l + 1)
				if r.Intn(3) == 0 && off < l {
					err = m.Delete(context.Background(), off, off+1)
				} else {
					err = m.Insert(context.Background(), off, string(rune('a'+i)))
				}
				if errors.Is(err, docsync.ErrConflict) {
					conflicts.Store(i, true)
				} else if err != nil {
					t.Error(err)
					return
				}
			}
		}(i, m)
	}
	wg.Wait()
	waitSynced(t, a, mirrors...)
}

// gate is a Caller whose events can be held back and dropped.
type gate struct {
	api.Caller
	mu     sync.Mutex
	hold   bool
	queue  []func()
	dropFn func(api.Event) bool
}

func (g *gate) Subscribe(ctx context.Context, topics []string, fn func(api.Event)) (func(), error) {
	return g.Caller.Subscribe(ctx, topics, func(e api.Event) {
		g.mu.Lock()
		if g.dropFn != nil && g.dropFn(e) {
			g.mu.Unlock()
			return
		}
		if g.hold {
			g.queue = append(g.queue, func() { fn(e) })
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()
		fn(e)
	})
}

func (g *gate) release() {
	g.mu.Lock()
	q := g.queue
	g.queue, g.hold = nil, false
	g.mu.Unlock()
	for _, f := range q {
		f()
	}
}

func TestConflictReloads(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	g := &gate{Caller: apitest.NewInProcess(t, a)}
	var mu sync.Mutex
	var changes []docsync.Change
	m := open(t, g, docsync.Options{OnChange: func(c docsync.Change) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	}})
	g.mu.Lock()
	g.hold = true
	g.mu.Unlock()
	// Someone else edits; the mirror has not seen it yet.
	if _, err := a.EditDocument("a.php", 0, 0, 0, "// other\n"); err != nil {
		t.Fatal(err)
	}
	err := m.Insert(context.Background(), 0, "mine")
	if !errors.Is(err, docsync.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if m.Text() != "// other\n"+initial {
		t.Fatalf("not reloaded: %q", m.Text())
	}
	g.release() // the stale event is ignored
	waitSynced(t, a, m)
	mu.Lock()
	if len(changes) != 1 || !changes[0].Reload || !changes[0].Remote {
		t.Fatalf("changes %+v", changes)
	}
	mu.Unlock()
	// Local edits are reported as local.
	if err := m.Insert(context.Background(), 0, "x"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if c := changes[len(changes)-1]; c.Remote || c.Reload || c.Version != m.Version() {
		t.Fatalf("local change %+v", c)
	}
}

func TestGapReloads(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	g := &gate{Caller: apitest.NewInProcess(t, a)}
	m := open(t, g, docsync.Options{})
	dropped := false
	g.mu.Lock()
	g.dropFn = func(e api.Event) bool {
		if e.Topic == api.TopicDocEdited && !dropped {
			dropped = true
			return true
		}
		return false
	}
	g.mu.Unlock()
	for _, s := range []string{"a", "b"} {
		if _, err := a.EditDocument("a.php", 0, 0, 0, s); err != nil {
			t.Fatal(err)
		}
	}
	waitSynced(t, a, m)
	if m.Text() != "ba"+initial {
		t.Fatalf("text %q", m.Text())
	}
}

func TestUndoRedoSaveClose(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	ctx := context.Background()
	closed := make(chan struct{})
	m := open(t, apitest.NewPipe(t, a), docsync.Options{OnClosed: func() { close(closed) }})
	if m.Path() != "a.php" || m.State().Modified {
		t.Fatalf("path %q state %+v", m.Path(), m.State())
	}
	if err := m.Insert(ctx, 0, "x"); err != nil {
		t.Fatal(err)
	}
	if !m.State().Modified || !m.State().CanUndo {
		t.Fatalf("state %+v", m.State())
	}
	if done, err := m.Undo(ctx); err != nil || !done || m.Text() != initial {
		t.Fatalf("undo %v %v %q", done, err, m.Text())
	}
	if done, err := m.Redo(ctx); err != nil || !done || m.Text() != "x"+initial {
		t.Fatalf("redo %v %v %q", done, err, m.Text())
	}
	res, err := m.Save(ctx)
	if err != nil || res.Modified {
		t.Fatalf("save %+v %v", res, err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Insert(ctx, 99, "x"); api.ErrorCode(err) != api.CodeInvalidParams {
		t.Fatalf("out of range: %v", err)
	}
	// Another client closes the document.
	if _, err := api.Call[api.DocCloseResult](ctx, api.NewInProcess(a), "doc.close", api.PathParams{Path: "a.php"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("OnClosed not called")
	}
	if err := m.Insert(ctx, 0, "y"); !errors.Is(err, docsync.ErrClosed) {
		t.Fatalf("insert after close: %v", err)
	}
	if _, err := m.Undo(ctx); !errors.Is(err, docsync.ErrClosed) {
		t.Fatalf("undo after close: %v", err)
	}
	if _, err := m.Save(ctx); !errors.Is(err, docsync.ErrClosed) {
		t.Fatal(err)
	}
	if err := m.Reload(ctx); !errors.Is(err, docsync.ErrClosed) {
		t.Fatal(err)
	}
	if err := m.WaitVersion(ctx, 1000); !errors.Is(err, docsync.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := m.CloseDocument(ctx); !errors.Is(err, docsync.ErrClosed) {
		t.Fatal(err)
	}
}

func TestOpenErrorsAndCloseDocument(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	c := apitest.NewInProcess(t, a)
	ctx := context.Background()
	if _, err := docsync.Open(ctx, c, "missing.php", docsync.Options{}); api.ErrorCode(err) != api.CodeNotFound {
		t.Fatalf("missing: %v", err)
	}
	n, err := docsync.Open(ctx, c, "new.php", docsync.Options{Create: true})
	if err != nil || n.Text() != "" {
		t.Fatalf("create: %v", err)
	}
	if res, err := n.CloseDocument(ctx); err != nil || res.Path != "new.php" {
		t.Fatalf("close: %+v %v", res, err)
	}
	if _, err := docsync.Open(ctx, &failSub{c}, "a.php", docsync.Options{}); err == nil {
		t.Fatal("subscribe error not reported")
	}
	m := open(t, c, docsync.Options{})
	ctx2, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.WaitVersion(ctx2, m.Version()+1); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait: %v", err)
	}
}

type failSub struct{ api.Caller }

func (failSub) Subscribe(context.Context, []string, func(api.Event)) (func(), error) {
	return nil, errors.New("no events")
}

// Review 2026-11-16 #2: CloseDocument never discards unsaved changes.
func TestCloseDocumentKeepsUnsavedChanges(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	c := apitest.NewInProcess(t, a)
	ctx := context.Background()
	m := open(t, c, docsync.Options{})
	if err := m.Insert(ctx, 0, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CloseDocument(ctx); api.ErrorCode(err) != api.CodeConflict {
		t.Fatalf("close with unsaved changes: %v", err)
	}
	if _, err := m.Save(ctx); err != nil {
		t.Fatalf("mirror detached after refused close: %v", err)
	}
	if res, err := m.CloseDocument(ctx); err != nil || res.Discarded {
		t.Fatalf("close after save: %+v %v", res, err)
	}
}
