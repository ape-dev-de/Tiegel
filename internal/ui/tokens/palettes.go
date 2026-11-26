package tokens

import "image/color"

// The Tiegel palette ("crucible"): cool graphite surfaces with a warm copper
// accent, like molten metal in a crucible. Syntax colors are muted pastels in
// dark mode and deeper inks in light mode. The values are original to Tiegel.

func darkPalette() Palette {
	return Palette{
		Background:   Hex(0x1A1C20),
		Surface:      Hex(0x212429),
		SurfaceAlt:   Hex(0x272A30),
		SurfaceHover: Hex(0x2E323A),
		Border:       Hex(0x343841),
		BorderStrong: Hex(0x474C57),
		Text:         Hex(0xD8DCE2),
		TextMuted:    Hex(0x9199A5),
		TextDisabled: Hex(0x5F6671),
		Accent:       Hex(0xD98A4E),
		AccentHover:  Hex(0xE59A60),
		OnAccent:     Hex(0x1A1C20),
		Danger:       Hex(0xE07070),
		Warning:      Hex(0xD9B464),
		Success:      Hex(0x84BA82),
		Info:         Hex(0x78A9D6),
		Selection:    Hex(0x2F3D4E),
		Focus:        Hex(0xD98A4E),
		TooltipBg:    Hex(0x3A3F48),
		TooltipText:  Hex(0xE8EBEF),
	}
}

func darkEditor() EditorPalette {
	return EditorPalette{
		Background:       Hex(0x1D1F24),
		CurrentLine:      Hex(0x24272D),
		Gutter:           Hex(0x1D1F24),
		LineNumber:       Hex(0x5C636E),
		LineNumberActive: Hex(0xA9B0BA),
		Cursor:           Hex(0xE59A60),
		Selection:        Hex(0x33435A),
		Text:             Hex(0xD3D7DD),
		Breakpoint:       Hex(0xDB5A55),
		Whitespace:       Hex(0x484E58),
		Ruler:            Hex(0x2D3138),
		Analysis:         Hex(0xA08A5C),
		DiffAddedBg:      Hex(0x223127),
		DiffRemovedBg:    Hex(0x3A2427),
		DiffAddedFg:      Hex(0x84BA82),
		DiffRemovedFg:    Hex(0xE07070),
		DiffHeaderBg:     Hex(0x252A33),
	}
}

func darkSyntax() SyntaxPalette {
	return SyntaxPalette{
		Keyword:     Hex(0xD99A6C),
		String:      Hex(0x9EC283),
		Number:      Hex(0x86B8D0),
		Comment:     Hex(0x707883),
		Variable:    Hex(0xC6A6DB),
		Function:    Hex(0xE2CA8C),
		Type:        Hex(0x72BCC4),
		Constant:    Hex(0xDB9393),
		Operator:    Hex(0xB9C1CB),
		Punctuation: Hex(0x98A1AC),
		Tag:         Hex(0xE09479),
		Attribute:   Hex(0xD5B987),
		Property:    Hex(0xA8C6E6),
	}
}

func darkGit() GitPalette {
	return GitPalette{
		Added:         Hex(0x5F9F68),
		Modified:      Hex(0x5D8DC4),
		Deleted:       Hex(0xC45E5E),
		FileAdded:     Hex(0x8CC48E),
		FileModified:  Hex(0x82AEE0),
		FileDeleted:   Hex(0x8E959F),
		FileUntracked: Hex(0xD9A06C),
		FileIgnored:   Hex(0x9C9A4F), // olive: ignored by .gitignore
		FileConflict:  Hex(0xE27D7D),
	}
}

func lightPalette() Palette {
	return Palette{
		Background:   Hex(0xF6F6F3),
		Surface:      Hex(0xFFFFFF),
		SurfaceAlt:   Hex(0xEEEEEA),
		SurfaceHover: Hex(0xE6E6E1),
		Border:       Hex(0xDADAD4),
		BorderStrong: Hex(0xB9B9B2),
		Text:         Hex(0x1E2227),
		TextMuted:    Hex(0x5A616B),
		TextDisabled: Hex(0xA3A8AF),
		Accent:       Hex(0xA9561C),
		AccentHover:  Hex(0x924914),
		OnAccent:     Hex(0xFFFFFF),
		Danger:       Hex(0xB83A30),
		Warning:      Hex(0x9A6800),
		Success:      Hex(0x3A7A42),
		Info:         Hex(0x2F6AA8),
		Selection:    Hex(0xD9E4F1),
		Focus:        Hex(0xA9561C),
		TooltipBg:    Hex(0x2B2F35),
		TooltipText:  Hex(0xF2F3F5),
	}
}

func lightEditor() EditorPalette {
	return EditorPalette{
		Background:       Hex(0xFFFFFF),
		CurrentLine:      Hex(0xF6F4EF),
		Gutter:           Hex(0xFAFAF8),
		LineNumber:       Hex(0x9AA0A8),
		LineNumberActive: Hex(0x4A5058),
		Cursor:           Hex(0xA9561C),
		Selection:        Hex(0xCADAEE),
		Text:             Hex(0x1E2227),
		Breakpoint:       Hex(0xCF3B33),
		Whitespace:       Hex(0xC4C8CE),
		Ruler:            Hex(0xE8E9EC),
		Analysis:         Hex(0xB89A5E),
		DiffAddedBg:      Hex(0xE3F2E2),
		DiffRemovedBg:    Hex(0xF8E1DF),
		DiffAddedFg:      Hex(0x2F6E37),
		DiffRemovedFg:    Hex(0xA3332A),
		DiffHeaderBg:     Hex(0xEBEFF4),
	}
}

func lightSyntax() SyntaxPalette {
	return SyntaxPalette{
		Keyword:     Hex(0x9C4A12),
		String:      Hex(0x3B7330),
		Number:      Hex(0x1E6894),
		Comment:     Hex(0x7F858E),
		Variable:    Hex(0x76409A),
		Function:    Hex(0x805400),
		Type:        Hex(0x116C7F),
		Constant:    Hex(0xA8394A),
		Operator:    Hex(0x474D55),
		Punctuation: Hex(0x666D76),
		Tag:         Hex(0x9E3F28),
		Attribute:   Hex(0x7E6118),
		Property:    Hex(0x2D5A8A),
	}
}

func lightGit() GitPalette {
	return GitPalette{
		Added:         Hex(0x55A160),
		Modified:      Hex(0x4C82C2),
		Deleted:       Hex(0xC75050),
		FileAdded:     Hex(0x2F7A38),
		FileModified:  Hex(0x2C62A3),
		FileDeleted:   Hex(0x7C828B),
		FileUntracked: Hex(0x9C5418),
		FileIgnored:   Hex(0x7E7B1E), // olive: ignored by .gitignore
		FileConflict:  Hex(0xB33A30),
	}
}

// Hex converts 0xRRGGBB into an opaque color.
func Hex(rgb uint32) color.NRGBA {
	return color.NRGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xFF}
}
