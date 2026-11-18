package tokens

import "image/color"

// TerminalPalette holds the colors of the embedded terminal: the 16
// ANSI colors programs select by number (0–7 normal, 8–15 bright), the
// default foreground and background, cursor, selection and file links.
type TerminalPalette struct {
	ANSI       [16]color.NRGBA
	Foreground color.NRGBA
	Background color.NRGBA
	Cursor     color.NRGBA
	Selection  color.NRGBA
	Link       color.NRGBA
}

func darkTerminal() TerminalPalette {
	return TerminalPalette{
		ANSI: [16]color.NRGBA{
			Hex(0x2B2E34), Hex(0xE07070), Hex(0x84BA82), Hex(0xD9B464),
			Hex(0x78A9D6), Hex(0xC08CC8), Hex(0x6DBDB8), Hex(0xC9CED6),
			Hex(0x5F6671), Hex(0xF08A85), Hex(0x9ED69B), Hex(0xEDCB7E),
			Hex(0x93BFE8), Hex(0xD6A6DE), Hex(0x8AD4CF), Hex(0xF0F2F5),
		},
		Foreground: Hex(0xD3D7DD),
		Background: Hex(0x1D1F24),
		Cursor:     Hex(0xE59A60),
		Selection:  Hex(0x33435A),
		Link:       Hex(0xE59A60),
	}
}

func lightTerminal() TerminalPalette {
	return TerminalPalette{
		ANSI: [16]color.NRGBA{
			Hex(0x2B2E34), Hex(0xB83A3A), Hex(0x3E7B3C), Hex(0x8A6A12),
			Hex(0x2F64A6), Hex(0x8A4A96), Hex(0x2A7F7A), Hex(0xC9CED6),
			Hex(0x5F6671), Hex(0xD24F4F), Hex(0x4F9A4C), Hex(0xA8821C),
			Hex(0x3F7BC4), Hex(0xA35FB0), Hex(0x369C96), Hex(0xF4F5F7),
		},
		Foreground: Hex(0x24272D),
		Background: Hex(0xFBFBFC),
		Cursor:     Hex(0xB8652E),
		Selection:  Hex(0xC9DAF0),
		Link:       Hex(0xA4561F),
	}
}

// cubeLevels are the six intensities of the xterm 6×6×6 color cube.
var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

// TerminalColor returns color i of the 256-color terminal palette: the
// theme's 16 ANSI colors, then the xterm 6×6×6 cube (16–231) and the
// gray ramp (232–255), which are fixed by the xterm convention.
func (th *Theme) TerminalColor(i uint8) color.NRGBA {
	switch {
	case i < 16:
		return th.Terminal.ANSI[i]
	case i < 232:
		n := i - 16
		return color.NRGBA{R: cubeLevels[n/36], G: cubeLevels[n/6%6], B: cubeLevels[n%6], A: 0xff}
	}
	g := 8 + 10*(i-232)
	return color.NRGBA{R: g, G: g, B: g, A: 0xff}
}
