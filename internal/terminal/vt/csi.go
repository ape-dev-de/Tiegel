package vt

// Version is reported to programs asking for the terminal version
// (XTVERSION); the owner may set it at start-up.
var Version = "Tiegel"

func (t *Terminal) csiDispatch(final byte) {
	p := &t.p
	if len(p.inter) > 0 {
		t.csiIntermediate(final)
		return
	}
	switch p.priv {
	case '?':
		switch final {
		case 'h', 'l':
			for i := range p.params {
				t.setPrivateMode(p.rawParam(i, 0), final == 'h')
			}
		case 'J':
			t.eraseDisplay(p.rawParam(0, 0))
		case 'K':
			t.eraseLine(p.rawParam(0, 0))
		case 'n':
			if p.rawParam(0, 0) == 6 { // DECXCPR
				row, col := t.reportPos()
				t.reply("\x1b[?" + itoa(row) + ";" + itoa(col) + "R")
			}
		}
		// CSI ? u (kitty keyboard query) stays unanswered: programs
		// fall back to the legacy encoding after the DA1 reply.
		return
	case '>':
		switch final {
		case 'c': // secondary DA: VT220-ish, version 10
			t.reply("\x1b[>1;10;0c")
		case 'q': // XTVERSION
			if p.rawParam(0, 0) == 0 {
				t.reply("\x1bP>|" + Version + "\x1b\\")
			}
		case 'm': // XTMODKEYS
			if p.rawParam(0, -1) == 4 {
				t.modes.ModifyOtherKeys = max(0, min(2, p.rawParam(1, 0)))
			}
		case 'n':
			if p.rawParam(0, -1) == 4 {
				t.modes.ModifyOtherKeys = 0
			}
		}
		return
	case 0:
	default:
		return
	}
	n := p.param(0, 1)
	switch final {
	case '@':
		t.insertBlanks(n)
	case 'A':
		t.moveTo(t.row-n, t.col, true)
	case 'B', 'e':
		t.moveTo(t.row+n, t.col, true)
	case 'C', 'a':
		t.moveTo(t.row, t.col+n, true)
	case 'D':
		t.moveTo(t.row, t.col-n, true)
	case 'E':
		t.moveTo(t.row+n, 0, true)
	case 'F':
		t.moveTo(t.row-n, 0, true)
	case 'G', '`':
		t.moveTo(t.row, n-1, true)
	case 'H', 'f':
		row := p.param(0, 1) - 1
		col := p.param(1, 1) - 1
		if t.origin {
			row += t.top
		}
		t.moveTo(row, col, false)
	case 'I':
		t.tabForward(n)
	case 'J':
		t.eraseDisplay(p.rawParam(0, 0))
	case 'K':
		t.eraseLine(p.rawParam(0, 0))
	case 'L':
		t.insertLines(n)
	case 'M':
		t.deleteLines(n)
	case 'P':
		t.deleteChars(n)
	case 'S':
		t.scrollUp(t.top, t.bottom, n)
	case 'T':
		t.scrollDown(t.top, t.bottom, n)
	case 'X':
		t.eraseChars(n)
	case 'Z':
		t.tabBackward(n)
	case 'b':
		t.repeat(n)
	case 'c':
		if p.rawParam(0, 0) == 0 {
			t.reply("\x1b[?62;22c") // VT220 with ANSI color
		}
	case 'd':
		row := n - 1
		if t.origin {
			row += t.top
		}
		t.moveTo(row, t.col, false)
	case 'g':
		switch p.rawParam(0, 0) {
		case 0:
			if t.col < t.cols {
				t.tabs[t.col] = false
			}
		case 3:
			clear(t.tabs)
		}
	case 'h', 'l':
		for i := range p.params {
			switch p.rawParam(i, 0) {
			case 4:
				t.insert = final == 'h'
			case 20:
				t.newline = final == 'h'
			}
		}
	case 'm':
		t.sgr()
	case 'n':
		switch p.rawParam(0, 0) {
		case 5:
			t.reply("\x1b[0n")
		case 6:
			row, col := t.reportPos()
			t.reply("\x1b[" + itoa(row) + ";" + itoa(col) + "R")
		}
	case 'r':
		top := p.param(0, 1) - 1
		bottom := p.param(1, t.rows) - 1
		bottom = min(bottom, t.rows-1)
		if top < bottom {
			t.top, t.bottom = top, bottom
			t.moveTo(0, 0, false)
			if t.origin {
				t.row = t.top
			}
		}
	case 's':
		t.saveCursor()
	case 't':
		t.windowOp()
	case 'u':
		t.restoreCursor()
	}
}

// reportPos returns the 1-based cursor position for CPR (relative to
// the scroll region in origin mode).
func (t *Terminal) reportPos() (row, col int) {
	row = t.row + 1
	if t.origin {
		row -= t.top
	}
	return row, min(t.col, t.cols-1) + 1
}

// windowOp handles XTWINOPS: size reports and the title stack; window
// manipulation is ignored.
func (t *Terminal) windowOp() {
	p := &t.p
	switch p.rawParam(0, 0) {
	case 18: // text area size in characters
		t.reply("\x1b[8;" + itoa(t.rows) + ";" + itoa(t.cols) + "t")
	case 19: // screen size in characters
		t.reply("\x1b[9;" + itoa(t.rows) + ";" + itoa(t.cols) + "t")
	case 22: // push title
		if len(t.titles) < 10 {
			t.titles = append(t.titles, t.title)
		}
	case 23: // pop title
		if n := len(t.titles); n > 0 {
			t.title = t.titles[n-1]
			t.titles = t.titles[:n-1]
		}
	}
}

// csiIntermediate handles CSI sequences with intermediate bytes.
func (t *Terminal) csiIntermediate(final byte) {
	p := &t.p
	switch string(p.inter) {
	case " ":
		if final == 'q' { // DECSCUSR
			t.cursor.Shape = min(6, p.rawParam(0, 0))
		}
	case "!":
		if final == 'p' { // DECSTR soft reset
			t.pen = Style{}
			t.insert, t.origin, t.autowrap = false, false, true
			t.top, t.bottom = 0, t.rows-1
			t.cursor.Visible = true
			t.modes.AppCursor, t.modes.AppKeypad = false, false
			t.charsets, t.activeG1 = [2]bool{}, false
		}
	case "$":
		if final == 'p' { // DECRQM
			t.reportMode(p.priv == '?', p.rawParam(0, 0))
		}
	}
}

// Mode states of a DECRQM reply.
const (
	modeUnknown = 0
	modeSet     = 1
	modeReset   = 2
)

// reportMode answers DECRQM for an ANSI or DEC private mode.
func (t *Terminal) reportMode(private bool, mode int) {
	st := modeUnknown
	b := func(on bool) int {
		if on {
			return modeSet
		}
		return modeReset
	}
	if !private {
		switch mode {
		case 4:
			st = b(t.insert)
		case 20:
			st = b(t.newline)
		}
		t.reply("\x1b[" + itoa(mode) + ";" + itoa(st) + "$y")
		return
	}
	m := t.Modes()
	switch mode {
	case 1:
		st = b(m.AppCursor)
	case 5:
		st = b(m.ReverseVideo)
	case 6:
		st = b(t.origin)
	case 7:
		st = b(t.autowrap)
	case 12:
		st = b(t.blinkMode)
	case 25:
		st = b(t.cursor.Visible)
	case 47, 1047, 1049:
		st = b(m.AltScreen)
	case 9, 1000, 1002, 1003:
		st = b(m.Mouse == mode)
	case 1004:
		st = b(m.FocusEvents)
	case 1006:
		st = b(m.MouseSGR)
	case 1007:
		st = b(m.AltScroll)
	case 1015:
		st = b(m.MouseURXVT)
	case 2004:
		st = b(m.BracketedPaste)
	case 2026:
		st = b(m.SyncOutput)
	}
	t.reply("\x1b[?" + itoa(mode) + ";" + itoa(st) + "$y")
}

func (t *Terminal) setPrivateMode(mode int, on bool) {
	switch mode {
	case 1:
		t.modes.AppCursor = on
	case 5:
		t.modes.ReverseVideo = on
	case 6:
		t.origin = on
		t.moveTo(0, 0, false)
		if on {
			t.row = t.top
		}
	case 7:
		t.autowrap = on
		if !on {
			t.wrapNext = false
		}
	case 12:
		t.blinkMode = on
	case 25:
		t.cursor.Visible = on
	case 47, 1047:
		t.switchScreen(on, false)
	case 1048:
		if on {
			t.saveCursor()
		} else {
			t.restoreCursor()
		}
	case 1049:
		t.switchScreen(on, true)
	case 9, 1000, 1002, 1003:
		if on {
			t.modes.Mouse = mode
		} else if t.modes.Mouse == mode {
			t.modes.Mouse = 0
		}
	case 1004:
		t.modes.FocusEvents = on
	case 1006:
		t.modes.MouseSGR = on
	case 1007:
		t.modes.AltScroll = on
	case 1015:
		t.modes.MouseURXVT = on
	case 2004:
		t.modes.BracketedPaste = on
	case 2026:
		t.modes.SyncOutput = on
	}
}
