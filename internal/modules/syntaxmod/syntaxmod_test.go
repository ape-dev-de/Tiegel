package syntaxmod

import (
	"context"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func newApp(t *testing.T) (*api.App, *Module) {
	t.Helper()
	m := New()
	return modtest.NewApp(t, []module.Module{m}, nil), m
}

func drain(t *testing.T, a *api.App) {
	t.Helper()
	if err := a.Bus().Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

// Works in every build: dotenv and plain text need no Tree-sitter.
func TestEnvAndPlain(t *testing.T) {
	a, _ := newApp(t)
	modtest.Project(t, a, map[string]string{".env": "A=1\n# note\nB=${A}\n", "notes.txt": "hello\nworld\n"})

	var h HighlightsResult
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: ".env"}}, &h)
	if h.Language != "env" || h.Open || h.Path != ".env" || h.Length != 18 || len(h.Spans) == 0 || h.Spans[0].Role != "property" {
		t.Fatalf("env %+v", h)
	}
	for _, s := range h.Spans {
		if s.Role == "plain" {
			t.Fatalf("plain span without plain: %+v", s)
		}
	}
	// Line range: only line 2 (the comment).
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: ".env"}, StartLine: 2, EndLine: 2}, &h)
	if h.Start != 4 || h.End != 11 || len(h.Spans) != 1 || h.Spans[0].Role != "comment" {
		t.Fatalf("line range %+v", h)
	}
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: ".env"}, StartLine: 3, EndLine: 99}, &h)
	if h.Start != 11 || h.End != 18 {
		t.Fatalf("open line range %+v", h)
	}

	// Open document, plain text, with plain spans.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "notes.txt"}, nil)
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "notes.txt"}, Plain: true, Start: ptr(1), End: ptr(3)}, &h)
	if !h.Open || h.Language != "" || len(h.Spans) != 1 || h.Spans[0] != (Span{1, 3, "plain"}) {
		t.Fatalf("plain %+v", h)
	}
	var f FoldsResult
	modtest.Call(t, a, "syntax.folds", Source{Path: "notes.txt"}, &f)
	if f.Folds == nil || len(f.Folds) != 0 {
		t.Fatalf("folds %+v", f)
	}
	var o OutlineResult
	modtest.Call(t, a, "syntax.outline", Source{Text: ptr("X=1"), Language: "dotenv"}, &o)
	if o.Language != "env" || o.Symbols == nil || len(o.Symbols) != 0 {
		t.Fatalf("outline %+v", o)
	}

	// Errors.
	modtest.WantCode(t, a, "syntax.highlights", HighlightsParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: ".env"}, Start: ptr(5), End: ptr(2)}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: ".env"}, End: ptr(500)}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: ".env"}, StartLine: 3, EndLine: 1}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "syntax.folds", Source{Path: ".env", Language: "cobol"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "syntax.folds", Source{Path: "missing.txt"}, api.CodeNotFound)

	// Disabled module.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "syntax.folds", Source{Path: ".env"}, api.CodeModuleDisabled)
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]syntax.Language{"": syntax.LangPlain, "text": syntax.LangPlain, "env": syntax.LangEnv, "php": syntax.LangPHP} {
		if got, _, err := parseLanguage(in); err != nil || got != want {
			t.Errorf("parseLanguage(%q) = %q, %v", in, got, err)
		}
	}
	for in, want := range map[string]syntax.Language{"php": syntax.LangPHP, "json": syntax.LangJSON, "yaml": syntax.LangYAML, "xml": syntax.LangXML, "dotenv": syntax.LangEnv, "plaintext": syntax.LangPlain} {
		if got := fromLangdetect(in); got != want {
			t.Errorf("fromLangdetect(%q) = %q", in, got)
		}
	}
	src := []byte("aä\nb")
	li := newLineIndex(src)
	if li.lineStart(-1) != 0 || li.lineStart(1) != 4 || li.lineStart(9) != 5 {
		t.Error("lineStart")
	}
	if p := li.position(src, syntax.Point{Line: 0, Column: 3}); p != (Position{Offset: 3, Line: 1, Col: 3}) {
		t.Errorf("position %+v", p)
	}
	if err := parseError(context.Canceled); err != context.Canceled {
		t.Error("cancel not passed through")
	}
	if api.ErrorCode(parseError(syntax.ErrNoTreeSitter)) != api.CodeUnavailable || api.ErrorCode(parseError(syntax.ErrClosed)) != api.CodeFailed {
		t.Error("parseError codes")
	}
	if len(New().Claims()) != 3 || New().DependsOn() != nil || New().ID() != "syntax" {
		t.Error("module info")
	}
}
