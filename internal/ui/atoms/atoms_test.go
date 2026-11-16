package atoms_test

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var area = image.Pt(400, 300)

func TestLabelRoles(t *testing.T) {
	roles := []atoms.Label{
		atoms.Text("Body"), atoms.Muted("Muted"), atoms.Small("Small"), atoms.Strong("Strong"),
		atoms.Heading("Heading"), atoms.Title("Title"), atoms.Code("$code = 1;"),
	}
	var heights []int
	for _, l := range roles {
		d := uitest.Smoke(t, "label "+l.Text, area, l.Layout)
		heights = append(heights, d.Size.Y)
		if d.Baseline <= 0 || d.Baseline >= d.Size.Y {
			t.Errorf("label %q baseline %d outside (0,%d)", l.Text, d.Baseline, d.Size.Y)
		}
	}
	if !(heights[2] < heights[0] && heights[0] < heights[4] && heights[4] < heights[5]) {
		t.Errorf("type scale not monotonic: %v", heights)
	}
}

func TestLabelTruncates(t *testing.T) {
	long := atoms.Text("a very long label that certainly does not fit into fifty pixels").SingleLine()
	d := uitest.Smoke(t, "truncated", image.Pt(50, 100), long.Layout)
	one := uitest.Smoke(t, "one", area, atoms.Text("x").Layout)
	if d.Size.Y != one.Size.Y {
		t.Errorf("single-line label height %d, want %d", d.Size.Y, one.Size.Y)
	}
}

func TestIcons(t *testing.T) {
	for _, g := range atoms.Glyphs {
		if !g.Valid() {
			t.Errorf("glyph %s is invalid", g.Name())
		}
		d := uitest.Smoke(t, "icon "+g.Name(), area, atoms.NewIcon(g).Layout)
		if d.Size != image.Pt(16, 16) {
			t.Errorf("icon %s size %v, want 16x16", g.Name(), d.Size)
		}
	}
	big := uitest.Smoke(t, "icon big", area, atoms.Icon{Glyph: atoms.GlyphRun, Size: 32}.Layout)
	if big.Size != image.Pt(32, 32) {
		t.Errorf("big icon %v", big.Size)
	}
	// Nil and broken glyphs reserve space but do not panic.
	uitest.Smoke(t, "icon nil", area, atoms.Icon{}.Layout)
	bad := atoms.NewGlyph("bad", []byte("nope"))
	if bad.Valid() {
		t.Error("invalid data reported valid")
	}
	uitest.Smoke(t, "icon bad", area, atoms.NewIcon(bad).Layout)
	var nilGlyph *atoms.Glyph
	if nilGlyph.Name() != "" || nilGlyph.Valid() {
		t.Error("nil glyph")
	}
}

func TestButtonVariants(t *testing.T) {
	for _, kind := range []atoms.ButtonKind{atoms.Primary, atoms.Secondary, atoms.Ghost} {
		for _, disabled := range []bool{false, true} {
			b := atoms.NewButton(kind, "Commit")
			b.Disabled = disabled
			d := uitest.Smoke(t, "button "+kind.String(), area, b.Layout)
			if d.Size.Y != 28 {
				t.Errorf("button %s height %d, want ControlH 28", kind, d.Size.Y)
			}
			if d.Size.X <= d.Size.Y {
				t.Errorf("button %s too narrow: %v", kind, d.Size)
			}
		}
	}
	withIcon := &atoms.Button{Kind: atoms.Primary, Text: "Run", Glyph: atoms.GlyphRun}
	plain := atoms.NewButton(atoms.Primary, "Run")
	if a, b := uitest.Smoke(t, "icon button", area, withIcon.Layout), uitest.Smoke(t, "plain", area, plain.Layout); a.Size.X <= b.Size.X {
		t.Errorf("button with glyph (%d) not wider than without (%d)", a.Size.X, b.Size.X)
	}
	iconOnly := &atoms.Button{Kind: atoms.Ghost, Glyph: atoms.GlyphSettings}
	if d := uitest.Smoke(t, "icon only", area, iconOnly.Layout); d.Size.X != d.Size.Y {
		t.Errorf("icon-only button not square: %v", d.Size)
	}
}

func TestButtonClick(t *testing.T) {
	h := uitest.NewHarness(area)
	b := atoms.NewButton(atoms.Primary, "OK")
	h.Frame(b.Layout)
	h.Click(image.Pt(5, 5))
	clicked := false
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked = b.Clicked(gtx)
		return b.Layout(gtx, th)
	})
	if !clicked {
		t.Fatal("click not registered")
	}
	b.Disabled = true
	h.Click(image.Pt(5, 5))
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked = b.Clicked(gtx)
		return b.Layout(gtx, th)
	})
	if clicked {
		t.Fatal("disabled button reported a click")
	}
}

func TestIconButton(t *testing.T) {
	b := atoms.NewIconButton(atoms.GlyphClose, "Close")
	d := uitest.Smoke(t, "iconbutton", area, b.Layout)
	if d.Size != image.Pt(28, 28) {
		t.Errorf("size %v", d.Size)
	}
	h := uitest.NewHarness(area)
	h.Frame(b.Layout)
	h.Click(image.Pt(10, 10))
	var clicked bool
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked = b.Clicked(gtx)
		return b.Layout(gtx, th)
	})
	if !clicked {
		t.Fatal("icon button click not registered")
	}
}

func TestCheckbox(t *testing.T) {
	for _, s := range []atoms.CheckState{atoms.Unchecked, atoms.Checked, atoms.Indeterminate} {
		for _, disabled := range []bool{false, true} {
			cb := atoms.NewCheckbox("src/Controller.php", s)
			cb.Disabled = disabled
			uitest.Smoke(t, "checkbox "+s.String(), area, cb.Layout)
		}
	}
	noLabel := uitest.Smoke(t, "no label", area, (&atoms.Checkbox{}).Layout)
	withLabel := uitest.Smoke(t, "label", area, atoms.NewCheckbox("x", atoms.Unchecked).Layout)
	if noLabel.Size.X >= withLabel.Size.X {
		t.Error("label does not add width")
	}
}

func TestCheckStateNext(t *testing.T) {
	cases := map[atoms.CheckState]atoms.CheckState{
		atoms.Unchecked:     atoms.Checked,
		atoms.Checked:       atoms.Unchecked,
		atoms.Indeterminate: atoms.Checked,
	}
	for in, want := range cases {
		if got := in.Next(); got != want {
			t.Errorf("%s.Next() = %s, want %s", in, got, want)
		}
	}
}

func TestCheckboxClickCycle(t *testing.T) {
	h := uitest.NewHarness(area)
	cb := atoms.NewCheckbox("file", atoms.Indeterminate)
	h.Frame(cb.Layout)
	for _, want := range []atoms.CheckState{atoms.Checked, atoms.Unchecked, atoms.Checked} {
		h.Click(image.Pt(5, 8))
		h.Frame(cb.Layout)
		if cb.State != want {
			t.Fatalf("state = %s, want %s", cb.State, want)
		}
	}
	if !cb.Checked() {
		t.Fatal("Checked() mismatch")
	}
	cb.Disabled = true
	h.Click(image.Pt(5, 8))
	h.Frame(cb.Layout)
	if cb.State != atoms.Checked {
		t.Fatal("disabled checkbox toggled")
	}
}

func TestTextField(t *testing.T) {
	tf := atoms.NewTextField("Search…")
	d := uitest.Smoke(t, "textfield", area, tf.Layout)
	if d.Size.X != area.X || d.Size.Y != 28 {
		t.Errorf("text field size %v, want %dx28", d.Size, area.X)
	}
	tf.SetText("hello")
	if tf.Text() != "hello" {
		t.Fatal("SetText/Text")
	}
	tf.Leading = atoms.GlyphSearch
	tf.Invalid = true
	tf.Mono = true
	uitest.Smoke(t, "textfield variants", area, tf.Layout)
	tf.Disabled = true
	uitest.Smoke(t, "textfield disabled", area, tf.Layout)

	multi := &atoms.TextField{Hint: "Commit message"}
	multi.SetText("line 1\nline 2\nline 3")
	md := uitest.Smoke(t, "multiline", area, multi.Layout)
	if md.Size.Y <= 28 {
		t.Errorf("multi-line field height %d not larger than single line", md.Size.Y)
	}
	// Unbounded width falls back to a sane default.
	d = uitest.Smoke(t, "unbounded", image.Pt(1<<24, 100), tf.Layout)
	if d.Size.X != 240 {
		t.Errorf("unbounded width %d, want 240", d.Size.X)
	}
}

func TestBadge(t *testing.T) {
	for _, k := range atoms.BadgeKinds {
		for _, solid := range []bool{false, true} {
			d := uitest.Smoke(t, "badge "+k.String(), area, atoms.Badge{Text: "8.3", Kind: k, Solid: solid}.Layout)
			if d.Size.Y >= 28 {
				t.Errorf("badge %s too tall: %v", k, d.Size)
			}
		}
	}
	if d := uitest.Smoke(t, "badge 1", area, atoms.Badge{Text: "1"}.Layout); d.Size.X < d.Size.Y {
		t.Errorf("single-char badge narrower than tall: %v", d.Size)
	}
}

func TestTooltip(t *testing.T) {
	bubble := uitest.Smoke(t, "bubble", area, atoms.TooltipBubble{Text: "Run the current file"}.Layout)
	long := uitest.Smoke(t, "bubble long", image.Pt(1000, 1000), atoms.TooltipBubble{
		Text: "A very long tooltip text that must wrap because tooltips never grow wider than a few hundred dp at most",
	}.Layout)
	if long.Size.X > 320 || long.Size.Y <= bubble.Size.Y {
		t.Errorf("long bubble not wrapped: %v", long.Size)
	}

	tip := &atoms.Tooltip{Text: "Settings", AlwaysVisible: true}
	btn := atoms.NewButton(atoms.Ghost, "Settings")
	d := uitest.Smoke(t, "tooltip", area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return btn.Layout(gtx, th) })
	})
	if !tip.Visible() {
		t.Error("AlwaysVisible tooltip not visible")
	}
	if d.Size.Y != 28 {
		t.Errorf("tooltip changed content size: %v", d.Size)
	}

	// Hover: invisible before the delay.
	h := uitest.NewHarness(area)
	hov := &atoms.Tooltip{Text: "Hover"}
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return hov.Layout(gtx, th, atoms.Text("target").Fn(th))
	}
	h.Frame(w)
	h.Click(image.Pt(3, 3)) // includes a Move → Enter
	h.Frame(w)
	if hov.Visible() {
		t.Error("tooltip visible before delay")
	}
}

func TestDivider(t *testing.T) {
	d := uitest.Smoke(t, "divider", area, atoms.Divider{}.Layout)
	if d.Size != image.Pt(area.X, 1) {
		t.Errorf("horizontal divider %v", d.Size)
	}
	v := uitest.Smoke(t, "vdivider", area, atoms.Divider{Vertical: true, Inset: 4}.Layout)
	if v.Size != image.Pt(1, area.Y) {
		t.Errorf("vertical divider %v", v.Size)
	}
}

func TestBadgeContrast(t *testing.T) {
	// Badge text must stay readable (≥ 3:1, large-ish UI text) on its tint.
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		for _, k := range atoms.BadgeKinds {
			for _, solid := range []bool{false, true} {
				bg, fg := atoms.BadgeColors(atoms.Badge{Kind: k, Solid: solid}, th)
				if r := tokens.ContrastRatio(fg, bg); r < 3 {
					t.Errorf("%s badge %s solid=%v contrast %.2f", th.Mode, k, solid, r)
				}
			}
		}
	}
}

func TestRenderHeadless(t *testing.T) {
	th := tokens.Dark(nil)
	b := atoms.NewButton(atoms.Primary, "Commit")
	img, err := uitest.Render(th, image.Pt(160, 40), b.Layout)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	// Somewhere inside the button the accent fill must be visible.
	px := img.RGBAAt(4, 14)
	if px.R != th.Color.Accent.R || px.G != th.Color.Accent.G || px.B != th.Color.Accent.B {
		t.Errorf("button fill pixel = %v, want accent %v", px, th.Color.Accent)
	}
	if n := uitest.CountDiffering(img, th.Color.Background); n < 500 {
		t.Errorf("only %d pixels drawn", n)
	}
}

func TestTooltipPassesClicks(t *testing.T) {
	h := uitest.NewHarness(area)
	tip := &atoms.Tooltip{Text: "Run"}
	b := atoms.NewIconButton(atoms.GlyphRun, "Run")
	clicked := false
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked = b.Clicked(gtx) || clicked
		return tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
	}
	h.Frame(w)
	h.Click(image.Pt(10, 10))
	h.Frame(w)
	if !clicked {
		t.Fatal("tooltip swallowed the click")
	}
}

func TestIconButtonActiveAndDisabled(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, disabled := range []bool{false, true} {
			b := &atoms.IconButton{Glyph: atoms.GlyphListen, Active: active, Disabled: disabled, Description: "Listen"}
			if d := uitest.Smoke(t, "iconbutton toggle", area, b.Layout); d.Size != image.Pt(28, 28) {
				t.Errorf("size %v", d.Size)
			}
		}
	}
	th := tokens.Dark(nil)
	b := &atoms.IconButton{Glyph: atoms.GlyphListen, Active: true}
	img, err := uitest.Render(th, image.Pt(40, 40), b.Layout)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	if px := img.RGBAAt(2, 14); px.R == th.Color.Background.R && px.G == th.Color.Background.G && px.B == th.Color.Background.B {
		t.Error("active toggle has no tinted background")
	}
}
