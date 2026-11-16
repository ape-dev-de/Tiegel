package main

import (
	"fmt"
	"image"
	"image/color"
	"slices"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// buildPages creates all catalog pages with their persistent widget state.
func buildPages() []*page {
	pages := append([]*page{
		colorsPage(), syntaxPage(), typographyPage(),
		labelPage(), iconPage(), buttonPage(), textFieldPage(), checkboxPage(),
		badgePage(), tooltipPage(), dividerPage(), surfacePage(),
		searchFieldPage(), fileRowPage(), tabPage(), breakpointPage(), diffPage(),
		treeRowPage(), toolbarPage(), tablePage(),
		editorPage(),
		debugToolbarPage(), callStackPage(), variablesPage(), watchesPage(), breakpointsListPage(), sessionListPage(),
		interpreterPickerPage(), pathMappingPage(), hintsPage(), statusbarVersionPage(),
		debugPanelPage(),
	}, append(gitPages(), diffPages()...)...)
	// Group by level so the sidebar shows each level once.
	order := map[string]int{"Tokens": 0, "Atoms": 1, "Molecules": 2, "Organisms": 3, "Templates": 4}
	slices.SortStableFunc(pages, func(a, b *page) int { return order[a.Level] - order[b.Level] })
	return pages
}

// ---- Tokens ----------------------------------------------------------------

type namedColor struct {
	name string
	c    color.NRGBA
}

func swatch(th *tokens.Theme, nc namedColor) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Dp(28)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				r := image.Rectangle{Max: image.Pt(sz, sz)}
				rr := gtx.Dp(th.Radius.M)
				paint.FillShape(gtx.Ops, th.Color.BorderStrong, clip.UniformRRect(r, rr).Op(gtx.Ops))
				paint.FillShape(gtx.Ops, nc.c, clip.UniformRRect(r.Inset(1), rr-1).Op(gtx.Ops))
				return layout.Dimensions{Size: r.Max}
			}),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Rigid(fixedWidth(150, column(0,
				atoms.Text(nc.name).SingleLine().Fn(th),
				atoms.Label{Text: hex(nc.c), Role: atoms.RoleCode, Color: th.Color.TextMuted}.Fn(th),
			))),
		)
	}
}

func hex(c color.NRGBA) string {
	if c.A != 0xFF {
		return fmt.Sprintf("#%02X%02X%02X%02X", c.R, c.G, c.B, c.A)
	}
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

func swatches(th *tokens.Theme, cs ...namedColor) layout.Widget {
	ws := make([]layout.Widget, len(cs))
	for i, nc := range cs {
		ws[i] = fixedWidth(200, swatch(th, nc))
	}
	return flow(th.Space.L, ws...)
}

func colorsPage() *page {
	return &page{
		Level: "Tokens", Name: "Colors", Glyph: atoms.GlyphSettings,
		Summary: "Semantic colors. Components only use these names – never raw values.",
		Variants: func(th *tokens.Theme) []variant {
			c, e, g := th.Color, th.Editor, th.Git
			return []variant{
				{"UI palette", swatches(th,
					namedColor{"Background", c.Background}, namedColor{"Surface", c.Surface},
					namedColor{"SurfaceAlt", c.SurfaceAlt}, namedColor{"SurfaceHover", c.SurfaceHover},
					namedColor{"Border", c.Border}, namedColor{"BorderStrong", c.BorderStrong},
					namedColor{"Text", c.Text}, namedColor{"TextMuted", c.TextMuted},
					namedColor{"TextDisabled", c.TextDisabled}, namedColor{"Selection", c.Selection},
					namedColor{"TooltipBg", c.TooltipBg}, namedColor{"Focus", c.Focus},
				)},
				{"Accent & status", swatches(th,
					namedColor{"Accent", c.Accent}, namedColor{"AccentHover", c.AccentHover},
					namedColor{"OnAccent", c.OnAccent}, namedColor{"Danger", c.Danger},
					namedColor{"Warning", c.Warning}, namedColor{"Success", c.Success},
					namedColor{"Info", c.Info},
				)},
				{"Editor", swatches(th,
					namedColor{"Background", e.Background}, namedColor{"CurrentLine", e.CurrentLine},
					namedColor{"Gutter", e.Gutter}, namedColor{"LineNumber", e.LineNumber},
					namedColor{"LineNumberActive", e.LineNumberActive}, namedColor{"Cursor", e.Cursor},
					namedColor{"Selection", e.Selection}, namedColor{"Breakpoint", e.Breakpoint},
					namedColor{"DiffAddedBg", e.DiffAddedBg}, namedColor{"DiffRemovedBg", e.DiffRemovedBg},
				)},
				{"Git", swatches(th,
					namedColor{"Gutter added", g.Added}, namedColor{"Gutter modified", g.Modified},
					namedColor{"Gutter deleted", g.Deleted}, namedColor{"File added", g.FileAdded},
					namedColor{"File modified", g.FileModified}, namedColor{"File deleted", g.FileDeleted},
					namedColor{"File untracked", g.FileUntracked}, namedColor{"File ignored", g.FileIgnored},
					namedColor{"File conflict", g.FileConflict},
				)},
			}
		},
	}
}

type span struct {
	role tokens.SyntaxRole
	text string
}

// phpSample is a hand-tokenized snippet showing every syntax role.
var phpSample = [][]span{
	{{tokens.RolePunctuation, "<?php"}},
	{},
	{{tokens.RoleComment, "/** Sends queued mails. */"}},
	{{tokens.RoleKeyword, "final class "}, {tokens.RoleType, "Mailer"}, {tokens.RoleKeyword, " implements "}, {tokens.RoleType, "Sender"}},
	{{tokens.RolePunctuation, "{"}},
	{{tokens.RolePlain, "    "}, {tokens.RoleKeyword, "private const "}, {tokens.RoleConstant, "MAX_RETRIES"}, {tokens.RoleOperator, " = "}, {tokens.RoleNumber, "3"}, {tokens.RolePunctuation, ";"}},
	{},
	{{tokens.RolePlain, "    "}, {tokens.RoleAttribute, "#[Override]"}},
	{{tokens.RolePlain, "    "}, {tokens.RoleKeyword, "public function "}, {tokens.RoleFunction, "send"}, {tokens.RolePunctuation, "("}, {tokens.RoleType, "Message "}, {tokens.RoleVariable, "$msg"}, {tokens.RolePunctuation, "): "}, {tokens.RoleType, "bool"}},
	{{tokens.RolePlain, "    "}, {tokens.RolePunctuation, "{"}},
	{{tokens.RolePlain, "        "}, {tokens.RoleVariable, "$this"}, {tokens.RoleOperator, "->"}, {tokens.RoleProperty, "log"}, {tokens.RoleOperator, "->"}, {tokens.RoleFunction, "info"}, {tokens.RolePunctuation, "("}, {tokens.RoleString, "\"Sending to {$msg->to}\""}, {tokens.RolePunctuation, ");"}},
	{{tokens.RolePlain, "        "}, {tokens.RoleKeyword, "return "}, {tokens.RoleVariable, "$msg"}, {tokens.RoleOperator, "->"}, {tokens.RoleProperty, "retries"}, {tokens.RoleOperator, " < "}, {tokens.RoleKeyword, "self"}, {tokens.RoleOperator, "::"}, {tokens.RoleConstant, "MAX_RETRIES"}, {tokens.RolePunctuation, ";"}},
	{{tokens.RolePlain, "    "}, {tokens.RolePunctuation, "}"}},
	{{tokens.RolePunctuation, "}"}},
	{{tokens.RolePunctuation, "?>"}, {tokens.RoleTag, "<p "}, {tokens.RoleAttribute, "class"}, {tokens.RoleOperator, "="}, {tokens.RoleString, "\"note\""}, {tokens.RoleTag, ">"}, {tokens.RolePlain, "Done"}, {tokens.RoleTag, "</p>"}},
}

// codeSample renders phpSample on the editor background with line numbers.
func codeSample(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pad := gtx.Dp(th.Space.M)
		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		rows := make([]layout.Widget, len(phpSample))
		for i, line := range phpSample {
			fc := []layout.FlexChild{layout.Rigid(fixedWidth(32, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				col := th.Editor.LineNumber
				if i == 8 {
					col = th.Editor.LineNumberActive
				}
				return atoms.Label{Text: fmt.Sprint(i + 1), Role: atoms.RoleCode, Color: col, Alignment: text.End}.Layout(gtx, th)
			})), layout.Rigid(layout.Spacer{Width: th.Space.L}.Layout)}
			for _, s := range line {
				fc = append(fc, layout.Rigid(atoms.Label{Text: s.text, Role: atoms.RoleCode, Color: th.SyntaxColor(s.role)}.Fn(th)))
			}
			if len(line) == 0 {
				fc = append(fc, layout.Rigid(atoms.Code(" ").Fn(th)))
			}
			cur := i == 8
			rows[i] = func(gtx layout.Context) layout.Dimensions {
				m := recordOps(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx, fc...)
				})
				if cur {
					paint.FillShape(gtx.Ops, th.Editor.CurrentLine, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, m.dims.Size.Y)}.Op())
				}
				m.addAt(gtx, image.Point{})
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, m.dims.Size.Y)}
			}
		}
		m := recordOps(cgtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X -= 2 * pad
			return column(0, rows...)(gtx)
		})
		size := image.Pt(gtx.Constraints.Max.X, m.dims.Size.Y+2*pad)
		paint.FillShape(gtx.Ops, th.Editor.Background, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(th.Radius.M)).Op(gtx.Ops))
		m.addAt(gtx, image.Pt(pad, pad))
		return layout.Dimensions{Size: size}
	}
}

func syntaxPage() *page {
	return &page{
		Level: "Tokens", Name: "Syntax", Glyph: atoms.GlyphFileCode,
		Summary: "Highlighting roles. Tree-sitter captures and LSP semantic tokens map onto these.",
		Variants: func(th *tokens.Theme) []variant {
			roles := make([]namedColor, 0, len(tokens.SyntaxRoles))
			for _, r := range tokens.SyntaxRoles {
				roles = append(roles, namedColor{r.String(), th.SyntaxColor(r)})
			}
			return []variant{
				{"PHP sample", codeSample(th)},
				{"Roles", swatches(th, roles...)},
			}
		},
	}
}

func typographyPage() *page {
	return &page{
		Level: "Tokens", Name: "Typography & Spacing", Glyph: atoms.GlyphFileText,
		Summary: "Go fonts (bundled), type scale, 4dp spacing grid and corner radii.",
		Variants: func(th *tokens.Theme) []variant {
			bar := func(name string, dp unit.Dp) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(fixedWidth(80, atoms.Code(name).Fn(th))),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							sz := image.Pt(gtx.Dp(dp), gtx.Dp(12))
							paint.FillShape(gtx.Ops, th.Color.Accent, clip.Rect{Max: sz}.Op())
							return layout.Dimensions{Size: sz}
						}),
						layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
						layout.Rigid(atoms.Small(fmt.Sprintf("%vdp", float32(dp))).Fn(th)),
					)
				}
			}
			radius := func(name string, dp unit.Dp) layout.Widget {
				return column(th.Space.XS,
					func(gtx layout.Context) layout.Dimensions {
						sz := image.Pt(gtx.Dp(56), gtx.Dp(36))
						rr := min(gtx.Dp(dp), sz.Y/2)
						paint.FillShape(gtx.Ops, th.Color.BorderStrong, clip.UniformRRect(image.Rectangle{Max: sz}, rr).Op(gtx.Ops))
						paint.FillShape(gtx.Ops, th.Color.SurfaceAlt, clip.UniformRRect(image.Rectangle{Max: sz}.Inset(1), max(0, rr-1)).Op(gtx.Ops))
						return layout.Dimensions{Size: sz}
					},
					atoms.Small(fmt.Sprintf("%s %vdp", name, float32(dp))).Fn(th),
				)
			}
			s, r := th.Space, th.Radius
			return []variant{
				{"Type scale (UI font: " + string(th.Font.UI.Typeface) + ", code: " + string(th.Font.Mono.Typeface) + ")", column(th.Space.S,
					atoms.Title(fmt.Sprintf("Title %vsp", float32(th.Text.Title))).Fn(th),
					atoms.Heading(fmt.Sprintf("Heading %vsp", float32(th.Text.Heading))).Fn(th),
					atoms.Label{Text: fmt.Sprintf("Large %vsp", float32(th.Text.Large))}.Fn(th),
					atoms.Text(fmt.Sprintf("Body %vsp – The quick brown fox jumps over the lazy dog", float32(th.Text.Body))).Fn(th),
					atoms.Small(fmt.Sprintf("Small %vsp – captions and badges", float32(th.Text.Small))).Fn(th),
					atoms.Code(fmt.Sprintf("Code %vsp – $foo = fn(int $x): int => $x ** 2;", float32(th.Text.Code))).Fn(th),
				)},
				{"Spacing (4dp grid)", column(th.Space.XS,
					bar("XXS", s.XXS), bar("XS", s.XS), bar("S", s.S), bar("M", s.M),
					bar("L", s.L), bar("XL", s.XL), bar("XXL", s.XXL),
				)},
				{"Radii", flow(th.Space.L, radius("S", r.S), radius("M", r.M), radius("L", r.L), radius("Pill", r.Pill))},
			}
		},
	}
}

// ---- Atoms -----------------------------------------------------------------

func labelPage() *page {
	return &page{
		Level: "Atoms", Name: "Label", Glyph: atoms.GlyphFileText,
		Summary: "Non-interactive text in one of the text roles. atoms.Text / Muted / Small / Strong / Heading / Title / Code.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Roles", column(th.Space.S,
					atoms.Title("Title").Fn(th), atoms.Heading("Heading").Fn(th),
					atoms.Strong("Strong").Fn(th), atoms.Text("Body").Fn(th),
					atoms.Muted("Muted").Fn(th), atoms.Small("Small").Fn(th),
					atoms.Code("Code: $user->name").Fn(th),
				)},
				{"Truncation (MaxLines = 1, 220dp)", fixedWidth(220, atoms.Text("src/Infrastructure/Persistence/Doctrine/UserRepository.php").SingleLine().Fn(th))},
				{"Color override", flow(th.Space.L,
					atoms.Text("Danger").WithColor(th.Color.Danger).Fn(th),
					atoms.Text("Warning").WithColor(th.Color.Warning).Fn(th),
					atoms.Text("Success").WithColor(th.Color.Success).Fn(th),
				)},
			}
		},
	}
}

func iconPage() *page {
	return &page{
		Level: "Atoms", Name: "Icon", Glyph: atoms.GlyphSettings,
		Summary: "Vector glyphs (Material Design icons, Apache 2.0) drawn in token colors.",
		Variants: func(th *tokens.Theme) []variant {
			all := make([]layout.Widget, len(atoms.Glyphs))
			for i, g := range atoms.Glyphs {
				all[i] = fixedWidth(96, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(atoms.Icon{Glyph: g, Size: 24}.Fn(th)),
						layout.Rigid(layout.Spacer{Height: th.Space.XS}.Layout),
						layout.Rigid(atoms.Small(g.Name()).Fn(th)),
					)
				})
			}
			return []variant{
				{"All glyphs (24dp)", flow(th.Space.S, all...)},
				{"Sizes 12 / 16 / 24 / 32", flow(th.Space.L,
					atoms.Icon{Glyph: atoms.GlyphFolder, Size: 12}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphFolder, Size: 16}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphFolder, Size: 24}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphFolder, Size: 32}.Fn(th),
				)},
				{"Colors", flow(th.Space.L,
					atoms.Icon{Glyph: atoms.GlyphError, Color: th.Color.Danger}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphWarning, Color: th.Color.Warning}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphDone, Color: th.Color.Success}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphInfo, Color: th.Color.Info}.Fn(th),
					atoms.Icon{Glyph: atoms.GlyphRun, Color: th.Color.Accent}.Fn(th),
				)},
			}
		},
	}
}

func buttonPage() *page {
	kinds := []atoms.ButtonKind{atoms.Primary, atoms.Secondary, atoms.Ghost}
	var normal, disabled, withIcon []*atoms.Button
	for _, k := range kinds {
		normal = append(normal, atoms.NewButton(k, "Commit"))
		d := atoms.NewButton(k, "Commit")
		d.Disabled = true
		disabled = append(disabled, d)
	}
	withIcon = []*atoms.Button{
		{Kind: atoms.Primary, Text: "Run", Glyph: atoms.GlyphRun},
		{Kind: atoms.Secondary, Text: "Debug", Glyph: atoms.GlyphDebug},
		{Kind: atoms.Ghost, Text: "Refresh", Glyph: atoms.GlyphRefresh},
		{Kind: atoms.Ghost, Glyph: atoms.GlyphSettings},
	}
	iconButtons := []*atoms.IconButton{
		atoms.NewIconButton(atoms.GlyphAdd, "Add"),
		atoms.NewIconButton(atoms.GlyphRemove, "Remove"),
		atoms.NewIconButton(atoms.GlyphRefresh, "Refresh"),
		atoms.NewIconButton(atoms.GlyphClose, "Close"),
		{Glyph: atoms.GlyphRun, Description: "Run"},
		{Glyph: atoms.GlyphDebug, Disabled: true, Description: "Debug"},
	}
	clicks := 0
	row := func(th *tokens.Theme, bs []*atoms.Button) layout.Widget {
		ws := make([]layout.Widget, len(bs))
		for i, b := range bs {
			ws[i] = func(gtx layout.Context) layout.Dimensions {
				if b.Clicked(gtx) {
					clicks++
				}
				return b.Layout(gtx, th)
			}
		}
		return flow(th.Space.S, ws...)
	}
	return &page{
		Level: "Atoms", Name: "Button", Glyph: atoms.GlyphRun,
		Summary: "Primary (one per view), Secondary, Ghost. Optional leading glyph; IconButton for toolbars.",
		Variants: func(th *tokens.Theme) []variant {
			iconButtons[4].Color = th.Color.Success
			ibs := make([]layout.Widget, len(iconButtons))
			for i, b := range iconButtons {
				ibs[i] = func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }
			}
			return []variant{
				{"Primary · Secondary · Ghost", row(th, normal)},
				{"Disabled", row(th, disabled)},
				{"With glyph / glyph only", row(th, withIcon)},
				{"IconButton (last one disabled)", flow(th.Space.XS, ibs...)},
				{"Interaction", atoms.Muted(fmt.Sprintf("Buttons clicked: %d", clicks)).Fn(th)},
			}
		},
	}
}

func textFieldPage() *page {
	empty := atoms.NewTextField("Commit message summary")
	filled := atoms.NewTextField("")
	filled.SetText("Fix N+1 query in UserRepository")
	icon := atoms.NewTextField("Go to file…")
	icon.Leading = atoms.GlyphSearch
	invalid := atoms.NewTextField("Port")
	invalid.SetText("99999")
	invalid.Invalid = true
	mono := atoms.NewTextField("Condition")
	mono.Mono = true
	mono.SetText("$retries > 3 && $user !== null")
	disabled := atoms.NewTextField("Disabled")
	disabled.SetText("/usr/local/bin/php")
	disabled.Disabled = true
	multi := &atoms.TextField{Hint: "Commit message"}
	multi.SetText("Add breakpoint conditions\n\nConditions are evaluated by Xdebug\nbefore the debugger stops.")
	f := func(th *tokens.Theme, t *atoms.TextField) layout.Widget {
		return fixedWidth(360, func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th) })
	}
	return &page{
		Level: "Atoms", Name: "TextField", Glyph: atoms.GlyphFileText,
		Summary: "Text input on widget.Editor: hint, leading glyph, invalid, mono, disabled, multi-line.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Empty with hint", f(th, empty)},
				{"Filled", f(th, filled)},
				{"Leading glyph", f(th, icon)},
				{"Invalid", f(th, invalid)},
				{"Monospace", f(th, mono)},
				{"Disabled", f(th, disabled)},
				{"Multi-line", f(th, multi)},
			}
		},
	}
}

func checkboxPage() *page {
	boxes := []*atoms.Checkbox{
		atoms.NewCheckbox("Unchecked", atoms.Unchecked),
		atoms.NewCheckbox("Checked", atoms.Checked),
		atoms.NewCheckbox("Indeterminate", atoms.Indeterminate),
	}
	disabled := []*atoms.Checkbox{
		{Label: "Disabled", Disabled: true},
		{Label: "Disabled checked", State: atoms.Checked, Disabled: true},
		{Label: "Disabled partial", State: atoms.Indeterminate, Disabled: true},
	}
	// A parent derived from its children, as in the commit dialog.
	children := []*atoms.Checkbox{
		atoms.NewCheckbox("src/Mailer.php", atoms.Checked),
		atoms.NewCheckbox("src/Queue.php", atoms.Unchecked),
		atoms.NewCheckbox("tests/MailerTest.php", atoms.Checked),
	}
	parent := atoms.NewCheckbox("Changes (3 files)", atoms.Indeterminate)
	group := func(th *tokens.Theme) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if parent.Update(gtx) {
				for _, c := range children {
					c.State = parent.State
				}
			}
			changed := false
			for _, c := range children {
				changed = c.Update(gtx) || changed
			}
			if changed {
				n := 0
				for _, c := range children {
					if c.Checked() {
						n++
					}
				}
				switch n {
				case 0:
					parent.State = atoms.Unchecked
				case len(children):
					parent.State = atoms.Checked
				default:
					parent.State = atoms.Indeterminate
				}
			}
			ws := []layout.Widget{func(gtx layout.Context) layout.Dimensions { return parent.Layout(gtx, th) }}
			for _, c := range children {
				ws = append(ws, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.XL}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return c.Layout(gtx, th) })
				})
			}
			return column(0, ws...)(gtx)
		}
	}
	list := func(th *tokens.Theme, cs []*atoms.Checkbox) layout.Widget {
		ws := make([]layout.Widget, len(cs))
		for i, c := range cs {
			ws[i] = func(gtx layout.Context) layout.Dimensions { return c.Layout(gtx, th) }
		}
		return flow(th.Space.XL, ws...)
	}
	return &page{
		Level: "Atoms", Name: "Checkbox", Glyph: atoms.GlyphDone,
		Summary: "Tri-state checkbox. Users toggle checked/unchecked; indeterminate is derived by the program.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"States", list(th, boxes)},
				{"Disabled", list(th, disabled)},
				{"Parent / children (commit dialog pattern)", group(th)},
			}
		},
	}
}

func badgePage() *page {
	return &page{
		Level: "Atoms", Name: "Badge", Glyph: atoms.GlyphInfo,
		Summary: "Small pill for counts, versions and states. Subtle (tinted) or solid.",
		Variants: func(th *tokens.Theme) []variant {
			var subtle, solid []layout.Widget
			for _, k := range atoms.BadgeKinds {
				subtle = append(subtle, atoms.Badge{Text: k.String(), Kind: k}.Fn(th))
				solid = append(solid, atoms.Badge{Text: k.String(), Kind: k, Solid: true}.Fn(th))
			}
			return []variant{
				{"Subtle", flow(th.Space.S, subtle...)},
				{"Solid", flow(th.Space.S, solid...)},
				{"Examples", flow(th.Space.S,
					atoms.Badge{Text: "PHP 8.3", Kind: atoms.BadgeInfo}.Fn(th),
					atoms.Badge{Text: "3", Kind: atoms.BadgeAccent, Solid: true}.Fn(th),
					atoms.Badge{Text: "deprecated", Kind: atoms.BadgeWarning}.Fn(th),
					atoms.Badge{Text: "12 errors", Kind: atoms.BadgeDanger}.Fn(th),
				)},
			}
		},
	}
}

func tooltipPage() *page {
	tip := &atoms.Tooltip{Text: "Run 'index.php' (⌃R)"}
	btn := &atoms.Button{Kind: atoms.Secondary, Text: "Hover me", Glyph: atoms.GlyphRun}
	return &page{
		Level: "Atoms", Name: "Tooltip", Glyph: atoms.GlyphInfo,
		Summary: "Shown below a widget after the pointer rests on it for 500ms. TooltipBubble is the static part.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Bubble", flow(th.Space.L,
					atoms.TooltipBubble{Text: "Commit and push"}.Fn(th),
					fixedWidth(320, atoms.TooltipBubble{Text: "Long tooltips wrap: the interpreter could not be found in the configured Docker Compose service."}.Fn(th)),
				)},
				{"Hover", func(gtx layout.Context) layout.Dimensions {
					d := tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return btn.Layout(gtx, th) })
					d.Size.Y += gtx.Dp(32) // room for the bubble
					return d
				}},
			}
		},
	}
}

func dividerPage() *page {
	return &page{
		Level: "Atoms", Name: "Divider", Glyph: atoms.GlyphRemove,
		Summary: "1dp separator in the border color, horizontal or vertical.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Horizontal", column(th.Space.S, atoms.Text("Above").Fn(th), atoms.Divider{}.Fn(th), atoms.Text("Below").Fn(th))},
				{"Vertical (in a toolbar)", func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.Y = gtx.Dp(24)
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(atoms.Text("Left").Fn(th)),
						layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.Y = gtx.Dp(24)
							return atoms.Divider{Vertical: true}.Layout(gtx, th)
						}),
						layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
						layout.Rigid(atoms.Text("Right").Fn(th)),
					)
				}},
			}
		},
	}
}

// ---- Molecules -------------------------------------------------------------

func searchFieldPage() *page {
	empty := molecules.NewSearchField("Search everywhere")
	filled := molecules.NewSearchField("Search")
	filled.SetText("UserRepository")
	return &page{
		Level: "Molecules", Name: "SearchField", Glyph: atoms.GlyphSearch,
		Summary: "TextField + search glyph + clear button (appears once text is entered).",
		Variants: func(th *tokens.Theme) []variant {
			w := func(s *molecules.SearchField) layout.Widget {
				return fixedWidth(360, func(gtx layout.Context) layout.Dimensions {
					s.Update(gtx)
					return s.Layout(gtx, th)
				})
			}
			return []variant{
				{"Empty", w(empty)},
				{"With query (click × to clear)", w(filled)},
			}
		},
	}
}

func fileRowPage() *page {
	rows := []*molecules.FileRow{
		{Name: "tiegel-demo", Dir: true, Expanded: true},
		{Name: "src", Depth: 1, Dir: true, Expanded: true},
		{Name: "Controller", Depth: 2, Dir: true},
		{Name: "Mailer.php", Depth: 2, Status: molecules.StatusModified, Glyph: atoms.GlyphFileCode, Selected: true},
		{Name: "Queue.php", Depth: 2, Status: molecules.StatusAdded, Glyph: atoms.GlyphFileCode},
		{Name: "Legacy.php", Depth: 2, Status: molecules.StatusDeleted, Glyph: atoms.GlyphFileCode},
		{Name: "Scratch.php", Depth: 2, Status: molecules.StatusUntracked, Glyph: atoms.GlyphFileCode},
		{Name: "Merge.php", Depth: 2, Status: molecules.StatusConflict, Glyph: atoms.GlyphFileCode},
		{Name: "vendor", Depth: 1, Dir: true, Status: molecules.StatusIgnored},
		{Name: "composer.json", Depth: 1, Glyph: atoms.GlyphFileText, Detail: "~/projects/tiegel-demo"},
	}
	return &page{
		Level: "Molecules", Name: "FileRow", Glyph: atoms.GlyphFolder,
		Summary: "File tree / list row: indentation, arrow, icon, VCS status color, selection.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Tree (click rows to select, arrows to expand)", fixedWidth(360, func(gtx layout.Context) layout.Dimensions {
					for _, r := range rows {
						if clicked, _ := r.Update(gtx); clicked {
							for _, o := range rows {
								o.Selected = o == r
							}
						}
					}
					ws := make([]layout.Widget, len(rows))
					for i, r := range rows {
						ws[i] = func(gtx layout.Context) layout.Dimensions { return r.Layout(gtx, th) }
					}
					return column(0, ws...)(gtx)
				})},
			}
		},
	}
}

func tabPage() *page {
	tabs := []*molecules.Tab{
		{Title: "Mailer.php", Glyph: atoms.GlyphFileCode, Active: true, Tooltip: "src/Mailer.php"},
		{Title: "Queue.php", Glyph: atoms.GlyphFileCode, Modified: true, Tooltip: "src/Queue.php"},
		{Title: "composer.json", Glyph: atoms.GlyphFileText},
		{Title: "docker-compose.yml", Glyph: atoms.GlyphFileText, Modified: true},
	}
	closed := ""
	return &page{
		Level: "Molecules", Name: "Tab", Glyph: atoms.GlyphFileCode,
		Summary: "Editor tab: active underline, modified dot, close button. Fixed width across states.",
		Variants: func(th *tokens.Theme) []variant {
			bar := func(gtx layout.Context) layout.Dimensions {
				for _, t := range tabs {
					sel, cl := t.Update(gtx)
					if sel {
						for _, o := range tabs {
							o.Active = o == t
						}
					}
					if cl {
						closed = t.Title
					}
				}
				fc := make([]layout.FlexChild, len(tabs))
				for i, t := range tabs {
					fc[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th) })
				}
				m := recordOps(gtx, func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, fc...) })
				size := image.Pt(gtx.Constraints.Max.X, m.dims.Size.Y)
				paint.FillShape(gtx.Ops, th.Color.SurfaceAlt, clip.Rect{Max: size}.Op())
				m.addAt(gtx, image.Point{})
				return layout.Dimensions{Size: size}
			}
			msg := "Click a tab to activate it, × to close (demo only logs)."
			if closed != "" {
				msg = "Close requested: " + closed
			}
			return []variant{
				{"Tab bar", bar},
				{"Events", atoms.Muted(msg).Fn(th)},
			}
		},
	}
}

func breakpointPage() *page {
	rows := []*molecules.BreakpointRow{
		molecules.NewBreakpointRow("src/Mailer.php", 42, ""),
		molecules.NewBreakpointRow("src/Mailer.php", 57, "$retries > 3"),
		molecules.NewBreakpointRow("src/Queue.php", 12, ""),
		molecules.NewBreakpointRow("public/index.php", 3, "isset($_GET['debug'])"),
	}
	rows[2].Enabled.State = atoms.Unchecked
	rows[3].Enabled.State = atoms.Unchecked
	rows[0].Selected = true
	return &page{
		Level: "Molecules", Name: "BreakpointRow", Glyph: atoms.GlyphDebug,
		Summary: "Breakpoint list entry: enable checkbox, marker (ring = disabled, bar = conditional), location, condition.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Breakpoints", fixedWidth(520, func(gtx layout.Context) layout.Dimensions {
					for _, r := range rows {
						if clicked, _ := r.Update(gtx); clicked {
							for _, o := range rows {
								o.Selected = o == r
							}
						}
					}
					ws := make([]layout.Widget, len(rows))
					for i, r := range rows {
						ws[i] = func(gtx layout.Context) layout.Dimensions { return r.Layout(gtx, th) }
					}
					return column(0, ws...)(gtx)
				})},
			}
		},
	}
}

func diffPage() *page {
	lines := []molecules.DiffLine{
		{Kind: molecules.DiffContext, OldNo: 55, NewNo: 55, Text: "    public function send(Message $msg): bool"},
		{Kind: molecules.DiffContext, OldNo: 56, NewNo: 56, Text: "    {"},
		{Kind: molecules.DiffRemoved, OldNo: 57, Text: "        return $this->transport->send($msg);"},
		{Kind: molecules.DiffAdded, NewNo: 57, Text: "        if ($msg->retries >= self::MAX_RETRIES) {"},
		{Kind: molecules.DiffAdded, NewNo: 58, Text: "            return false;"},
		{Kind: molecules.DiffAdded, NewNo: 59, Text: "        }"},
		{Kind: molecules.DiffAdded, NewNo: 60, Text: "        return $this->transport->send($msg);"},
		{Kind: molecules.DiffContext, OldNo: 58, NewNo: 61, Text: "    }"},
	}
	plain := &molecules.DiffBlock{Header: "@@ -55,4 +55,7 @@ final class Mailer", Lines: lines}
	sel := &molecules.DiffBlock{Header: "@@ -55,4 +55,7 @@ final class Mailer", Lines: lines, Selectable: true}
	sel.Include.State = atoms.Checked
	return &page{
		Level: "Molecules", Name: "DiffBlock", Glyph: atoms.GlyphBranch,
		Summary: "One hunk with old/new line numbers and +/− markers. Selectable variant for partial commits.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Hunk", func(gtx layout.Context) layout.Dimensions { return plain.Layout(gtx, th) }},
				{"Selectable hunk (commit dialog)", func(gtx layout.Context) layout.Dimensions { return sel.Layout(gtx, th) }},
			}
		},
	}
}
