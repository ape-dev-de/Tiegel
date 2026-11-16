package buffer

import "bytes"

// EOL is a line ending style.
type EOL uint8

const (
	// LF is the Unix line ending "\n".
	LF EOL = iota
	// CRLF is the Windows line ending "\r\n".
	CRLF
)

// String returns the line ending sequence.
func (e EOL) String() string {
	if e == CRLF {
		return "\r\n"
	}
	return "\n"
}

// DetectEOL returns the dominant line ending of content. Files without any
// line break are reported as LF.
func DetectEOL(content []byte) EOL {
	lf := bytes.Count(content, []byte{'\n'})
	crlf := bytes.Count(content, []byte("\r\n"))
	if crlf > 0 && crlf*2 >= lf {
		return CRLF
	}
	return LF
}

// NormalizeEOL converts every line ending in text ("\r\n", "\n", or a lone
// "\r") to eol. It returns text unchanged (not copied) if nothing needs to
// be converted. Use it for pasted text so that the buffer keeps one style.
func NormalizeEOL(text []byte, eol EOL) []byte {
	if bytes.IndexByte(text, '\r') < 0 && (eol == LF || bytes.IndexByte(text, '\n') < 0) {
		return text
	}
	out := make([]byte, 0, len(text)+len(text)/16)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch c {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			out = append(out, eol.String()...)
		case '\n':
			out = append(out, eol.String()...)
		default:
			out = append(out, c)
		}
	}
	return out
}
