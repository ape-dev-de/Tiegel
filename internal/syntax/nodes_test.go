//go:build cgo

package syntax

import (
	"context"
	"strings"
	"testing"
)

func TestNodesAt(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"<?php\nfunction f() {\n    fore|\n}\n", "name ERROR compound_statement function_definition program"},
		{"<?php\nclass A {\n    pubf|\n}\n", "name ERROR declaration_list class_declaration program"},
		{"<?php\n$x = foo(fore|);\n", "name argument arguments function_call_expression assignment_expression expression_statement program"},
		{"<html>fore|<?php echo 1; ?>", "text program"},
		{"<?php\n// fore|\n", "comment program"},
	} {
		off := strings.Index(tc.src, "|")
		src := strings.Replace(tc.src, "|", "", 1)
		d, err := NewDocument(context.Background(), LangPHP, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for _, n := range d.NodesAt(off) {
			kinds = append(kinds, n.Kind)
		}
		d.Close()
		if got := strings.Join(kinds, " "); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.src, got, tc.want)
		}
	}
	d, err := NewDocument(context.Background(), LangEnv, []byte("A=1"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.NodesAt(1) != nil {
		t.Error("env has no tree")
	}
}
