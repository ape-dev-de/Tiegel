package editorview

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestSelectionOccurrences(t *testing.T) {
	src := "<?php\n$request = $x;\nfoo($request, $requestId);\n$request->get();\n"
	s := buffer.New([]byte(src)).Snapshot()
	at := strings.Index(src, "$request")
	sel := cursor.Range(at, at+len("$request"))
	got := selectionOccurrences(s, sel, 0, s.Len())
	if len(got) != 2 {
		t.Fatalf("occurrences %v", got)
	}
	for _, o := range got {
		if src[o[0]:o[1]] != "$request" || o[0] == at {
			t.Fatalf("bad %v", o)
		}
	}
	// Reversed selection, partial words and blanks.
	if n := len(selectionOccurrences(s, cursor.Range(at+len("$request"), at), 0, s.Len())); n != 2 {
		t.Fatalf("reversed %d", n)
	}
	q := strings.Index(src, "quest")
	if n := len(selectionOccurrences(s, cursor.Range(q, q+5), 0, s.Len())); n != 3 {
		t.Fatalf("inner text matches anywhere: %d", n)
	}
	sp := strings.Index(src, " = ")
	if selectionOccurrences(s, cursor.Range(sp, sp+1), 0, s.Len()) != nil || selectionOccurrences(s, cursor.Caret(at), 0, s.Len()) != nil {
		t.Fatal("blank/empty selection")
	}
	nl := strings.Index(src, "\n")
	if selectionOccurrences(s, cursor.Range(0, nl+2), 0, s.Len()) != nil {
		t.Fatal("multi-line selection")
	}
	// Only the window is searched.
	end := strings.Index(src, "$request->")
	if n := len(selectionOccurrences(s, sel, end, s.Len())); n != 1 {
		t.Fatalf("window %d", n)
	}
}

// TestRenderOccurrences draws the marks of a selected $request (with
// TIEGEL_SHOTS=<dir> as PNGs).
func TestRenderOccurrences(t *testing.T) {
	src := "<?php\n\nfunction handle(Request $request): Response\n{\n    $id = $request->get('id');\n    $requestId = $id;\n    return $this->render($request, $requestId);\n}\n"
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		e := New(buffer.NewString(src))
		at := strings.Index(src, "$request")
		e.cur.SetSelections([]cursor.Selection{cursor.Range(at, at+len("$request"))}, 0)
		img, err := uitest.Render(th, image.Pt(640, 220), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return e.Layout(gtx, th)
		})
		e.Close()
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if dir := os.Getenv("TIEGEL_SHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, "editor-occurrences-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}
