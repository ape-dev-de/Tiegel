// Package mermaid parses Mermaid diagram sources ("```mermaid" blocks of
// Markdown documents) and lays them out into a renderer-neutral, geometric
// model: filled and stroked paths plus positioned text. The GUI draws the
// model with Gio (internal/ui/organisms/mdview), headless clients get it as
// JSON or as SVG (Diagram.SVG). Nothing is executed or fetched; the
// package is a clean-room implementation of a subset of the Mermaid
// syntax (D551).
//
// Supported diagram types: flowchart/graph, sequenceDiagram,
// stateDiagram(-v2), classDiagram, erDiagram and pie. Text is measured by
// an injectable Measurer so boxes fit the renderer's real font.
//
// The package imports no Gio.
package mermaid

import (
	"fmt"
	"math"
)

// Point is a position in diagram units (1 unit ≈ 1 dp at FontSize 14).
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Rect is an axis-aligned rectangle.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Center returns the center of r.
func (r Rect) Center() Point { return Point{r.X + r.W/2, r.Y + r.H/2} }

// Overlaps reports whether r and o share an interior area.
func (r Rect) Overlaps(o Rect) bool {
	return r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H
}

// SegOp is the operation of a path segment.
type SegOp uint8

// Path operations. Pts holds 1 (Move, Line), 2 (Quad: control, end) or 3
// (Cube: two controls, end) points; Close has none.
const (
	Move SegOp = iota
	Line
	Quad
	Cube
	Close
)

// Seg is one segment of a path.
type Seg struct {
	Op  SegOp   `json:"op"`
	Pts []Point `json:"pts,omitempty"`
}

// Role is the theme role of a paint; renderers map roles to their theme
// colors (light/dark).
type Role uint8

// Paint roles.
const (
	RoleNone          Role = iota // not painted
	RoleNodeFill                  // node background
	RoleNodeStroke                // node outline
	RoleClusterFill               // subgraph/composite state/block background
	RoleClusterStroke             // subgraph outline
	RoleLine                      // edges, messages, lifelines, arrow heads
	RoleLabelBg                   // background behind edge labels
	RoleText                      // primary text
	RoleTextMuted                 // secondary text (block conditions, numbers)
	RoleNoteFill                  // notes
	RoleNoteStroke                // note outline
	RoleActivation                // sequence activation bars
	RoleBackground                // the diagram background (hollow markers)
	RoleAccent                    // emphasized marks (autonumber badges, start states)
	RoleOnAccent                  // text on RoleAccent
	RolePie                       // pie slice; Paint.Index selects the series color
)

// Paint is a fill or stroke: a theme role, optionally overridden by an
// explicit color from the diagram source ("#rrggbb" or "#rrggbbaa").
type Paint struct {
	Role  Role   `json:"role"`
	Color string `json:"color,omitempty"`
	// Index is the series index of RolePie.
	Index int `json:"index,omitempty"`
}

// Dash is the stroke pattern.
type Dash uint8

// Stroke patterns.
const (
	Solid Dash = iota
	Dotted
	Dashed
)

// Anchor is the horizontal alignment of text at its X.
type Anchor uint8

// Text anchors.
const (
	AnchorMiddle Anchor = iota
	AnchorStart
	AnchorEnd
)

// Text is a block of lines. Y is the baseline of the first line; further
// lines follow every LineHeight.
type Text struct {
	X          float64  `json:"x"`
	Y          float64  `json:"y"`
	Lines      []string `json:"lines"`
	Size       float64  `json:"size"`
	LineHeight float64  `json:"lineHeight"`
	Bold       bool     `json:"bold,omitempty"`
	Italic     bool     `json:"italic,omitempty"`
	Anchor     Anchor   `json:"anchor,omitempty"`
	Paint      Paint    `json:"paint"`
}

// Item is one drawing primitive: a path (filled and/or stroked) or, with
// Text set, a text block. Items are painted in order.
type Item struct {
	Path   []Seg   `json:"path,omitempty"`
	Fill   Paint   `json:"fill"`
	Stroke Paint   `json:"stroke"`
	Width  float64 `json:"width,omitempty"`
	Dash   Dash    `json:"dash,omitempty"`
	Text   *Text   `json:"text,omitempty"`
}

// NodeInfo describes a laid-out node (box, participant, class, entity,
// state) for tests and headless clients.
type NodeInfo struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Shape string `json:"shape,omitempty"`
	Box   Rect   `json:"box"`
}

// EdgeInfo describes a laid-out edge or message: its route from the
// source border to the target border (arrow tip included).
type EdgeInfo struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Label  string  `json:"label,omitempty"`
	Points []Point `json:"points"`
}

// Diagram is a laid-out diagram. Coordinates start at (0, 0); Width and
// Height include a margin.
type Diagram struct {
	Kind   string     `json:"kind"`
	Width  float64    `json:"width"`
	Height float64    `json:"height"`
	Items  []Item     `json:"items"`
	Nodes  []NodeInfo `json:"nodes,omitempty"`
	Edges  []EdgeInfo `json:"edges,omitempty"`
}

// Measurer returns the advance width of s at font size in diagram units.
type Measurer func(s string, size float64, bold bool) float64

// Options configure layout. The zero value is valid.
type Options struct {
	// Measure measures text; nil uses EstimateWidth.
	Measure Measurer
	// FontSize is the base text size (default 14).
	FontSize float64
}

// EstimateWidth is the default Measurer: a proportional sans-serif
// estimate (0.6 em per character, 1 em for wide East Asian characters).
func EstimateWidth(s string, size float64, bold bool) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r >= 0x1100 && (r <= 0x115f || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) ||
			(r >= 0xf900 && r <= 0xfaff) || (r >= 0xff00 && r <= 0xff60) || r >= 0x1f300):
			w += 1
		case r == ' ' || r == 'i' || r == 'l' || r == 'j' || r == '.' || r == ',' || r == '\'' || r == '|' || r == '!' || r == ':' || r == ';':
			w += 0.3
		case r >= 'A' && r <= 'Z', r == 'm', r == 'w', r == '@', r == '%':
			w += 0.72
		default:
			w += 0.56
		}
	}
	if bold {
		w *= 1.06
	}
	return w * size
}

// Error is a parse error with the 1-based source line (0: whole source).
type Error struct {
	Line int    `json:"line"`
	Msg  string `json:"message"`
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("mermaid: line %d: %s", e.Line, e.Msg)
	}
	return "mermaid: " + e.Msg
}

func errorf(line int, format string, args ...any) *Error {
	return &Error{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// env carries the options during layout.
type env struct {
	measure Measurer
	size    float64 // base font size
	lh      float64 // line height
}

func newEnv(o Options) *env {
	e := &env{measure: o.Measure, size: o.FontSize}
	if e.measure == nil {
		e.measure = EstimateWidth
	}
	if e.size <= 0 {
		e.size = 14
	}
	e.lh = math.Round(e.size*1.35*100) / 100
	return e
}

// textSize returns the width and height of lines at size (bold).
func (e *env) textSize(lines []string, size float64, bold bool) (w, h float64) {
	for _, l := range lines {
		w = max(w, e.measure(l, size, bold))
	}
	return math.Ceil(w), float64(len(lines)) * e.lineHeight(size)
}

func (e *env) lineHeight(size float64) float64 { return e.lh * size / e.size }

// text returns a text block centered on (cx, cy).
func (e *env) text(lines []string, cx, cy, size float64, bold bool, p Paint) *Text {
	lh := e.lineHeight(size)
	h := float64(len(lines)) * lh
	return &Text{X: cx, Y: cy - h/2 + lh/2 + size*0.35, Lines: lines, Size: size, LineHeight: lh, Bold: bold, Paint: p}
}

// textAt returns a text block with its first line's top at y and the given
// anchor at x.
func (e *env) textAt(lines []string, x, y, size float64, bold bool, a Anchor, p Paint) *Text {
	lh := e.lineHeight(size)
	return &Text{X: x, Y: y + lh/2 + size*0.35, Lines: lines, Size: size, LineHeight: lh, Bold: bold, Anchor: a, Paint: p}
}
