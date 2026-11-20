package vt

import (
	"encoding/base64"
	"strings"
	"testing"
)

// replies returns a terminal that records its replies.
func replies(cols, rows int) (*Terminal, *strings.Builder) {
	t := New(cols, rows, 100)
	var b strings.Builder
	t.Reply = func(p []byte) { b.Write(p) }
	return t, &b
}

func TestReports(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"da1", "\x1b[c", "\x1b[?62;22c"},
		{"da1 zero", "\x1b[0c", "\x1b[?62;22c"},
		{"da1 other", "\x1b[1c", ""},
		{"da2", "\x1b[>c", "\x1b[>1;10;0c"},
		{"xtversion", "\x1b[>q", "\x1bP>|Tiegel\x1b\\"},
		{"xtversion 1", "\x1b[>1q", ""},
		{"dsr", "\x1b[5n", "\x1b[0n"},
		{"cpr", "ab\x1b[6n", "\x1b[1;3R"},
		{"decxcpr", "\x1b[2;4H\x1b[?6n", "\x1b[?2;4R"},
		{"cpr origin", "\x1b[3;5r\x1b[?6h\x1b[2;1H\x1b[6n", "\x1b[2;1R"},
		{"size chars", "\x1b[18t", "\x1b[8;5;20t"},
		{"screen chars", "\x1b[19t", "\x1b[9;5;20t"},
		{"kitty query unanswered", "\x1b[?u", ""},
		{"decrqm sync reset", "\x1b[?2026$p", "\x1b[?2026;2$y"},
		{"decrqm sync set", "\x1b[?2026h\x1b[?2026$p", "\x1b[?2026;1$y"},
		{"decrqm paste", "\x1b[?2004h\x1b[?2004$p", "\x1b[?2004;1$y"},
		{"decrqm unknown", "\x1b[?2027$p", "\x1b[?2027;0$y"},
		{"decrqm alt", "\x1b[?1049h\x1b[?1049$p", "\x1b[?1049;1$y"},
		{"decrqm mouse", "\x1b[?1002h\x1b[?1002$p\x1b[?1000$p", "\x1b[?1002;1$y\x1b[?1000;2$y"},
		{"decrqm ansi insert", "\x1b[4h\x1b[4$p\x1b[20$p\x1b[3$p", "\x1b[4;1$y\x1b[20;2$y\x1b[3;0$y"},
		{"decrqm cursor", "\x1b[?25l\x1b[?25$p\x1b[?12h\x1b[?12$p\x1b[?7$p\x1b[?6$p\x1b[?1$p\x1b[?5$p", "\x1b[?25;2$y\x1b[?12;1$y\x1b[?7;1$y\x1b[?6;2$y\x1b[?1;2$y\x1b[?5;2$y"},
		{"decrqm misc", "\x1b[?1004$p\x1b[?1006$p\x1b[?1007$p\x1b[?1015$p", "\x1b[?1004;2$y\x1b[?1006;2$y\x1b[?1007;1$y\x1b[?1015;2$y"},
		{"osc 11 st", "\x1b]11;?\x1b\\", "\x1b]11;rgb:1e1e/1f1f/2222\x1b\\"},
		{"osc 10 bel", "\x1b]10;?\x07", "\x1b]10;rgb:d0d0/d0d0/d0d0\x07"},
		{"osc 10 chained", "\x1b]10;?;?\x07", "\x1b]10;rgb:d0d0/d0d0/d0d0\x07\x1b]11;rgb:1e1e/1f1f/2222\x07"},
		{"osc 12", "\x1b]12;?\x07", "\x1b]12;rgb:d0d0/d0d0/d0d0\x07"},
		{"osc 12 chained too far", "\x1b]12;?;?\x07", "\x1b]12;rgb:d0d0/d0d0/d0d0\x07"},
		{"osc 11 set ignored", "\x1b]11;#ffffff\x07", ""},
		{"osc 4", "\x1b]4;1;?;196;?;bad;?;300;?;2;x\x07", "\x1b]4;1;rgb:cdcd/0000/0000\x07\x1b]4;196;rgb:ffff/0000/0000\x07"},
		{"osc 4 gray", "\x1b]4;232;?\x1b\\", "\x1b]4;232;rgb:0808/0808/0808\x1b\\"},
	}
	for _, c := range cases {
		term, got := replies(20, 5)
		feed(term, c.in)
		if got.String() != c.want {
			t.Errorf("%s: reply %q want %q", c.name, got.String(), c.want)
		}
	}
	// No Reply callback: nothing breaks.
	feed(New(5, 2, 0), "\x1b[c\x1b]11;?\x07")
}

func TestTitleStackAndMarks(t *testing.T) {
	term := New(20, 5, 100)
	feed(term, "\x1b]2;one\x07\x1b[22;0t\x1b]2;two\x07")
	if term.Title() != "two" {
		t.Fatal(term.Title())
	}
	feed(term, "\x1b[23;0t")
	if term.Title() != "one" {
		t.Fatal("pop", term.Title())
	}
	feed(term, "\x1b[23t\x1b[1t") // empty stack, unknown op
	for range 12 {
		feed(term, "\x1b[22t")
	}
	if len(term.titles) != 10 {
		t.Fatal("stack bound", len(term.titles))
	}
	feed(term, "\x1b]1;icon\x07\x1b]104\x07")
	if term.Title() != "one" {
		t.Fatal("icon changed title")
	}
	// Shell integration marks and the exit code.
	feed(term, "\x1b]133;A\x07$ \x1b]133;B\x07ls\r\n\x1b]133;C\x07out\r\n\x1b]133;D;2\x07\x1b]133;A\x07")
	if m := term.Line(0).Mark; m != MarkPrompt|MarkInput {
		t.Fatalf("mark 0 %v", m)
	}
	if term.Line(1).Mark != MarkOutput || term.Line(2).Mark != MarkDone|MarkPrompt || term.LastExit() != 2 {
		t.Fatalf("marks %v %v exit %d", term.Line(1).Mark, term.Line(2).Mark, term.LastExit())
	}
	feed(term, "\x1b]133;D\x07\x1b]133;X\x07")
	if term.LastExit() != -1 {
		t.Fatal("exit without code")
	}
	// Marks travel into the scrollback; the alternate screen has none.
	feed(term, "\r\n\r\n\r\n\r\n\r\n")
	if term.History() == 0 || term.Line(0).Mark != MarkPrompt|MarkInput {
		t.Fatalf("scrollback mark %v", term.Line(0).Mark)
	}
	feed(term, "\x1b[?1049h\x1b]133;A\x07")
	if term.Line(term.History()).Mark != 0 {
		t.Fatal("mark on alt screen")
	}
}

func TestHyperlinks(t *testing.T) {
	term := New(30, 3, 0)
	feed(term, "a\x1b]8;;https://x.test/1\x1b\\bc\x1b[1m\x1b[0md\x1b]8;;\x1b\\e")
	l := term.Line(0)
	id := l.Cells[1].Style.Link
	if l.Cells[0].Style.Link != 0 || id == 0 || l.Cells[3].Style.Link != id || l.Cells[4].Style.Link != 0 {
		t.Fatalf("links %v", []uint16{l.Cells[0].Style.Link, id, l.Cells[3].Style.Link, l.Cells[4].Style.Link})
	}
	if term.LinkURL(id) != "https://x.test/1" || term.LinkURL(0) != "" || term.LinkURL(999) != "" {
		t.Fatal("url")
	}
	// Same id and URL share an entry; another id does not.
	feed(term, "\x1b]8;id=a;https://x.test/1\x07f\x1b]8;id=a;https://x.test/1\x07g\x1b]8;id=b:k=v;https://x.test/1\x07h\x1b]8;;\x07")
	l = term.Line(0)
	if l.Cells[5].Style.Link != l.Cells[6].Style.Link || l.Cells[6].Style.Link == l.Cells[7].Style.Link || l.Cells[5].Style.Link == id {
		t.Fatal("link ids")
	}
	spans := term.Spans(0)
	if len(spans) < 4 || spans[1].Style.Link != id {
		t.Fatalf("spans split by link %+v", spans)
	}
	// Overlong targets and a malformed sequence end the link.
	feed(term, "\x1b]8;;"+strings.Repeat("x", maxLinkURL+1)+"\x07i\x1b]8\x07j")
	l = term.Line(0)
	if l.Cells[8].Style.Link != 0 || l.Cells[9].Style.Link != 0 {
		t.Fatal("bad links")
	}
	// The table is bounded.
	term.links = make([]string, maxLinks)
	feed(term, "\x1b]8;;https://new\x07k")
	if term.Line(0).Cells[10].Style.Link != 0 {
		t.Fatal("full table")
	}
}

func TestClipboard(t *testing.T) {
	term := New(10, 2, 0)
	enc := base64.StdEncoding.EncodeToString([]byte("copied ä"))
	feed(term, "\x1b]52;c;"+enc+"\x07") // no callback: ignored
	var got []string
	term.Clipboard = func(target, text string) { got = append(got, target+"="+text) }
	feed(term, "\x1b]52;c;"+enc+"\x07\x1b]52;;"+strings.TrimRight(enc, "=")+"\x1b\\\x1b]52;c;?\x07\x1b]52;c;!!\x07\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte{0xff})+"\x07\x1b]52\x07")
	if strings.Join(got, "|") != "c=copied ä|s0=copied ä" {
		t.Fatalf("clipboard %q", got)
	}
	// Large payloads fit; the buffer is released afterwards.
	big := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 200<<10)))
	feed(term, "\x1b]52;c;"+big+"\x07")
	if len(got) != 3 || len(got[2]) != 2+200<<10 || term.p.osc != nil {
		t.Fatal("big clipboard")
	}
}

func TestOSCPassThrough(t *testing.T) {
	term := New(10, 2, 0)
	var got []string
	term.OSC = func(num, text string) { got = append(got, num+":"+text) }
	feed(term, "\x1b]7716;tiegel-pid=42\x07\x1b]9;note\x1b\\")
	if strings.Join(got, "|") != "7716:tiegel-pid=42|9:note" {
		t.Fatal(got)
	}
}

func TestUnderlineStylesAndColors(t *testing.T) {
	term := New(30, 2, 0)
	feed(term, "\x1b[4ma\x1b[4:2mb\x1b[4:3mc\x1b[4:4md\x1b[4:5me\x1b[4:9mf\x1b[21mg\x1b[24mh\x1b[4:1;58;5;9mi\x1b[58:2::1:2:3mj\x1b[59mk\x1b[53ml\x1b[55mm\x1b[4:0mn")
	l := term.Line(0)
	want := []UnderlineStyle{ULSingle, ULDouble, ULCurly, ULDotted, ULDashed, ULSingle, ULDouble, ULNone, ULSingle, ULSingle, ULSingle, ULSingle, ULSingle, ULNone}
	for i, w := range want {
		if got := l.Cells[i].Style.Attr.UnderlineStyle(); got != w {
			t.Errorf("cell %d underline %d want %d", i, got, w)
		}
	}
	if l.Cells[8].Style.UL != Indexed(9) || l.Cells[9].Style.UL != RGB(1, 2, 3) || l.Cells[10].Style.UL != Default {
		t.Error("underline colors")
	}
	if l.Cells[11].Style.Attr&Overline == 0 || l.Cells[12].Style.Attr&Overline != 0 {
		t.Error("overline")
	}
	// Truncated extended colors do not crash and set nothing.
	feed(term, "\x1b[38;5mo\x1b[48;2;1;2mp\x1b[38:5mq\x1b[38;9mr")
	if c := term.Line(0).Cells[14]; c.Style.FG != Default {
		t.Errorf("truncated 256 color %+v", c.Style)
	}
}

func TestCursorShapeAndBlink(t *testing.T) {
	term := New(5, 2, 0)
	if c := term.Cursor(); c.Blink || c.Shape != 0 {
		t.Fatal("default cursor")
	}
	feed(term, "\x1b[?12h")
	if !term.Cursor().Blink {
		t.Fatal("mode 12")
	}
	for shape, blink := range map[int]bool{1: true, 2: false, 3: true, 4: false, 5: true, 6: false} {
		feed(term, "\x1b["+itoa(shape)+" q")
		if c := term.Cursor(); c.Shape != shape || c.Blink != blink {
			t.Errorf("shape %d: %+v", shape, c)
		}
	}
	feed(term, "\x1b[9 q")
	if term.Cursor().Shape != 6 {
		t.Error("shape clamp")
	}
}

func TestModesSet(t *testing.T) {
	term := New(5, 2, 0)
	feed(term, "\x1b[?9h")
	if term.Modes().Mouse != MouseModeX10 {
		t.Fatal("x10")
	}
	feed(term, "\x1b[?1003h\x1b[?9l") // turning off another mode keeps 1003
	if term.Modes().Mouse != MouseModeAny {
		t.Fatal("any")
	}
	feed(term, "\x1b[?1015h\x1b[?1007l\x1b[?2026h\x1b[>4;2m")
	m := term.Modes()
	if !m.MouseURXVT || m.AltScroll || !m.SyncOutput || m.ModifyOtherKeys != 2 {
		t.Fatalf("modes %+v", m)
	}
	feed(term, "\x1b[>4;9m")
	if term.Modes().ModifyOtherKeys != 2 {
		t.Fatal("clamp")
	}
	feed(term, "\x1b[>4n\x1b[>1;2m")
	if term.Modes().ModifyOtherKeys != 0 {
		t.Fatal("reset")
	}
	feed(term, "\x1b[>4;1m\x1b[>5n")
	if term.Modes().ModifyOtherKeys != 1 {
		t.Fatal("other resource")
	}
	feed(term, "\x1bc")
	if m := term.Modes(); m.Mouse != 0 || !m.AltScroll || m.SyncOutput {
		t.Fatalf("RIS %+v", m)
	}
}

func TestEncodeMouse(t *testing.T) {
	sgr := Modes{Mouse: MouseModeNormal, MouseSGR: true}
	btn := Modes{Mouse: MouseModeButton, MouseSGR: true}
	anyM := Modes{Mouse: MouseModeAny, MouseSGR: true}
	x10 := Modes{Mouse: MouseModeX10}
	normal := Modes{Mouse: MouseModeNormal}
	urxvt := Modes{Mouse: MouseModeNormal, MouseURXVT: true}
	cases := []struct {
		name string
		e    Mouse
		m    Modes
		want string
	}{
		{"off", Mouse{Action: MousePress, Button: MouseLeft}, Modes{}, ""},
		{"sgr press", Mouse{Action: MousePress, Button: MouseLeft, Row: 2, Col: 4}, sgr, "\x1b[<0;5;3M"},
		{"sgr release", Mouse{Action: MouseRelease, Button: MouseRight, Row: 0, Col: 0}, sgr, "\x1b[<2;1;1m"},
		{"sgr mods", Mouse{Action: MousePress, Button: MouseMiddle, Shift: true, Alt: true, Ctrl: true}, sgr, "\x1b[<29;1;1M"},
		{"sgr wheel", Mouse{Action: MousePress, Button: MouseWheelDown, Row: 9, Col: 9}, sgr, "\x1b[<65;10;10M"},
		{"wheel left", Mouse{Action: MousePress, Button: MouseWheelLeft}, sgr, "\x1b[<66;1;1M"},
		{"wheel right", Mouse{Action: MousePress, Button: MouseWheelRight}, sgr, "\x1b[<67;1;1M"},
		{"wheel up", Mouse{Action: MousePress, Button: MouseWheelUp}, sgr, "\x1b[<64;1;1M"},
		{"wheel release dropped", Mouse{Action: MouseRelease, Button: MouseWheelUp}, sgr, ""},
		{"normal ignores motion", Mouse{Action: MouseMove, Button: MouseLeft}, sgr, ""},
		{"button drag", Mouse{Action: MouseMove, Button: MouseLeft, Row: 1, Col: 1}, btn, "\x1b[<32;2;2M"},
		{"button hover dropped", Mouse{Action: MouseMove}, btn, ""},
		{"any hover", Mouse{Action: MouseMove, Row: 1, Col: 1}, anyM, "\x1b[<35;2;2M"},
		{"press without button", Mouse{Action: MousePress}, anyM, ""},
		{"release without button", Mouse{Action: MouseRelease}, anyM, ""},
		{"wheel motion", Mouse{Action: MouseMove, Button: MouseWheelUp}, anyM, ""},
		{"x10 press", Mouse{Action: MousePress, Button: MouseLeft, Row: 0, Col: 0, Ctrl: true}, x10, "\x1b[M !!"},
		{"x10 release dropped", Mouse{Action: MouseRelease, Button: MouseLeft}, x10, ""},
		{"normal release", Mouse{Action: MouseRelease, Button: MouseLeft, Row: 1, Col: 2}, normal, "\x1b[M##\""},
		{"normal far", Mouse{Action: MousePress, Button: MouseLeft, Row: 0, Col: 300}, normal, ""},
		{"urxvt", Mouse{Action: MousePress, Button: MouseLeft, Row: 0, Col: 300}, urxvt, "\x1b[32;301;1M"},
		{"unknown button", Mouse{Action: MousePress, Button: "x"}, sgr, ""},
		{"unknown action", Mouse{Action: "hover", Button: MouseLeft}, sgr, ""},
		{"negative", Mouse{Action: MousePress, Button: MouseLeft, Row: -1}, sgr, ""},
	}
	for _, c := range cases {
		if got := string(EncodeMouse(c.e, c.m)); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
	if string(EncodeFocus(true, Modes{FocusEvents: true})) != "\x1b[I" || string(EncodeFocus(false, Modes{FocusEvents: true})) != "\x1b[O" || EncodeFocus(true, Modes{}) != nil {
		t.Error("focus")
	}
}

func TestEncodeKeyExtras(t *testing.T) {
	mok1 := Modes{ModifyOtherKeys: 1}
	mok2 := Modes{ModifyOtherKeys: 2}
	cases := []struct {
		k    Key
		m    Modes
		want string
	}{
		{Key{Name: "Enter", Shift: true}, Modes{}, "\x1b\r"},
		{Key{Name: "Enter", Ctrl: true}, Modes{}, "\r"},
		{Key{Name: "/", Ctrl: true}, Modes{}, "\x1f"},
		{Key{Name: "Enter", Shift: true}, mok1, "\x1b[27;2;13~"},
		{Key{Name: "Enter", Ctrl: true}, mok1, "\x1b[27;5;13~"},
		{Key{Name: "Tab", Ctrl: true}, mok1, "\x1b[27;5;9~"},
		{Key{Name: "Tab", Shift: true}, mok1, "\x1b[Z"},
		{Key{Name: "Backspace", Ctrl: true}, mok1, "\x1b[27;5;127~"},
		{Key{Name: "Backspace", Alt: true}, mok1, "\x1b\x7f"},
		{Key{Name: "Escape", Shift: true}, mok1, "\x1b[27;2;27~"},
		{Key{Name: "Space", Ctrl: true}, mok1, "\x00"},
		{Key{Name: "Space", Ctrl: true}, mok2, "\x1b[27;5;32~"},
		{Key{Name: "Space", Ctrl: true, Shift: true}, mok1, "\x1b[27;6;32~"},
		{Key{Name: "Space", Shift: true}, mok1, " "},
		{Key{Name: "a", Ctrl: true}, mok1, "\x01"},
		{Key{Name: "a", Ctrl: true}, mok2, "\x1b[27;5;97~"},
		{Key{Name: "a", Ctrl: true, Shift: true}, mok1, "\x1b[27;6;65~"},
		{Key{Name: "1", Ctrl: true}, mok1, "\x1b[27;5;49~"},
		{Key{Name: "a", Alt: true}, mok1, "\x1ba"},
		{Key{Name: "Up", Ctrl: true}, mok1, "\x1b[1;5A"},
		{Key{Name: "ab", Ctrl: true}, mok1, ""},
		{Key{Name: "a"}, mok2, "a"},
	}
	for _, c := range cases {
		if got := string(EncodeKey(c.k, c.m)); got != c.want {
			t.Errorf("%+v %+v: %q want %q", c.k, c.m, got, c.want)
		}
	}
}

func TestSyncAndDecstr(t *testing.T) {
	term := New(5, 3, 0)
	feed(term, "\x1b[?2026h")
	if !term.Modes().SyncOutput {
		t.Fatal("sync")
	}
	feed(term, "\x1b[?2026l\x1b[2;3r\x1b[?25l\x1b[4h\x1b[!p")
	if term.top != 0 || term.bottom != 2 || !term.Cursor().Visible || term.insert {
		t.Fatal("DECSTR")
	}
}

// TestPrintASCIIWide: the fast ASCII path keeps wide characters
// consistent when it overwrites them.
func TestPrintASCIIWide(t *testing.T) {
	term := New(6, 2, 0)
	feed(term, "世界ab\x1b[1;2Hxyz")
	if got := term.LineText(0); got != " xyzab" {
		t.Fatalf("%q", got)
	}
	for i, c := range term.Line(0).Cells {
		if c.Width == 0 || c.Width == 2 {
			t.Fatalf("cell %d still wide: %+v", i, c)
		}
	}
	// Exactly filling the line leaves a pending wrap.
	term = New(3, 2, 0)
	feed(term, "abc")
	if !term.wrapNext || term.col != 2 {
		t.Fatal("pending wrap")
	}
	feed(term, "d")
	if term.ScreenText() != "abc\nd" || !term.Line(0).Wrapped {
		t.Fatalf("%q", term.ScreenText())
	}
	// Charsets and insert mode go through the careful path.
	feed(term, "\x1b(0q\x1b(B\x1b[4h\rX")
	if term.LineText(1) != "Xd─" {
		t.Fatalf("%q", term.LineText(1))
	}
}

func TestRotate(t *testing.T) {
	mk := func(n int) []Line {
		ls := make([]Line, n)
		for i := range ls {
			ls[i].Mark = Mark(i)
		}
		return ls
	}
	for n := 0; n <= 12; n++ {
		ls := mk(12)
		rotate(ls, n)
		for i := range ls {
			if want := Mark((i + n) % 12); n > 0 && n < 12 && ls[i].Mark != want {
				t.Fatalf("rotate %d: %d has %d", n, i, ls[i].Mark)
			}
		}
	}
}

func TestReflow(t *testing.T) {
	term := New(10, 3, 100)
	feed(term, "abcdefghijklmno\r\nxy")
	// Narrower: rewrapped, cursor stays after "xy".
	term.Resize(5, 3)
	if got := term.Text(0, 0, term.Total()-1, 5); got != "abcdefghijklmno\nxy" {
		t.Fatalf("text %q", got)
	}
	if term.ScreenText() != "fghij\nklmno\nxy" || term.History() != 1 {
		t.Fatalf("screen %q history %d", term.ScreenText(), term.History())
	}
	if c := term.Cursor(); c.Row != 2 || c.Col != 2 {
		t.Fatalf("cursor %+v", c)
	}
	feed(term, "z")
	if term.LineText(term.History()+2) != "xyz" {
		t.Fatal("typing after reflow")
	}
	// Wider again: joined back.
	term.Resize(20, 3)
	if term.ScreenText() != "abcdefghijklmno\nxyz" || term.History() != 0 {
		t.Fatalf("wide %q %d", term.ScreenText(), term.History())
	}
	if c := term.Cursor(); c.Row != 1 || c.Col != 3 {
		t.Fatalf("cursor %+v", c)
	}
	// The cursor inside a wrapped line.
	term = New(6, 4, 100)
	feed(term, "abcdefghij\x1b[2;3H")
	term.Resize(3, 4)
	if c := term.Cursor(); term.LineText(term.History() + c.Row)[c.Col:c.Col+1] != "i" {
		t.Fatalf("cursor %+v on %q", c, term.ScreenText())
	}
	// Wide characters do not split and the filler blank disappears.
	term = New(5, 3, 100)
	feed(term, "abcd世e")
	if term.ScreenText() != "abcd\n世e" {
		t.Fatalf("before %q", term.ScreenText())
	}
	term.Resize(8, 3)
	if term.ScreenText() != "abcd世e" {
		t.Fatalf("joined %q", term.ScreenText())
	}
	term.Resize(3, 3)
	if term.ScreenText() != "abc\nd世\ne" {
		t.Fatalf("narrow %q", term.ScreenText())
	}
	// Without scrollback lines above the screen are dropped.
	term = New(4, 2, 0)
	feed(term, "abcdefgh")
	term.Resize(2, 2)
	if term.ScreenText() != "ef\ngh" || term.Dropped() != 2 {
		t.Fatalf("no scrollback %q %d", term.ScreenText(), term.Dropped())
	}
	// A bounded scrollback keeps the newest lines.
	term = New(4, 1, 2)
	feed(term, "abcdefghijkl")
	term.Resize(2, 1)
	if term.History() != 2 || term.LineText(0) != "gh" {
		t.Fatalf("bounded %d %q", term.History(), term.LineText(0))
	}
	// Empty rows below the cursor are not kept as content.
	term = New(10, 5, 100)
	feed(term, "a\r\nb")
	term.Resize(5, 5)
	if term.ScreenText() != "a\nb" || term.History() != 0 || term.Cursor().Row != 1 {
		t.Fatalf("blank rows %q", term.ScreenText())
	}
	// Content below the cursor stays; the cursor stays on screen.
	term = New(4, 3, 100)
	feed(term, "1\r\n2\r\n3\x1b[1;1H")
	term.Resize(3, 2)
	if c := term.Cursor(); c.Row != 0 || term.LineText(term.History()) != "1" {
		t.Fatalf("cursor above content %+v %q", c, term.ScreenText())
	}
}

// TestReflowBehindAltScreen: the main screen reflows while a full-screen
// program runs; its saved cursor follows.
func TestReflowBehindAltScreen(t *testing.T) {
	term := New(8, 3, 100)
	feed(term, "abcdefghij\r\n$ ")
	feed(term, "\x1b[?1049h\x1b[1;1Hfull")
	term.Resize(4, 3)
	if term.LineText(term.History()) != "full" {
		t.Fatal("alt content")
	}
	feed(term, "\x1b[?1049lX")
	if got := term.Text(0, 0, term.Total()-1, 4); got != "abcdefghij\n$ X" {
		t.Fatalf("after alt %q", got)
	}
	// The alternate screen grows and shrinks around its cursor.
	term = New(6, 4, 100)
	feed(term, "\x1b[?1049h\x1b[4;1Hbottom")
	term.Resize(5, 2)
	if c := term.Cursor(); c.Row != 1 {
		t.Fatalf("alt cursor %+v", c)
	}
	term.Resize(7, 5)
	if _, rows := term.Size(); rows != 5 || len(term.alt.lines) != 5 {
		t.Fatal("alt rows")
	}
}

func TestPaletteRGB(t *testing.T) {
	if PaletteRGB(16) != [3]uint8{0, 0, 0} || PaletteRGB(231) != [3]uint8{255, 255, 255} || PaletteRGB(255) != [3]uint8{238, 238, 238} || PaletteRGB(9) != [3]uint8{255, 0, 0} {
		t.Fatal("palette")
	}
	if RuneWidth('a') != 1 || RuneWidth('世') != 2 || RuneWidth(0x301) != 0 || RuneWidth('👍') != 2 {
		t.Fatal("widths")
	}
}
