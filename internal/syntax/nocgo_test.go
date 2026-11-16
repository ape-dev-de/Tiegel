//go:build !cgo

package syntax

import (
	"context"
	"errors"
	"testing"
)

func TestNoTreeSitter(t *testing.T) {
	ctx := context.Background()
	if TreeSitter {
		t.Fatal("TreeSitter must be false without cgo")
	}
	for _, l := range []Language{LangPHP, LangJSON, LangYAML, LangXML} {
		if _, err := NewDocument(ctx, l, []byte("x")); !errors.Is(err, ErrNoTreeSitter) {
			t.Errorf("%s: err = %v", l, err)
		}
	}
	if _, err := NewQuery(LangPHP, "(x)"); !errors.Is(err, ErrNoTreeSitter) {
		t.Errorf("NewQuery err = %v", err)
	}
	var q Query
	q.Close()
	if q.Language() != "" || q.CaptureNames() != nil || q.PatternCount() != 0 || q.PatternStart(0) != 0 {
		t.Error("zero query")
	}

	d, err := NewDocument(ctx, LangEnv, []byte("A=1\n# c\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Language() != LangEnv || string(d.Source()) != "A=1\n# c\n" || d.HasError() || d.SExpr() != "" {
		t.Error("env document state")
	}
	d.Edit(InputEdit{})
	if sp := d.Highlights(0, 100); len(sp) < 3 || sp[0].Role != RoleProperty {
		t.Errorf("env spans = %v", sp)
	}
	if d.Highlights(5, 2) != nil || d.Folds() != nil || d.Outline() != nil {
		t.Error("empty results expected")
	}
	if _, err := d.Matches(nil); !errors.Is(err, ErrNoTreeSitter) {
		t.Error(err)
	}
	s, err := d.Snapshot()
	if err != nil || s.Language() != LangEnv || len(s.Source()) != 8 || s.Folds() != nil || s.Outline() != nil {
		t.Fatal("snapshot", err)
	}
	if _, err := s.Matches(nil); !errors.Is(err, ErrNoTreeSitter) {
		t.Error(err)
	}
	s.Close()

	p, _ := NewDocument(ctx, LangPlain, nil)
	if err := p.Reparse(ctx, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if sp := p.Highlights(0, 5); len(sp) != 1 || sp[0].Role != RolePlain {
		t.Errorf("plain = %v", sp)
	}
	p.Close()
	if p.Highlights(0, 5) != nil || p.Reparse(ctx, nil) != ErrClosed {
		t.Error("closed")
	}
	if _, err := p.Snapshot(); err != ErrClosed {
		t.Error(err)
	}
	if !LangPHP.HasTree() || LangEnv.HasTree() {
		t.Error("HasTree")
	}
	if DetectLanguage("a.php", nil) != LangPHP || PointAt([]byte("a\nb"), 3) != (Point{1, 1}) {
		t.Error("types")
	}
}
