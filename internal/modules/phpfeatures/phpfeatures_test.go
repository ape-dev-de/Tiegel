package phpfeatures

import (
	"context"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

const enumSrc = "<?php\nenum Suit { case Hearts; }\n"

func newApp(t *testing.T) (*api.App, *Module) {
	t.Helper()
	m := New(Options{Debounce: 5 * time.Millisecond})
	a := modtest.NewApp(t, []module.Module{phpruntime.New(phpruntime.Options{}), m}, nil)
	return a, m
}

func diagsOf(e bus.Event) Diagnostics { return e.Payload.(Diagnostics) }

func TestCheck(t *testing.T) {
	a, _ := newApp(t)
	modtest.WantCode(t, a, "php.check", CheckParams{Path: "/nonexistent/x.php"}, api.CodeNotFound)
	modtest.Project(t, a, map[string]string{
		"composer.json": `{"require":{"php":"^8.0"}}`,
		"enum.php":      enumSrc,
	})
	var res CheckResult
	modtest.Call(t, a, "php.check", CheckParams{Path: "enum.php"}, &res)
	if res.Version != "8.0.0" || len(res.Diagnostics) == 0 || res.Source == "" {
		t.Fatalf("check %+v", res)
	}
	d := res.Diagnostics[0]
	if d.Line != 2 || d.Col < 1 || d.Severity == "" || d.Feature == "" || d.Highlight == "" {
		t.Fatalf("diagnostic %+v", d)
	}
	modtest.Call(t, a, "php.check", CheckParams{Path: "enum.php", Version: "8.1"}, &res)
	if len(res.Diagnostics) != 0 || res.Source != "param" {
		t.Fatalf("8.1 %+v", res)
	}
	// The open (unsaved) document is checked, not the disk content.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "enum.php"}, nil)
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "enum.php", Text: "<?php\necho 1;\n"}, nil)
	modtest.Call(t, a, "php.check", CheckParams{Path: "enum.php"}, &res)
	if len(res.Diagnostics) != 0 {
		t.Fatalf("open doc %+v", res)
	}
	modtest.WantCode(t, a, "php.check", CheckParams{Path: "missing.php"}, api.CodeNotFound)
	modtest.WantCode(t, a, "php.check", CheckParams{Path: "enum.php", Version: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "php.check", CheckParams{}, api.CodeInvalidParams)
}

func TestNoVersion(t *testing.T) {
	a, _ := newApp(t)
	modtest.Project(t, a, map[string]string{"a.php": enumSrc})
	modtest.WantCode(t, a, "php.check", CheckParams{Path: "a.php"}, api.CodeNotFound)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.WantCode(t, a, "php.diagnostics", api.PathParams{Path: "a.php"}, api.CodeNotFound)
	modtest.WantCode(t, a, "php.diagnostics", api.PathParams{Path: "other.php"}, api.CodeNotFound)
}

func TestBackgroundDiagnostics(t *testing.T) {
	a, m := newApp(t)
	rec := modtest.Record(t, a, "*")
	modtest.Project(t, a, map[string]string{
		"composer.json": `{"require":{"php":"^8.0"}}`,
		"enum.php":      "<?php\necho 'ä😀';\n",
		"data.json":     "{}",
	})
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "data.json"}, nil)
	var st api.DocOpenResult
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "enum.php"}, &st)
	ev := diagsOf(rec.Wait(t, api.TopicPHPDiagnostics, nil))
	if ev.Path != "enum.php" || len(ev.Diagnostics) != 0 || ev.PHPVersion != "8.0.0" {
		t.Fatalf("clean %+v", ev)
	}
	// Insert an enum after the multibyte line: incremental edit.
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "enum.php", At: api.Pos{Line: 3}, Text: "enum Suit { case Hearts; }\n"}, nil)
	ev = diagsOf(rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return len(diagsOf(e).Diagnostics) > 0 }))
	if ev.Diagnostics[0].Line != 3 || ev.Version != 1 {
		t.Fatalf("enum %+v", ev)
	}
	// The GUI edits the shared buffer directly; the module follows.
	d, err := a.Document("enum.php")
	if err != nil {
		t.Fatal(err)
	}
	d.Lock()
	if _, err := d.Buffer().Insert(0, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	d.Unlock()
	ev = diagsOf(rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return diagsOf(e).Version == 2 }))
	if len(ev.Diagnostics) == 0 || ev.Diagnostics[0].Line != 4 {
		t.Fatalf("gui edit %+v", ev)
	}
	modtest.Eventually(t, "source in sync", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		tr := m.docs[d.Path()]
		return tr != nil && string(tr.src) == string(d.Snapshot().Text)
	})
	var got Diagnostics
	modtest.Call(t, a, "php.diagnostics", api.PathParams{Path: "enum.php"}, &got)
	if got.Version != 2 || len(got.Diagnostics) == 0 {
		t.Fatalf("method %+v", got)
	}
	// Undo both changes (multi-step) → clean again.
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "enum.php"}, nil)
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "enum.php"}, nil)
	ev = diagsOf(rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return diagsOf(e).Version == 4 }))
	if len(ev.Diagnostics) != 0 {
		t.Fatalf("after undo %+v", ev)
	}

	// Version override: persisted, published, re-checks.
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "enum.php", At: api.Pos{Line: 3}, Text: "enum A {}\n"}, nil)
	rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return diagsOf(e).Version == 5 && len(diagsOf(e).Diagnostics) > 0 })
	var or OverrideResult
	modtest.Call(t, a, "php.setVersionOverride", OverrideParams{Version: "8.1.3"}, &or)
	if or.Override != "8.1" {
		t.Fatalf("override %+v", or)
	}
	rec.Wait(t, api.TopicPHPVersionChanged, nil)
	ev = diagsOf(rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return diagsOf(e).PHPVersion == "8.1.0" }))
	if len(ev.Diagnostics) != 0 {
		t.Fatalf("8.1 %+v", ev)
	}
	var pv phpruntime.PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Short != "8.1" || pv.Source != "override" {
		t.Fatalf("php.version %+v", pv)
	}
	modtest.WantCode(t, a, "php.setVersionOverride", OverrideParams{Version: "x"}, api.CodeInvalidParams)
	modtest.Call(t, a, "php.setVersionOverride", OverrideParams{}, &or)
	if or.Override != "" {
		t.Fatalf("cleared %+v", or)
	}
	rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return diagsOf(e).PHPVersion == "8.0.0" && diagsOf(e).Version == 5 })

	// Closing drops the document.
	modtest.Call(t, a, "doc.close", api.PathParams{Path: "enum.php"}, nil)
	modtest.Eventually(t, "dropped", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.docs) == 0
	})
}

func TestResyncAndRestart(t *testing.T) {
	a, m := newApp(t)
	rec := modtest.Record(t, a, api.TopicPHPDiagnostics)
	modtest.Project(t, a, map[string]string{"composer.json": `{"require":{"php":"^8.0"}}`, "a.php": "<?php\n"})
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "php.check", CheckParams{Path: "a.php"}, api.CodeModuleDisabled)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "a.php", At: api.Pos{Line: 2}, Text: "enum E {}\n"}, nil)
	// Enabling picks up the already open (and edited) document.
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: ID}, nil)
	ev := diagsOf(rec.Wait(t, api.TopicPHPDiagnostics, nil))
	if ev.Version != 1 || len(ev.Diagnostics) == 0 {
		t.Fatalf("after enable %+v", ev)
	}
	d, _ := a.Document("a.php")
	// A missed change (version gap) triggers a resync from the buffer.
	d.Lock()
	_, _ = d.Buffer().Insert(0, []byte("\n"))
	d.Unlock()
	m.edited(api.DocEditEvent{File: d.Path(), Version: 99})
	rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return diagsOf(e).Version == 2 })
	// Stale and unknown edits are ignored.
	m.edited(api.DocEditEvent{File: d.Path(), Version: 1})
	m.edited(api.DocEditEvent{File: "/elsewhere.php", Version: 1})
	// interpreter.changed invalidates the cached version.
	a.Bus().Publish(api.TopicInterpreterChanged, api.InterpreterChangedEvent{ID: "x"})
	modtest.Eventually(t, "version invalidated", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.version == nil || len(rec.Events("")) > 2
	})
	if err := m.Start(fakeCtx{}); err == nil {
		t.Fatal("start without App context")
	}
	if m.ID() != ID || m.DependsOn()[0] != "php-runtime" || len(m.Claims()) != 3 {
		t.Fatal("identity")
	}
	if q, _ := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{}); q.Short != "8.0" {
		t.Fatalf("hook %+v", q)
	}
}

type fakeCtx struct{ module.Context }
