package mermaid

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// Theme maps paint roles to CSS colors for SVG export.
type Theme struct {
	Colors map[Role]string
	// Pie holds the series colors of pie slices (cycled).
	Pie []string
	// Font is the CSS font-family of text.
	Font string
}

// LightTheme and DarkTheme are the default SVG themes, close to Tiegel's
// light and dark UI palettes.
var (
	LightTheme = Theme{
		Colors: map[Role]string{
			RoleNodeFill: "#eef3fb", RoleNodeStroke: "#5b7fbf", RoleClusterFill: "#f6f7f9", RoleClusterStroke: "#c9ced6",
			RoleLine: "#5c6370", RoleLabelBg: "#ffffff", RoleText: "#1f2328", RoleTextMuted: "#6a737d",
			RoleNoteFill: "#fff8db", RoleNoteStroke: "#d4a72c", RoleActivation: "#e8ebef", RoleBackground: "#ffffff",
			RoleAccent: "#3e6fc4", RoleOnAccent: "#ffffff",
		},
		Pie:  []string{"#4e79a7", "#f28e2b", "#59a14f", "#e15759", "#76b7b2", "#edc948", "#b07aa1", "#ff9da7", "#9c755f", "#bab0ac"},
		Font: "-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif",
	}
	DarkTheme = Theme{
		Colors: map[Role]string{
			RoleNodeFill: "#1f2a3a", RoleNodeStroke: "#6d93d6", RoleClusterFill: "#20232a", RoleClusterStroke: "#3d434d",
			RoleLine: "#a0a8b4", RoleLabelBg: "#181a1f", RoleText: "#e6e8eb", RoleTextMuted: "#9aa3ad",
			RoleNoteFill: "#3a3420", RoleNoteStroke: "#b8962e", RoleActivation: "#2c3038", RoleBackground: "#181a1f",
			RoleAccent: "#6d93d6", RoleOnAccent: "#0f1115",
		},
		Pie:  []string{"#6d93d6", "#f2a65a", "#7cc47f", "#e8797b", "#8fd0ca", "#f0d264", "#c79bc0", "#ffb3bb", "#b9927a", "#c8c0bc"},
		Font: "-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif",
	}
)

// color resolves p in the theme ("none" when unpainted).
func (t Theme) color(p Paint) string {
	if p.Color != "" {
		return p.Color
	}
	if p.Role == RoleNone {
		return "none"
	}
	if p.Role == RolePie {
		if len(t.Pie) == 0 {
			return "#888888"
		}
		return t.Pie[p.Index%len(t.Pie)]
	}
	if c, ok := t.Colors[p.Role]; ok {
		return c
	}
	return "#888888"
}

func num(f float64) string {
	f = math.Round(f*100) / 100
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// PathData returns the SVG path data of segs.
func PathData(segs []Seg) string {
	var b strings.Builder
	for _, s := range segs {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		switch s.Op {
		case Move:
			b.WriteString("M" + num(s.Pts[0].X) + " " + num(s.Pts[0].Y))
		case Line:
			b.WriteString("L" + num(s.Pts[0].X) + " " + num(s.Pts[0].Y))
		case Quad:
			b.WriteString("Q" + num(s.Pts[0].X) + " " + num(s.Pts[0].Y) + " " + num(s.Pts[1].X) + " " + num(s.Pts[1].Y))
		case Cube:
			b.WriteString("C" + num(s.Pts[0].X) + " " + num(s.Pts[0].Y) + " " + num(s.Pts[1].X) + " " + num(s.Pts[1].Y) +
				" " + num(s.Pts[2].X) + " " + num(s.Pts[2].Y))
		case Close:
			b.WriteString("Z")
		}
	}
	return b.String()
}

// SVG renders the diagram as a standalone SVG document.
func (d *Diagram) SVG(t Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s">`+"\n",
		num(d.Width), num(d.Height), num(d.Width), num(d.Height))
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="%s"/>`+"\n", t.color(Paint{Role: RoleBackground}))
	for _, it := range d.Items {
		if it.Text != nil {
			writeText(&b, t, it.Text)
			continue
		}
		fmt.Fprintf(&b, `<path d="%s" fill="%s"`, PathData(it.Path), t.color(it.Fill))
		if it.Stroke.Role != RoleNone || it.Stroke.Color != "" {
			w := it.Width
			if w == 0 {
				w = 1
			}
			fmt.Fprintf(&b, ` stroke="%s" stroke-width="%s" stroke-linejoin="round" stroke-linecap="round"`, t.color(it.Stroke), num(w))
			switch it.Dash {
			case Dotted:
				fmt.Fprintf(&b, ` stroke-dasharray="%s %s"`, num(2*w), num(3*w))
			case Dashed:
				fmt.Fprintf(&b, ` stroke-dasharray="%s %s"`, num(5*w), num(4*w))
			}
		}
		b.WriteString("/>\n")
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func writeText(b *strings.Builder, t Theme, tx *Text) {
	anchor := "middle"
	switch tx.Anchor {
	case AnchorStart:
		anchor = "start"
	case AnchorEnd:
		anchor = "end"
	}
	extra := ""
	if tx.Bold {
		extra += ` font-weight="bold"`
	}
	if tx.Italic {
		extra += ` font-style="italic"`
	}
	for i, l := range tx.Lines {
		if l == "" {
			continue
		}
		fmt.Fprintf(b, `<text x="%s" y="%s" font-family="%s" font-size="%s" fill="%s" text-anchor="%s"%s>%s</text>`+"\n",
			num(tx.X), num(tx.Y+float64(i)*tx.LineHeight), html.EscapeString(t.Font), num(tx.Size), t.color(tx.Paint), anchor, extra,
			html.EscapeString(l))
	}
}
