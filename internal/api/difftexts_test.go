package api

import "testing"

func TestDiffTexts(t *testing.T) {
	a := newTestApp(t)
	var r DiffTextsResult
	call(t, a, "diff.texts", DiffTextsParams{Left: "'foo\nbar'", Right: "'foo\nbaz'"}, &r)
	if r.Identical || len(r.Blocks) != 1 || r.Blocks[0].Kind != "modified" || r.Blocks[0].LeftStart != 2 || r.Left.Label != "Expected" || r.Right.Label != "Actual" {
		t.Fatalf("%+v", r)
	}
	if len(r.Blocks[0].RightInline) != 1 || r.Blocks[0].RightInline[0].Line != 2 {
		t.Fatalf("inline %+v", r.Blocks[0])
	}
	call(t, a, "diff.texts", DiffTextsParams{Left: "x", Right: "x", LeftLabel: "A"}, &r)
	if !r.Identical || r.Left.Label != "A" || !r.Left.Exists {
		t.Fatalf("%+v", r)
	}
}
