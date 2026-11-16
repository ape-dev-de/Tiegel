// Command mkicon renders Tiegel's app icon (a stylised crucible with molten
// metal) from vector shapes defined in this file.
//
// The design is original and lives only here: the same shape list is written
// as PNG (for iconutil) and optionally as SVG (for documentation or editing).
//
//	go run ./packaging/macos/mkicon -png icon_1024.png -size 1024 -svg icon.svg
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"strings"

	"golang.org/x/image/vector"
)

// canvas is the design grid; all coordinates below refer to it.
const canvas = 1024

func main() {
	pngPath := flag.String("png", "", "write a PNG to this path")
	size := flag.Int("size", 1024, "PNG edge length in pixels")
	svgPath := flag.String("svg", "", "write an SVG to this path")
	flag.Parse()
	if err := run(*pngPath, *size, *svgPath); err != nil {
		fmt.Fprintln(os.Stderr, "mkicon:", err)
		os.Exit(1)
	}
}

func run(pngPath string, size int, svgPath string) error {
	if pngPath == "" && svgPath == "" {
		return fmt.Errorf("nothing to do: pass -png and/or -svg")
	}
	if size < 16 || size > 4096 {
		return fmt.Errorf("size %d out of range 16..4096", size)
	}
	shapes := design()
	if pngPath != "" {
		if err := writeFile(pngPath, func(w io.Writer) error {
			return png.Encode(w, render(shapes, size))
		}); err != nil {
			return err
		}
	}
	if svgPath != "" {
		if err := writeFile(svgPath, func(w io.Writer) error {
			_, err := io.WriteString(w, toSVG(shapes))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, fn func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := fn(f); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Design

func hex(s string) color.NRGBA {
	var r, g, b uint8
	fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{r, g, b, 0xff}
}

// design returns the icon as an ordered list of filled shapes (back to front).
func design() []shape {
	var out []shape

	// Background tile following the macOS icon grid (824 px tile, 100 px margin).
	out = append(out, shape{
		path: roundedRect(100, 100, 924, 924, 184),
		fill: linear(512, 100, 512, 924, hex("#2E3D58"), hex("#121826")),
	})
	// Sparks rising from the melt.
	for _, s := range []struct{ x, y, r float32 }{{448, 286, 16}, {560, 232, 12}, {612, 300, 10}, {500, 196, 8}} {
		out = append(out, shape{path: ellipse(s.x, s.y, s.r, s.r), fill: solid(hex("#FFC857"))})
	}
	// Molten surface; its lower half disappears behind the rim.
	out = append(out, shape{
		path: ellipse(512, 392, 238, 46),
		fill: linear(512, 346, 512, 438, hex("#FFD166"), hex("#FF6B1A")),
	})
	// Crucible body: a trapezoid narrowing downwards with a rounded base.
	out = append(out, shape{
		path: crucibleBody(),
		fill: linear(268, 0, 756, 0, hex("#ECE6DC"), hex("#9C9184")),
	})
	// Darker band around the body for depth.
	out = append(out, shape{
		path: polygon(pt{289, 560}, pt{735, 560}, pt{725, 600}, pt{299, 600}),
		fill: linear(290, 0, 734, 0, hex("#B9AFA2"), hex("#7D7366")),
	})
	// Rim.
	out = append(out, shape{
		path: roundedRect(232, 392, 792, 446, 27),
		fill: linear(232, 0, 792, 0, hex("#F7F3EC"), hex("#B7AEA2")),
	})
	// Glowing drip running down the inside edge of the rim.
	out = append(out, shape{
		path: drip(),
		fill: linear(0, 440, 0, 520, hex("#FF8A1F"), hex("#FF5A12")),
	})
	return out
}

func crucibleBody() path {
	var p path
	p.moveTo(262, 430)
	p.lineTo(762, 430)
	p.lineTo(690, 742)
	p.cubeTo(682, 778, 656, 796, 620, 796)
	p.lineTo(404, 796)
	p.cubeTo(368, 796, 342, 778, 334, 742)
	p.close()
	return p
}

func drip() path {
	var p path
	p.moveTo(596, 444)
	p.lineTo(636, 444)
	p.cubeTo(636, 470, 630, 496, 616, 512)
	p.cubeTo(602, 496, 596, 470, 596, 444)
	p.close()
	return p
}

// ---------------------------------------------------------------------------
// Geometry

type pt struct{ x, y float32 }

type segKind int

const (
	segMove segKind = iota
	segLine
	segCube
	segClose
)

type seg struct {
	kind segKind
	pts  [3]pt
}

type path struct{ segs []seg }

func (p *path) moveTo(x, y float32) { p.segs = append(p.segs, seg{kind: segMove, pts: [3]pt{{x, y}}}) }
func (p *path) lineTo(x, y float32) { p.segs = append(p.segs, seg{kind: segLine, pts: [3]pt{{x, y}}}) }
func (p *path) cubeTo(x1, y1, x2, y2, x, y float32) {
	p.segs = append(p.segs, seg{kind: segCube, pts: [3]pt{{x1, y1}, {x2, y2}, {x, y}}})
}
func (p *path) close() { p.segs = append(p.segs, seg{kind: segClose}) }

// kappa approximates a quarter circle with a cubic Bézier curve.
const kappa = 0.5522847

func roundedRect(x0, y0, x1, y1, r float32) path {
	k := r * kappa
	var p path
	p.moveTo(x0+r, y0)
	p.lineTo(x1-r, y0)
	p.cubeTo(x1-r+k, y0, x1, y0+r-k, x1, y0+r)
	p.lineTo(x1, y1-r)
	p.cubeTo(x1, y1-r+k, x1-r+k, y1, x1-r, y1)
	p.lineTo(x0+r, y1)
	p.cubeTo(x0+r-k, y1, x0, y1-r+k, x0, y1-r)
	p.lineTo(x0, y0+r)
	p.cubeTo(x0, y0+r-k, x0+r-k, y0, x0+r, y0)
	p.close()
	return p
}

func ellipse(cx, cy, rx, ry float32) path {
	kx, ky := rx*kappa, ry*kappa
	var p path
	p.moveTo(cx+rx, cy)
	p.cubeTo(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry)
	p.cubeTo(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy)
	p.cubeTo(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry)
	p.cubeTo(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy)
	p.close()
	return p
}

func polygon(pts ...pt) path {
	var p path
	for i, q := range pts {
		if i == 0 {
			p.moveTo(q.x, q.y)
		} else {
			p.lineTo(q.x, q.y)
		}
	}
	p.close()
	return p
}

// ---------------------------------------------------------------------------
// Paint

type paint struct {
	x0, y0, x1, y1 float32
	c0, c1         color.NRGBA
}

func solid(c color.NRGBA) paint { return paint{c0: c, c1: c} }

func linear(x0, y0, x1, y1 float32, c0, c1 color.NRGBA) paint {
	return paint{x0, y0, x1, y1, c0, c1}
}

func (pa paint) isSolid() bool { return pa.c0 == pa.c1 }

// at returns the colour at design coordinates (x, y).
func (pa paint) at(x, y float32) color.NRGBA {
	if pa.isSolid() {
		return pa.c0
	}
	dx, dy := pa.x1-pa.x0, pa.y1-pa.y0
	t := ((x-pa.x0)*dx + (y-pa.y0)*dy) / (dx*dx + dy*dy)
	t = float32(math.Max(0, math.Min(1, float64(t))))
	mix := func(a, b uint8) uint8 { return uint8(float32(a) + (float32(b)-float32(a))*t + 0.5) }
	return color.NRGBA{mix(pa.c0.R, pa.c1.R), mix(pa.c0.G, pa.c1.G), mix(pa.c0.B, pa.c1.B), mix(pa.c0.A, pa.c1.A)}
}

type shape struct {
	path path
	fill paint
}

// gradientImage adapts a paint to image.Image at a given output scale.
type gradientImage struct {
	pa    paint
	scale float32
}

func (g gradientImage) ColorModel() color.Model { return color.NRGBAModel }
func (g gradientImage) Bounds() image.Rectangle {
	return image.Rect(-1e9, -1e9, 1e9, 1e9)
}
func (g gradientImage) At(x, y int) color.Color {
	return g.pa.at((float32(x)+0.5)/g.scale, (float32(y)+0.5)/g.scale)
}

// render rasterises shapes into a size×size RGBA image.
func render(shapes []shape, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := float32(size) / canvas
	for _, s := range shapes {
		r := vector.NewRasterizer(size, size)
		r.DrawOp = draw.Over
		for _, sg := range s.path.segs {
			q := sg.pts
			switch sg.kind {
			case segMove:
				r.MoveTo(q[0].x*scale, q[0].y*scale)
			case segLine:
				r.LineTo(q[0].x*scale, q[0].y*scale)
			case segCube:
				r.CubeTo(q[0].x*scale, q[0].y*scale, q[1].x*scale, q[1].y*scale, q[2].x*scale, q[2].y*scale)
			case segClose:
				r.ClosePath()
			}
		}
		var src image.Image = gradientImage{pa: s.fill, scale: scale}
		if s.fill.isSolid() {
			src = image.NewUniform(s.fill.c0)
		}
		r.Draw(dst, dst.Bounds(), src, image.Point{})
	}
	return dst
}

// toSVG writes the shapes as a standalone SVG document.
func toSVG(shapes []shape) string {
	var defs, body strings.Builder
	for i, s := range shapes {
		fill := svgColor(s.fill.c0)
		if !s.fill.isSolid() {
			id := fmt.Sprintf("g%d", i)
			fmt.Fprintf(&defs, `<linearGradient id="%s" gradientUnits="userSpaceOnUse" x1="%g" y1="%g" x2="%g" y2="%g">`+
				`<stop offset="0" stop-color="%s" stop-opacity="%s"/><stop offset="1" stop-color="%s" stop-opacity="%s"/></linearGradient>`+"\n",
				id, s.fill.x0, s.fill.y0, s.fill.x1, s.fill.y1,
				svgColor(s.fill.c0), svgOpacity(s.fill.c0), svgColor(s.fill.c1), svgOpacity(s.fill.c1))
			fill = "url(#" + id + ")"
		}
		fmt.Fprintf(&body, `<path fill="%s" d="%s"/>`+"\n", fill, svgPath(s.path))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%[1]d" height="%[1]d" viewBox="0 0 %[1]d %[1]d">
<defs>
%[2]s</defs>
%[3]s</svg>
`, canvas, defs.String(), body.String())
}

func svgPath(p path) string {
	var b strings.Builder
	for _, sg := range p.segs {
		q := sg.pts
		switch sg.kind {
		case segMove:
			fmt.Fprintf(&b, "M%g %g", q[0].x, q[0].y)
		case segLine:
			fmt.Fprintf(&b, "L%g %g", q[0].x, q[0].y)
		case segCube:
			fmt.Fprintf(&b, "C%g %g %g %g %g %g", q[0].x, q[0].y, q[1].x, q[1].y, q[2].x, q[2].y)
		case segClose:
			b.WriteString("Z")
		}
	}
	return b.String()
}

func svgColor(c color.NRGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func svgOpacity(c color.NRGBA) string { return fmt.Sprintf("%.3g", float64(c.A)/255) }
