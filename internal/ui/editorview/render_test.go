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
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var samplePHP = `<?php
declare(strict_types=1);

namespace App\Service;

/**
 * Loads users. Ünïcödé: 😀 e` + "́" + ` 漢字
 */
final class UserService
{
	private const LIMIT = 10;

	public function find(int $id): ?User
	{
		$user = $this->repo->load($id, "name", 'x');
		if ($user === null) {
			return null; // not found
		}
		return $user;
	}
}
` + "\t$wide = '" + strings.Repeat("long line ", 40) + "';\n"

// decorated returns an editor showing every overlay.
func decorated(t testing.TB) *Editor {
	e := New(buffer.NewString(samplePHP))
	t.Cleanup(e.Close)
	e.Highlighter = highlight.PHPDemo
	e.SetFoldRanges(highlight.BraceFolds(e.Buffer().Snapshot()))
	e.SetGutterMarkers(LineMarkers{
		13: {{Kind: MarkerBreakpoint}, {Kind: MarkerGitModified}},
		15: {{Kind: MarkerExecutionPoint}, {Kind: MarkerGitAdded}},
		16: {{Kind: MarkerBreakpointDisabled}, {Kind: MarkerGitAdded}},
		17: {{Kind: MarkerBreakpointConditional}, {Kind: MarkerGitDeleted}},
		9:  {{Kind: MarkerIcon, Glyph: atoms.GlyphWarning, Tone: ToneWarning}},
	})
	src := e.Buffer().String()
	at := strings.Index(src, "load")
	e.SetDiagnostics([]Diagnostic{
		{Start: at, End: at + 4, Severity: SeverityError, Message: "undefined method"},
		{Start: strings.Index(src, "$id)"), End: strings.Index(src, "$id)") + 3, Severity: SeverityWarning},
		{Start: strings.Index(src, "LIMIT"), End: strings.Index(src, "LIMIT") + 5, Severity: SeverityHint},
		{Start: strings.Index(src, "repo"), End: strings.Index(src, "repo") + 4, Severity: SeverityInfo},
	})
	q, _ := search.Compile("user", search.Options{})
	e.SetSearch(q)
	ret := strings.Index(src, "return $user")
	e.Cursor().SetSelections([]cursor.Selection{
		cursor.Range(strings.Index(src, "Loads"), strings.Index(src, "users.")+5),
		cursor.Caret(strings.Index(src, "($user") + 1),
		cursor.Caret(ret),
	}, 1)
	e.Folds().Collapse(strings.Count(src[:strings.Index(src, "if (")], "\n"))
	return e
}

func TestRenderSmoke(t *testing.T) {
	e := decorated(t)
	uitest.Smoke(t, "editor", image.Pt(900, 500), e.Layout)
	uitest.Smoke(t, "tiny", image.Pt(30, 10), e.Layout)
	empty := New(buffer.NewString(""))
	defer empty.Close()
	uitest.Smoke(t, "empty", image.Pt(300, 200), empty.Layout)
	// Unbounded constraints fall back to a default size.
	d, err := uitest.Layout(tokens.Dark(nil), image.Pt(1<<24, 1<<24), e.Layout)
	if err != nil || d.Size.X <= 0 || d.Size.X >= 1<<20 {
		t.Fatalf("unbounded: %v %v", d, err)
	}
}

// TestRenderHeadless draws the decorated editor in both themes on the GPU
// and checks that syntax colors reach the screen. With TIEGEL_SHOTS=<dir>
// the PNGs are written for visual review.
func TestRenderHeadless(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	size := image.Pt(900, 520)
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		e := decorated(t)
		img, err := uitest.Render(th, size, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			e.Focus(gtx) // carets are drawn only while focused
			return e.Layout(gtx, th)
		})
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if !e.Focused() {
			t.Error("not focused")
		}
		if n := countNear(img, th.Editor.Cursor); n < 20 {
			t.Errorf("%s: carets not drawn (%d px)", th.Mode, n)
		}
		if n := uitest.CountDiffering(img, th.Editor.Background); n < 5000 {
			t.Errorf("%s: only %d pixels drawn", th.Mode, n)
		}
		for _, role := range []tokens.SyntaxRole{tokens.RoleKeyword, tokens.RoleVariable, tokens.RoleString} {
			if n := countNear(img, th.SyntaxColor(role)); n < 10 {
				t.Errorf("%s: %s color barely visible (%d px)", th.Mode, role, n)
			}
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "editor-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// countNear counts pixels within a small distance of c (antialiasing
// makes exact matches rare).
func countNear(img *image.RGBA, c interface{ RGBA() (r, g, b, a uint32) }) int {
	cr, cg, cb, _ := c.RGBA()
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := img.RGBAAt(x, y)
			if near(uint32(p.R)<<8, cr) && near(uint32(p.G)<<8, cg) && near(uint32(p.B)<<8, cb) {
				n++
			}
		}
	}
	return n
}

func near(a, b uint32) bool {
	d := int(a>>8) - int(b>>8)
	return d > -12 && d < 12
}
