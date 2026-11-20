package vt

import (
	"unicode/utf8"
)

// Parser states (after Paul Williams' DEC parser, reduced to what the
// terminal acts on).
type pstate uint8

const (
	stGround pstate = iota
	stEscape
	stEscapeInter
	stCSI
	stCSIIgnore
	stOSC
	stOSCEsc  // ESC inside an OSC string (ST is ESC \)
	stString  // DCS, SOS, PM, APC: consumed until ST
	stStrEsc  // ESC inside such a string
	stCharset // ESC ( X and friends: one designator byte follows
)

const (
	maxParams = 32
	// maxOSC bounds an OSC string (OSC 52 carries base64 clipboard
	// text).
	maxOSC = 1 << 20
)

// parser holds the state between Write calls.
type parser struct {
	state pstate
	// utf8 collects the bytes of an incomplete UTF-8 sequence.
	utf8 []byte
	// CSI: private marker ('?', '>', '<', '='), parameter groups
	// (';' separated, ':' sub-parameters; -1 = omitted) and intermediates.
	priv   byte
	params [][]int
	cur    []int
	inter  []byte
	// charset designation: which G set (0 or 1).
	gset int
	osc  []byte
	// oscBEL: the OSC string ended with BEL (replies use the same
	// terminator).
	oscBEL bool
}

// pushParam ends the current parameter group, reusing the backing
// arrays of earlier sequences (no allocation per sequence).
func (p *parser) pushParam() {
	if len(p.cur) == 0 {
		p.cur = append(p.cur, -1)
	}
	if len(p.params) < maxParams {
		n := len(p.params)
		if n < cap(p.params) {
			p.params = p.params[:n+1]
			p.params[n] = append(p.params[n][:0], p.cur...)
		} else {
			p.params = append(p.params, append([]int(nil), p.cur...))
		}
	}
	p.cur = p.cur[:0]
}

func (p *parser) resetCSI() {
	p.priv = 0
	p.params = p.params[:0]
	p.cur = p.cur[:0]
	p.inter = p.inter[:0]
}

// Write feeds program output into the terminal. It never fails; the
// return values satisfy io.Writer.
func (t *Terminal) Write(b []byte) (int, error) {
	t.seq++
	p := &t.p
	n := len(b)
	if len(p.utf8) > 0 {
		// Complete a sequence split across writes.
		for len(b) > 0 && len(p.utf8) < utf8.UTFMax && !utf8.FullRune(p.utf8) {
			if b[0] < 0x80 || b[0] >= 0xc0 {
				break
			}
			p.utf8 = append(p.utf8, b[0])
			b = b[1:]
		}
		if utf8.FullRune(p.utf8) || len(b) > 0 {
			r, _ := utf8.DecodeRune(p.utf8)
			p.utf8 = p.utf8[:0]
			t.printRune(r)
		} else {
			return n, nil
		}
	}
	for len(b) > 0 {
		c := b[0]
		if p.state == stGround && c >= 0x20 && c != 0x7f {
			if c < 0x80 {
				// Fast path for runs of ASCII.
				i := 1
				for i < len(b) && b[i] >= 0x20 && b[i] < 0x7f {
					i++
				}
				t.printASCII(b[:i])
				b = b[i:]
				continue
			}
			if !utf8.FullRune(b) {
				p.utf8 = append(p.utf8[:0], b...)
				return n, nil
			}
			r, size := utf8.DecodeRune(b)
			t.printRune(r)
			b = b[size:]
			continue
		}
		t.step(c)
		b = b[1:]
	}
	return n, nil
}

// step processes one byte outside the ground-state printing path.
func (t *Terminal) step(c byte) {
	p := &t.p
	// Controls valid in every state (except strings, which end on them).
	switch p.state {
	case stOSC, stOSCEsc, stString, stStrEsc:
		t.stringByte(c)
		return
	}
	switch c {
	case 0x18, 0x1a: // CAN, SUB abort a sequence
		p.state = stGround
		return
	case 0x1b:
		p.state = stEscape
		p.inter = p.inter[:0]
		return
	}
	if c < 0x20 {
		t.control(c)
		return
	}
	switch p.state {
	case stGround:
		// 0x7f DEL is ignored.
	case stEscape:
		t.escape(c)
	case stEscapeInter:
		if c >= 0x20 && c <= 0x2f {
			p.inter = append(p.inter, c)
			return
		}
		t.escDispatch(c)
		p.state = stGround
	case stCharset:
		t.designate(p.gset, c)
		p.state = stGround
	case stCSI:
		t.csiByte(c)
	case stCSIIgnore:
		if c >= 0x40 && c <= 0x7e {
			p.state = stGround
		}
	}
}

func (t *Terminal) escape(c byte) {
	p := &t.p
	switch c {
	case '[':
		p.resetCSI()
		p.state = stCSI
	case ']':
		p.osc = p.osc[:0]
		p.state = stOSC
	case 'P', 'X', '^', '_': // DCS, SOS, PM, APC
		p.state = stString
	case '(', ')', '*', '+':
		p.gset = 0
		if c == ')' {
			p.gset = 1
		}
		if c == '*' || c == '+' {
			p.gset = 2 // G2/G3 are accepted and ignored
		}
		p.state = stCharset
	default:
		if c >= 0x20 && c <= 0x2f {
			p.inter = append(p.inter[:0], c)
			p.state = stEscapeInter
			return
		}
		t.escDispatch(c)
		p.state = stGround
	}
}

func (t *Terminal) csiByte(c byte) {
	p := &t.p
	switch {
	case c >= '0' && c <= '9':
		if len(p.inter) > 0 {
			p.state = stCSIIgnore
			return
		}
		if len(p.cur) == 0 {
			p.cur = append(p.cur, -1)
		}
		v := &p.cur[len(p.cur)-1]
		if *v < 0 {
			*v = 0
		}
		if *v < 1<<20 {
			*v = *v*10 + int(c-'0')
		}
	case c == ';':
		p.pushParam()
	case c == ':':
		if len(p.cur) == 0 {
			p.cur = append(p.cur, -1)
		}
		if len(p.cur) < 8 {
			p.cur = append(p.cur, -1)
		}
	case c >= '<' && c <= '?':
		if len(p.params) == 0 && len(p.cur) == 0 && p.priv == 0 {
			p.priv = c
			return
		}
		p.state = stCSIIgnore
	case c >= 0x20 && c <= 0x2f:
		p.inter = append(p.inter, c)
	case c >= 0x40 && c <= 0x7e:
		if len(p.cur) > 0 || len(p.params) > 0 {
			p.pushParam()
		}
		p.state = stGround
		t.csiDispatch(c)
	default:
		p.state = stCSIIgnore
	}
}

// stringByte handles bytes inside OSC and other strings.
func (t *Terminal) stringByte(c byte) {
	p := &t.p
	switch p.state {
	case stOSC:
		switch c {
		case 0x07:
			p.oscBEL = true
			t.oscDispatch()
			p.state = stGround
		case 0x1b:
			p.state = stOSCEsc
		case 0x18, 0x1a:
			p.state = stGround
		default:
			if len(p.osc) < maxOSC {
				p.osc = append(p.osc, c)
			}
		}
	case stOSCEsc:
		p.oscBEL = false
		if c == '\\' {
			t.oscDispatch()
			p.state = stGround
			return
		}
		// Not ST: the string ends and ESC starts a new sequence.
		t.oscDispatch()
		p.state = stEscape
		t.step(c)
	case stString:
		switch c {
		case 0x1b:
			p.state = stStrEsc
		case 0x07, 0x18, 0x1a:
			p.state = stGround
		}
	case stStrEsc:
		if c == '\\' {
			p.state = stGround
			return
		}
		p.state = stEscape
		t.step(c)
	}
}

// param returns parameter i (first sub-parameter) or def when omitted
// or zero (most CSI parameters treat 0 like the default).
func (p *parser) param(i, def int) int {
	if i >= len(p.params) || len(p.params[i]) == 0 || p.params[i][0] <= 0 {
		return def
	}
	return p.params[i][0]
}

// rawParam returns parameter i or def when omitted (0 stays 0).
func (p *parser) rawParam(i, def int) int {
	if i >= len(p.params) || len(p.params[i]) == 0 || p.params[i][0] < 0 {
		return def
	}
	return p.params[i][0]
}
