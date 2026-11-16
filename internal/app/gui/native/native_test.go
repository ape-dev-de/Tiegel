package native

import (
	"encoding/binary"
	"testing"
)

func TestParseChord(t *testing.T) {
	tests := []struct {
		in   string
		key  string
		mods uint32
		ok   bool
	}{
		{"Cmd+Shift+O", "o", modCmd | modShift, true},
		{"Cmd+,", ",", modCmd, true},
		{"Ctrl+Tab", "\t", modCtrl, true},
		{"Cmd+Alt+B", "b", modCmd | modAlt, true},
		{"cmd+option+b", "b", modCmd | modAlt, true},
		{"F9", "", 0, true},
		{"F1", "", 0, true},
		{"F20", "", 0, true},
		{"Shift+F8", "", modShift, true},
		{"Cmd+F2", "", modCmd, true},
		{"Cmd+=", "=", modCmd, true},
		{"Cmd+-", "-", modCmd, true},
		{"Cmd++", "+", modCmd, true},
		{"Cmd+[", "[", modCmd, true},
		{"Cmd+]", "]", modCmd, true},
		{"Cmd+/", "/", modCmd, true},
		{"Cmd+1", "1", modCmd, true},
		{"Shift+Escape", "\x1b", modShift, true},
		{"Esc", "\x1b", 0, true},
		{"Cmd+Shift+F12", "", modCmd | modShift, true},
		{"Ctrl+Space", " ", modCtrl, true},
		{"Cmd+Return", "\r", modCmd, true},
		{"Enter", "\r", 0, true},
		{"Alt+Left", "", modAlt, true},
		{"Alt+Right", "", modAlt, true},
		{"Up", "", 0, true},
		{"Down", "", 0, true},
		{"Backspace", "\b", 0, true},
		{"Cmd+Delete", "", modCmd, true},
		{"Home", "", 0, true},
		{"End", "", 0, true},
		{"PageUp", "", 0, true},
		{"PageDown", "", 0, true},
		{"Meta+Control+X", "x", modCmd | modCtrl, true},
		{"", "", 0, false},
		{"   ", "", 0, false},
		{"F0", "", 0, false},
		{"F21", "", 0, false},
		{"Fx", "", 0, false},
		{"Hyper+A", "", 0, false},
		{"Cmd+K Cmd+S", "", 0, false},
		{"Cmd+Foo", "", 0, false},
		{"Cmd+ä", "", 0, false},
	}
	for _, tt := range tests {
		key, mods, ok := parseChord(tt.in)
		if key != tt.key || mods != tt.mods || ok != tt.ok {
			t.Errorf("parseChord(%q) = %q, %#x, %v; want %q, %#x, %v",
				tt.in, key, mods, ok, tt.key, tt.mods, tt.ok)
		}
	}
}

// decoded is a parsed menu buffer record, for tests.
type decoded struct {
	op          byte
	kind, flags byte
	tag         int64
	mods        uint32
	title, key  string
}

func decode(t *testing.T, b []byte) []decoded {
	t.Helper()
	str := func() string {
		n := binary.LittleEndian.Uint32(b)
		s := string(b[4 : 4+n])
		b = b[4+n:]
		return s
	}
	var out []decoded
	for len(b) > 0 {
		d := decoded{op: b[0]}
		b = b[1:]
		switch d.op {
		case 'M':
			d.flags = b[0]
			b = b[1:]
			d.title = str()
		case 'I':
			d.kind, d.flags = b[0], b[1]
			d.tag = int64(binary.LittleEndian.Uint64(b[2:]))
			d.mods = binary.LittleEndian.Uint32(b[10:])
			b = b[14:]
			d.title = str()
			d.key = str()
		case 'S', 'E':
		default:
			t.Fatalf("bad opcode %q", d.op)
		}
		out = append(out, d)
	}
	return out
}

func TestEncodeMenus(t *testing.T) {
	menus := []Menu{
		{Title: "File", Items: []MenuItem{
			{ID: "file.open", Title: "Open…", Shortcut: "Cmd+O"},
			{Separator: true, ID: "ignored"},
			{ID: "file.recent", Title: "Recent", Children: []MenuItem{
				{ID: "recent.1", Title: "a", Checked: true, Disabled: true},
			}},
		}},
		{Title: "Window"},
	}
	buf, ids := encodeMenus(menus, 7)
	recs := decode(t, buf)

	if recs[0].op != 'M' || recs[0].flags != menuApp {
		t.Fatalf("first record = %+v, want app menu", recs[0])
	}
	var quit, open, recent, child, window *decoded
	for i := range recs {
		r := &recs[i]
		switch {
		case r.title == "Quit Tiegel":
			quit = r
		case r.title == "Open…":
			open = r
		case r.title == "Recent":
			recent = r
		case r.title == "a":
			child = r
		case r.op == 'M' && r.title == "Window":
			window = r
		}
	}
	if quit == nil || quit.flags&itemFunctional == 0 || quit.key != "q" || quit.mods != modCmd ||
		ids[quit.tag&0xffffffff] != "app.quit" || quit.tag>>32 != 7 {
		t.Errorf("quit = %+v", quit)
	}
	if open == nil || open.flags != 0 || open.key != "o" || ids[open.tag&0xffffffff] != "file.open" {
		t.Errorf("open = %+v", open)
	}
	if recent == nil || recent.flags != itemSubmenu {
		t.Errorf("recent = %+v", recent)
	}
	if child == nil || child.flags != itemChecked|itemDisabled || ids[child.tag&0xffffffff] != "recent.1" {
		t.Errorf("child = %+v", child)
	}
	if window == nil || window.flags != menuWindows {
		t.Errorf("window = %+v", window)
	}
	// Balanced begin/end.
	depth := 0
	for _, r := range recs {
		if r.op == 'M' || r.op == 'I' && r.flags&itemSubmenu != 0 {
			depth++
		}
		if r.op == 'E' {
			depth--
		}
	}
	if depth != 0 {
		t.Errorf("unbalanced menu records: depth %d", depth)
	}
}
