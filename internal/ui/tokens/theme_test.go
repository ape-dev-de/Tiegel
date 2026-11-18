package tokens

import (
	"image/color"
	"math"
	"testing"

	"gioui.org/font/gofont"
)

func themes() []*Theme {
	sh := NewShaper()
	return []*Theme{Dark(sh), Light(sh)}
}

func TestContrastRatioKnownValues(t *testing.T) {
	black, white := Hex(0x000000), Hex(0xFFFFFF)
	if got := ContrastRatio(black, white); math.Abs(got-21) > 0.01 {
		t.Fatalf("black/white = %.2f, want 21", got)
	}
	if got := ContrastRatio(white, white); math.Abs(got-1) > 1e-9 {
		t.Fatalf("white/white = %.2f, want 1", got)
	}
	// #767676 on white is the classic 4.54:1 reference.
	if got := ContrastRatio(Hex(0x767676), white); math.Abs(got-4.54) > 0.01 {
		t.Fatalf("#767676/white = %.3f, want 4.54", got)
	}
}

type pair struct {
	name   string
	fg, bg color.NRGBA
	min    float64
}

func TestThemeContrast(t *testing.T) {
	for _, th := range themes() {
		c, e := th.Color, th.Editor
		pairs := []pair{
			{"Text/Background", c.Text, c.Background, 4.5},
			{"Text/Surface", c.Text, c.Surface, 4.5},
			{"Text/SurfaceAlt", c.Text, c.SurfaceAlt, 4.5},
			{"Text/Selection", c.Text, c.Selection, 4.5},
			{"Text/SurfaceHover", c.Text, c.SurfaceHover, 4.5},
			{"TextMuted/Background", c.TextMuted, c.Background, 4.5},
			{"TextMuted/Surface", c.TextMuted, c.Surface, 4.5},
			{"OnAccent/Accent", c.OnAccent, c.Accent, 4.5},
			{"OnAccent/AccentHover", c.OnAccent, c.AccentHover, 4.5},
			{"TooltipText/TooltipBg", c.TooltipText, c.TooltipBg, 4.5},
			{"EditorText/EditorBg", e.Text, e.Background, 4.5},
			{"EditorText/CurrentLine", e.Text, e.CurrentLine, 4.5},
			{"EditorText/DiffAdded", e.Text, e.DiffAddedBg, 4.5},
			{"EditorText/DiffRemoved", e.Text, e.DiffRemovedBg, 4.5},
			// Non-text UI elements and secondary roles: WCAG 1.4.11 (3:1).
			{"Accent/Background", c.Accent, c.Background, 3},
			{"Danger/Background", c.Danger, c.Background, 3},
			{"Warning/Background", c.Warning, c.Background, 3},
			{"Success/Background", c.Success, c.Background, 3},
			{"LineNumber/Gutter", e.LineNumber, e.Gutter, 2.5},
		}
		for _, r := range SyntaxRoles {
			min := 4.5
			if r == RoleComment {
				min = 3 // comments are deliberately subdued
			}
			pairs = append(pairs, pair{"syntax " + r.String(), th.SyntaxColor(r), e.Background, min})
		}
		g := th.Git
		for _, p := range []pair{
			{"FileAdded", g.FileAdded, c.Surface, 4.5},
			{"FileModified", g.FileModified, c.Surface, 4.5},
			{"FileUntracked", g.FileUntracked, c.Surface, 4.5},
			{"FileConflict", g.FileConflict, c.Surface, 4.5},
			{"FileDeleted", g.FileDeleted, c.Surface, 3},
		} {
			pairs = append(pairs, p)
		}
		for _, p := range pairs {
			if got := ContrastRatio(p.fg, p.bg); got < p.min {
				t.Errorf("%s theme: %s contrast %.2f < %.1f", th.Mode, p.name, got, p.min)
			}
		}
	}
}

func TestThemesDiffer(t *testing.T) {
	th := themes()
	if th[0].Color.Background == th[1].Color.Background {
		t.Fatal("light and dark backgrounds are identical")
	}
	if th[0].Mode != ModeDark || th[1].Mode != ModeLight {
		t.Fatal("unexpected modes")
	}
	if th[0].Toggled().Mode != ModeLight || th[1].Toggled().Mode != ModeDark {
		t.Fatal("Toggled does not switch mode")
	}
	if th[0].Toggled().Shaper != th[0].Shaper {
		t.Fatal("Toggled must keep the shaper")
	}
	if New(ModeDark, nil).Shaper == nil {
		t.Fatal("New must create a shaper")
	}
}

func TestSpacingGrid(t *testing.T) {
	s := themes()[0].Space
	for i, v := range []float32{float32(s.XS), float32(s.S), float32(s.M), float32(s.L), float32(s.XL), float32(s.XXL)} {
		if int(v)%4 != 0 {
			t.Errorf("spacing step %d = %v is not on the 4dp grid", i, v)
		}
	}
	if s.Step(3) != 12 {
		t.Errorf("Step(3) = %v", s.Step(3))
	}
}

func TestSyntaxColorCoversAllRoles(t *testing.T) {
	th := themes()[0]
	seen := map[color.NRGBA]SyntaxRole{}
	for _, r := range SyntaxRoles {
		col := th.SyntaxColor(r)
		if col == th.Editor.Text {
			t.Errorf("role %s falls back to plain text", r)
		}
		if prev, dup := seen[col]; dup {
			t.Errorf("roles %s and %s share a color", prev, r)
		}
		seen[col] = r
		if r.String() == "unknown" {
			t.Errorf("role %d has no name", r)
		}
	}
	if th.SyntaxColor(RolePlain) != th.Editor.Text {
		t.Error("plain role must use editor text color")
	}
	if SyntaxRole(99).String() != "unknown" {
		t.Error("out of range role name")
	}
}

func TestFontChainsIncludeBundledFonts(t *testing.T) {
	want := map[string]bool{}
	for _, f := range gofont.Collection() {
		want[string(f.Font.Typeface)] = true
	}
	th := themes()[0]
	for _, tf := range []string{string(th.Font.UI.Typeface), string(th.Font.Mono.Typeface)} {
		found := false
		for _, f := range splitChain(tf) {
			found = found || want[f]
		}
		if !found {
			t.Errorf("typeface chain %q has no bundled Go font %v", tf, want)
		}
	}
}

func TestColorHelpers(t *testing.T) {
	a, b := Hex(0x000000), Hex(0xFFFFFF)
	if m := Mix(a, b, 0.5); m.R < 127 || m.R > 128 {
		t.Errorf("Mix midpoint = %v", m)
	}
	if Mix(a, b, -1) != a || Mix(a, b, 2) != b {
		t.Error("Mix must clamp t")
	}
	if WithAlpha(a, 10).A != 10 {
		t.Error("WithAlpha")
	}
	if o := Over(WithAlpha(b, 0), a); o != a {
		t.Errorf("Over transparent = %v", o)
	}
	if ModeDark.String() != "dark" || ModeLight.String() != "light" {
		t.Error("Mode.String")
	}
}

func TestTerminalColors(t *testing.T) {
	for _, th := range []*Theme{Dark(nil), Light(nil)} {
		if c := th.TerminalColor(1); c != th.Terminal.ANSI[1] {
			t.Errorf("%v: ANSI 1 = %v", th.Mode, c)
		}
		if c := th.TerminalColor(16); c != (color.NRGBA{A: 0xff}) {
			t.Errorf("cube start %v", c)
		}
		if c := th.TerminalColor(231); c != (color.NRGBA{R: 255, G: 255, B: 255, A: 0xff}) {
			t.Errorf("cube end %v", c)
		}
		if c := th.TerminalColor(232); c != (color.NRGBA{R: 8, G: 8, B: 8, A: 0xff}) {
			t.Errorf("gray start %v", c)
		}
		if c := th.TerminalColor(255); c.R != 238 {
			t.Errorf("gray end %v", c)
		}
		if r := ContrastRatio(th.Terminal.Foreground, th.Terminal.Background); r < 7 {
			t.Errorf("%v: terminal text contrast %.1f", th.Mode, r)
		}
	}
}
