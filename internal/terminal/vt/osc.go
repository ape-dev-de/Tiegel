package vt

import (
	"encoding/base64"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits of interned hyperlinks.
const (
	maxLinks   = 1<<16 - 1
	maxLinkURL = 2048
)

func (t *Terminal) oscDispatch() {
	s := string(t.p.osc)
	if cap(t.p.osc) > 64<<10 {
		t.p.osc = nil // drop a large clipboard buffer
	}
	num, rest, _ := strings.Cut(s, ";")
	switch num {
	case "0", "2":
		t.title = sanitize(rest)
	case "1": // icon name
	case "7":
		t.cwd = fileURLPath(rest)
	case "8":
		t.hyperlink(rest)
	case "52":
		t.clipboard(rest)
	case "133":
		t.shellMark(rest)
	case "4":
		t.paletteQuery(rest)
	case "10", "11", "12":
		t.colorQuery(num, rest)
	case "104", "110", "111", "112":
		// Palette resets: programs cannot change the palette, nothing
		// to reset.
	default:
		if t.OSC != nil {
			t.OSC(num, rest)
		}
	}
}

// oscReply answers with the terminator of the request.
func (t *Terminal) oscReply(s string) {
	if t.p.oscBEL {
		t.reply("\x1b]" + s + "\x07")
		return
	}
	t.reply("\x1b]" + s + "\x1b\\")
}

// hyperlink handles OSC 8 ; params ; URI: an empty URI ends the link.
// Links with the same id= parameter (or the same URI without id) share
// one entry, so a link split over lines stays one link.
func (t *Terminal) hyperlink(rest string) {
	params, uri, ok := strings.Cut(rest, ";")
	if !ok || uri == "" {
		t.pen.Link = 0
		return
	}
	uri = sanitize(uri)
	if len(uri) > maxLinkURL {
		t.pen.Link = 0
		return
	}
	id := ""
	for _, kv := range strings.Split(params, ":") {
		if v, ok := strings.CutPrefix(kv, "id="); ok {
			id = v
		}
	}
	key := id + "\x00" + uri
	if t.linkIndex == nil {
		t.linkIndex = map[string]uint16{}
	}
	if n, ok := t.linkIndex[key]; ok {
		t.pen.Link = n
		return
	}
	if len(t.links) >= maxLinks {
		t.pen.Link = 0
		return
	}
	t.links = append(t.links, uri)
	n := uint16(len(t.links))
	t.linkIndex[key] = n
	t.pen.Link = n
}

// clipboard handles OSC 52 ; targets ; base64: writes go to the
// Clipboard callback; queries ("?") are never answered (the clipboard
// may hold secrets).
func (t *Terminal) clipboard(rest string) {
	target, data, ok := strings.Cut(rest, ";")
	if !ok || data == "?" || t.Clipboard == nil {
		return
	}
	if target == "" {
		target = "s0"
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(data)
		if err != nil {
			return
		}
	}
	if !utf8.Valid(b) {
		return
	}
	t.Clipboard(target, string(b))
}

// shellMark handles OSC 133 (FinalTerm shell integration): A prompt
// start, B command input, C output start, D;exit command end.
func (t *Terminal) shellMark(rest string) {
	kind, args, _ := strings.Cut(rest, ";")
	switch kind {
	case "A":
		t.markLine(MarkPrompt)
	case "B":
		t.markLine(MarkInput)
	case "C":
		t.markLine(MarkOutput)
	case "D":
		t.markLine(MarkDone)
		t.lastExit = -1
		code, _, _ := strings.Cut(args, ";")
		if n, err := strconv.Atoi(code); err == nil {
			t.lastExit = n
		}
	}
}

// rgbSpec formats a color like xterm (rgb:rrrr/gggg/bbbb).
func rgbSpec(c [3]uint8) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	b.WriteString("rgb:")
	for i, v := range c {
		if i > 0 {
			b.WriteByte('/')
		}
		h := []byte{hex[v>>4], hex[v&15]}
		b.Write(h)
		b.Write(h)
	}
	return b.String()
}

// colorQuery answers OSC 10/11/12 ; ? (foreground, background, cursor).
// Programs may chain queries (OSC 10;?;? asks for 10 and 11). Setting
// the colors is not supported.
func (t *Terminal) colorQuery(num, rest string) {
	n, _ := strconv.Atoi(num)
	for _, q := range strings.Split(rest, ";") {
		if q == "?" {
			var c [3]uint8
			switch n {
			case 10:
				c = t.Colors.FG
			case 11:
				c = t.Colors.BG
			case 12:
				c = t.Colors.Cursor
			default:
				return
			}
			t.oscReply(strconv.Itoa(n) + ";" + rgbSpec(c))
		}
		n++
	}
}

// paletteQuery answers OSC 4 ; n ; ? pairs.
func (t *Terminal) paletteQuery(rest string) {
	parts := strings.Split(rest, ";")
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i+1] != "?" {
			continue
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 || n > 255 {
			continue
		}
		c := PaletteRGB(uint8(n))
		if n < 16 {
			c = t.Colors.ANSI[n]
		}
		t.oscReply("4;" + parts[i] + ";" + rgbSpec(c))
	}
}

// sanitize drops control characters from a title.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
}

// fileURLPath extracts the path of a file:// URL (OSC 7); the host part
// is ignored, percent escapes are decoded.
func fileURLPath(u string) string {
	if !strings.HasPrefix(u, "file://") {
		return ""
	}
	u = u[len("file://"):]
	i := strings.IndexByte(u, '/')
	if i < 0 {
		return ""
	}
	p := u[i:]
	var b strings.Builder
	for j := 0; j < len(p); j++ {
		if p[j] == '%' && j+2 < len(p) {
			if v, err := strconv.ParseUint(p[j+1:j+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				j += 2
				continue
			}
		}
		b.WriteByte(p[j])
	}
	return sanitize(b.String())
}
