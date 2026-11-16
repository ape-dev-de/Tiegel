package phpver

import (
	"context"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

const sample = `<?php
enum Suit { case Hearts; }
readonly class Point { public function __construct(public int $x) {} }
$r = $s |> trim(...);
$a = (real) $x;
echo utf8_encode($s);
`

func ids(ds []Diagnostic) map[string]Diagnostic {
	m := map[string]Diagnostic{}
	for _, d := range ds {
		m[d.FeatureID] = d
	}
	return m
}

func TestCheckVersions(t *testing.T) {
	doc := parsePHP(t, sample)

	ds, err := Check(doc, Version{8, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(ds)
	for _, id := range []string{"enums", "readonly_classes", "pipe_operator", "first_class_callable_syntax", "real_cast"} {
		if _, ok := got[id]; !ok {
			t.Errorf("8.0: missing %s in %v", id, got)
		}
	}
	if _, ok := got["constructor_promotion"]; ok {
		t.Error("8.0 supports constructor promotion")
	}
	if _, ok := got["utf8_encode_decode"]; ok {
		t.Error("utf8_encode is not deprecated in 8.0")
	}
	if d := got["real_cast"]; d.Severity != SeverityError || !strings.Contains(d.Message, "removed in PHP 8.0") {
		t.Errorf("real_cast: %+v", d)
	}
	if d := got["enums"]; d.Range.Start != (syntax.Point{Line: 1, Column: 0}) || d.Range.EndByte-d.Range.StartByte != 4 {
		t.Errorf("enums range: %+v", d.Range)
	}
	if d := got["enums"]; !strings.Contains(d.Message, "requires PHP 8.1 (project targets 8.0)") {
		t.Errorf("enums message: %q", d.Message)
	}
	for i := 1; i < len(ds); i++ {
		if ds[i].Range.StartByte < ds[i-1].Range.StartByte {
			t.Fatal("diagnostics not sorted")
		}
	}

	ds, err = Check(doc, Version{8, 2, 5})
	if err != nil {
		t.Fatal(err)
	}
	got = ids(ds)
	if _, ok := got["enums"]; ok {
		t.Error("8.2 supports enums")
	}
	if d, ok := got["utf8_encode_decode"]; !ok || d.Severity != SeverityWarning || !strings.Contains(d.Message, "deprecated since PHP 8.2") {
		t.Errorf("utf8_encode on 8.2: %+v", d)
	}
	if _, ok := got["pipe_operator"]; !ok {
		t.Error("8.2 lacks pipe operator")
	}

	ds, err = Check(doc, Version{8, 5, 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if !strings.HasPrefix(d.FeatureID, "real_cast") && !strings.HasPrefix(d.FeatureID, "utf8") {
			t.Errorf("8.5: unexpected %+v", d)
		}
	}
}

func TestCheckWarningsAndNotes(t *testing.T) {
	doc := parsePHP(t, "<?php\nclass A extends B { #[\\Override] public function f() {} }\n")
	ds, err := Check(doc, Version{8, 2, 0})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(ds)
	if d := got["override_attribute"]; d.Severity != SeverityWarning || !strings.Contains(d.Message, "ignored") {
		t.Errorf("override: %+v", d)
	}
}

func TestCheckNonPHPAndClosed(t *testing.T) {
	d, err := syntax.NewDocument(context.Background(), syntax.LangJSON, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if ds, err := Check(d, Version{7, 0, 0}); err != nil || ds != nil {
		t.Fatalf("json: %v %v", ds, err)
	}
	p := parsePHP(t, "<?php enum A {}")
	p.Close()
	if _, err := Check(p, Version{7, 0, 0}); err == nil {
		t.Fatal("closed document should fail")
	}
}

func TestNewCheckerErrors(t *testing.T) {
	bad := []string{
		"features:\n  - {id: a, min: '8.0', query: '(no_such_node) @feature'}\n",
		"features:\n  - {id: a, min: '8.0', query: '(enum_declaration) @x'}\n",
	}
	for _, src := range bad {
		tab, err := ParseTable([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewChecker(tab); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
	// Entries without query are skipped; an empty checker reports nothing.
	tab, err := ParseTable([]byte("features:\n  - {id: a, min: '9.0'}\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewChecker(tab)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Table() != tab {
		t.Fatal("table")
	}
	if ds, err := c.Check(parsePHP(t, "<?php enum A {}"), Version{7, 0, 0}); err != nil || ds != nil {
		t.Fatalf("empty checker: %v %v", ds, err)
	}
}

func TestCheckConcurrent(t *testing.T) {
	doc := parsePHP(t, sample)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := Check(doc, Version{7, 4, 0})
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
