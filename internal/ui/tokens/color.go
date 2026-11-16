package tokens

import (
	"image/color"
	"math"
)

// WithAlpha returns c with its alpha channel set to a.
func WithAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// Mix blends a towards b by t in [0,1] (0 = a, 1 = b), per channel in sRGB.
func Mix(a, b color.NRGBA, t float32) color.NRGBA {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	lerp := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B), A: lerp(a.A, b.A)}
}

// Over composites the translucent color fg over the opaque color bg.
func Over(fg, bg color.NRGBA) color.NRGBA {
	out := Mix(bg, fg, float32(fg.A)/255)
	out.A = 0xFF
	return out
}

// RelativeLuminance returns the WCAG 2.x relative luminance of c (alpha is
// ignored).
func RelativeLuminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// ContrastRatio returns the WCAG contrast ratio between a and b (1…21).
func ContrastRatio(a, b color.NRGBA) float64 {
	la, lb := RelativeLuminance(a), RelativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
