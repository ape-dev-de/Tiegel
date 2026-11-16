package syntax

// Tests that need no Tree-sitter (run with and without cgo).

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func newDoc(t testing.TB, lang Language, src string) *Document {
	t.Helper()
	d, err := NewDocument(context.Background(), lang, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

// roleAt returns the role of the first occurrence of needle in src.
func roleAt(t *testing.T, d *Document, needle string) Role {
	t.Helper()
	src := d.Source()
	i := bytes.Index(src, []byte(needle))
	if i < 0 {
		t.Fatalf("needle %q not found", needle)
	}
	spans := d.Highlights(i, i+len(needle))
	if len(spans) == 0 {
		t.Fatalf("no spans for %q", needle)
	}
	r := spans[0].Role
	for _, s := range spans {
		if s.Role != r {
			t.Fatalf("needle %q has mixed roles: %v", needle, spans)
		}
	}
	return r
}

func checkTiling(t *testing.T, spans []Span, start, end int) {
	t.Helper()
	pos := start
	for _, s := range spans {
		if s.Start != pos || s.End <= s.Start {
			t.Fatalf("spans do not tile at %d: %+v", pos, s)
		}
		pos = s.End
	}
	if pos != end {
		t.Fatalf("spans end at %d, want %d", pos, end)
	}
}

func TestEnvHighlights(t *testing.T) {
	src := "# comment\nexport APP_ENV=prod\nDEBUG=true\nPORT=8080\nURL=\"http://${HOST}:$PORT\" # trailing\nRAW='${NOPE}'\nPLAIN=abc $HOME #c\nEMPTY=\n  \nbroken line\n"
	d := newDoc(t, LangEnv, src)
	if d.Language() != LangEnv || LangEnv.HasTree() || !LangPHP.HasTree() {
		t.Fatal("language")
	}
	for needle, want := range map[string]Role{
		"# comment": RoleComment, "export": RoleKeyword, "APP_ENV": RoleProperty, "prod": RoleString,
		"true": RoleConstant, "8080": RoleNumber, "${HOST}": RoleVariable, "$PORT": RoleVariable,
		"# trailing": RoleComment, "'${NOPE}'": RoleString, "$HOME": RoleVariable, "#c": RoleComment,
		"broken": RoleProperty,
	} {
		if got := roleAt(t, d, needle); got != want {
			t.Errorf("env %q: %v want %v", needle, got, want)
		}
	}
	i := strings.Index(src, "=prod")
	if got := d.Highlights(i, i+1); len(got) != 1 || got[0].Role != RoleOperator {
		t.Errorf("env '=': %v", got)
	}
	checkTiling(t, d.Highlights(0, len(src)), 0, len(src))
	// Ranges starting mid-line work as well.
	checkTiling(t, d.Highlights(3, 25), 3, 25)
	// Reparse just replaces the text.
	if err := d.Reparse(context.Background(), []byte("A=1")); err != nil {
		t.Fatal(err)
	}
	if got := roleAt(t, d, "1"); got != RoleNumber {
		t.Fatal(got)
	}
}

func TestRolesAndCaptures(t *testing.T) {
	if len(Roles()) != int(roleCount) || Roles()[0] != RolePlain {
		t.Fatal("Roles")
	}
	for _, r := range Roles() {
		if r.String() == "" {
			t.Fatal("empty role name")
		}
	}
	if Role(200).String() != "plain" {
		t.Fatal("out of range role")
	}
	cases := map[string]Role{
		"function.method":       RoleFunction,
		"punctuation.bracket":   RolePunctuation,
		"string.special.key":    RoleProperty,
		"string.special":        RoleString,
		"variable.builtin":      RoleKeyword,
		"type.builtin":          RoleType,
		"markup":                RolePlain,
		"nonsense.thing":        RolePlain,
		"constant.builtin":      RoleConstant,
		"keyword.function.call": RoleKeyword,
	}
	for in, want := range cases {
		if got := RoleForCapture(in); got != want {
			t.Errorf("RoleForCapture(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		path, head string
		want       Language
	}{
		{"src/A.php", "", LangPHP},
		{"x.phtml", "", LangPHP},
		{"composer.json", "", LangJSON},
		{"composer.lock", "", LangJSON},
		{"yarn.lock", "", LangPlain},
		{"compose.yaml", "", LangYAML},
		{"a.yml", "", LangYAML},
		{"phpunit.xml", "", LangXML},
		{"phpunit.xml.dist", "", LangXML},
		{".env", "", LangEnv},
		{".env.local", "", LangEnv},
		{"prod.env", "", LangEnv},
		{"bin/console", "#!/usr/bin/env php\n<?php", LangPHP},
		{"bin/run", "#!/bin/sh\necho", LangPlain},
		{"script", "  <?php echo 1;", LangPHP},
		{"feed", "<?xml version=\"1.0\"?>", LangXML},
		{"README", "hello", LangPlain},
	}
	for _, c := range cases {
		if got := DetectLanguage(c.path, []byte(c.head)); got != c.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestPointsAndEdits(t *testing.T) {
	src := []byte("ab\ncd\nef")
	if p := PointAt(src, 4); p != (Point{Line: 1, Column: 1}) {
		t.Fatal(p)
	}
	if p := PointAt(src, 100); p != (Point{Line: 2, Column: 2}) {
		t.Fatal(p)
	}
	if p := PointAt(src, -1); p != (Point{}) {
		t.Fatal(p)
	}
	e := NewInputEdit(src, 1, 4, []byte("X\nYZ"))
	want := InputEdit{StartByte: 1, OldEndByte: 4, NewEndByte: 5,
		StartPoint: Point{0, 1}, OldEndPoint: Point{1, 1}, NewEndPoint: Point{1, 2}}
	if e != want {
		t.Fatalf("%+v", e)
	}
	e = NewInputEdit(src, 0, 0, []byte("Q"))
	if e.NewEndPoint != (Point{0, 1}) {
		t.Fatalf("%+v", e)
	}
}
