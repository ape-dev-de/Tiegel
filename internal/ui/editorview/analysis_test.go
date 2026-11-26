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
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestSubtleDiagnostics draws findings read from a PHPStan report (D556):
// a thin dotted underline in Editor.Analysis and a dot in the gutter,
// next to a real error squiggle. With TIEGEL_SHOTS the PNGs
// (analysis-dark/light.png) are written for review.
func TestSubtleDiagnostics(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	src := samplePHP
	at := strings.Index(src, "load(")
	ret := strings.Index(src, "?User")
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		e := New(buffer.NewString(src))
		e.Highlighter = highlight.PHPDemo
		line := strings.Count(src[:at], "\n")
		e.SetGutterMarkers(LineMarkers{
			line:                               {{Kind: MarkerDot, Tone: ToneAnalysis}},
			strings.Count(src[:ret], "\n"):     {{Kind: MarkerDot, Tone: ToneAnalysis}, {Kind: MarkerBreakpoint}},
			strings.Count(src[:ret], "\n") + 9: {{Kind: MarkerDot, Tone: ToneDanger}},
		})
		e.SetDiagnostics([]Diagnostic{
			{Start: at, End: at + 4, Severity: SeverityWarning, Subtle: true, Message: "Call to an undefined method", Detail: "method.notFound"},
			{Start: ret, End: ret + 5, Severity: SeverityWarning, Subtle: true},
			{Start: strings.Index(src, "LIMIT"), End: strings.Index(src, "LIMIT") + 5, Severity: SeverityError},
		})
		if ds := e.DiagnosticsAt(at + 1); len(ds) != 1 || !ds[0].Subtle || ds[0].Detail != "method.notFound" {
			t.Fatalf("%+v", ds)
		}
		img, err := uitest.Render(th, image.Pt(720, 420), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return e.Layout(gtx, th)
		})
		e.Close()
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		// The dots are drawn in the analysis tone, much fewer pixels than a
		// squiggle; the error keeps its own color.
		if n := countNear(img, th.Editor.Analysis); n < 15 {
			t.Errorf("%s: analysis marks barely visible (%d px)", th.Mode, n)
		}
		if n := countNear(img, th.Color.Danger); n < 10 {
			t.Errorf("%s: error squiggle missing (%d px)", th.Mode, n)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "analysis-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}
