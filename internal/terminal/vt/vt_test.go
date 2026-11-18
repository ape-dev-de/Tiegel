package vt

import (
	"strings"
	"testing"
)

func feed(t *Terminal, s string) { _, _ = t.Write([]byte(s)) }

// fixture is an escape sequence fixture: input, expected screen text
// and cursor.
type fixture struct {
	name     string
	cols     int
	rows     int
	in       string
	screen   string
	row, col int
}

var fixtures = []fixture{
	{name: "plain", in: "hello", screen: "hello", row: 0, col: 5},
	{name: "crlf", in: "ab\r\ncd", screen: "ab\ncd", row: 1, col: 2},
	{name: "lf keeps column", in: "ab\ncd", screen: "ab\n  cd", row: 1, col: 4},
	{name: "backspace", in: "abc\b\bX", screen: "aXc", row: 0, col: 2},
	{name: "tab", in: "a\tb", screen: "a       b", row: 0, col: 9},
	{name: "cup", in: "\x1b[2;3Hx", screen: "\n  x", row: 1, col: 3},
	{name: "cup default", in: "abc\x1b[Hx", screen: "xbc", row: 0, col: 1},
	{name: "cursor moves", in: "\x1b[3B\x1b[4Cx\x1b[2Ay\x1b[3Dz", screen: "\n   z y\n\n    x", row: 1, col: 4},
	{name: "cha vpa", in: "\x1b[5Gx\x1b[3dy", screen: "    x\n\n     y", row: 2, col: 6},
	{name: "erase line right", in: "abcdef\x1b[1;3H\x1b[K", screen: "ab", row: 0, col: 2},
	{name: "erase line left", in: "abcdef\x1b[1;3H\x1b[1K", screen: "   def", row: 0, col: 2},
	{name: "erase line all", in: "abcdef\x1b[2K", screen: "", row: 0, col: 6},
	{name: "erase display below", in: "aa\r\nbb\r\ncc\x1b[2;2H\x1b[J", screen: "aa\nb", row: 1, col: 1},
	{name: "erase display above", in: "aa\r\nbb\r\ncc\x1b[2;1H\x1b[1J", screen: "\n b\ncc", row: 1, col: 0},
	{name: "erase display all", in: "aa\r\nbb\x1b[2J", screen: "", row: 1, col: 2},
	{name: "insert chars", in: "abcd\x1b[1;2H\x1b[2@", screen: "a  bcd", row: 0, col: 1},
	{name: "delete chars", in: "abcdef\x1b[1;2H\x1b[2P", screen: "adef", row: 0, col: 1},
	{name: "erase chars", in: "abcdef\x1b[1;2H\x1b[2X", screen: "a  def", row: 0, col: 1},
	{name: "insert lines", in: "11\r\n22\r\n33\x1b[2;1H\x1b[L", screen: "11\n\n22\n33", row: 1, col: 0},
	{name: "delete lines", in: "11\r\n22\r\n33\x1b[1;1H\x1b[M", screen: "22\n33", row: 0, col: 0},
	{name: "autowrap", cols: 5, rows: 3, in: "abcdefg", screen: "abcde\nfg", row: 1, col: 2},
	{name: "pending wrap", cols: 5, rows: 3, in: "abcde", screen: "abcde", row: 0, col: 4},
	{name: "pending wrap cr", cols: 5, rows: 3, in: "abcde\rX", screen: "Xbcde", row: 0, col: 1},
	{name: "no autowrap", cols: 5, rows: 3, in: "\x1b[?7labcdefg", screen: "abcdg", row: 0, col: 4},
	{name: "scroll", cols: 5, rows: 2, in: "1\r\n2\r\n3", screen: "2\n3", row: 1, col: 1},
	{name: "scroll region", cols: 5, rows: 4, in: "a\r\nb\r\nc\r\nd\x1b[2;3r\x1b[3;1H\nX", screen: "a\nc\nX\nd", row: 2, col: 1},
	{name: "reverse index", cols: 5, rows: 3, in: "a\r\nb\x1b[1;1H\x1bMz", screen: "z\na\nb", row: 0, col: 1},
	{name: "save restore", in: "ab\x1b7\x1b[3;3Hx\x1b8c", screen: "abc\n\n  x", row: 0, col: 3},
	{name: "csi save restore", in: "ab\x1b[s\x1b[3;3Hx\x1b[uc", screen: "abc\n\n  x", row: 0, col: 3},
	{name: "dec graphics", in: "\x1b(0lqk\x1b(Bq", screen: "┌─┐q", row: 0, col: 4},
	{name: "shift out", in: "\x1b)0a\x0eq\x0fq", screen: "a─q", row: 0, col: 3},
	{name: "utf8", in: "äöü€", screen: "äöü€", row: 0, col: 4},
	{name: "wide", in: "a世b", screen: "a世b", row: 0, col: 4},
	{name: "combining", in: "éx", screen: "éx", row: 0, col: 2},
	{name: "rep", in: "a\x1b[3b", screen: "aaaa", row: 0, col: 4},
	{name: "osc ignored", in: "\x1b]0;title\x07ok\x1b]2;t2\x1b\\!", screen: "ok!", row: 0, col: 3},
	{name: "dcs ignored", in: "\x1bPq#0;1;1\x1b\\ok", screen: "ok", row: 0, col: 2},
	{name: "unknown csi ignored", in: "a\x1b[>4;1mb\x1b[?1;2$pc", screen: "abc", row: 0, col: 3},
	{name: "cancel", in: "\x1b[3\x18x", screen: "x", row: 0, col: 1},
	{name: "sgr no effect on text", in: "\x1b[1;31mred\x1b[0m", screen: "red", row: 0, col: 3},
	{name: "tab stops", in: "\x1b[3g\x1b[1;4H\x1bH\rx\ty", screen: "x  y", row: 0, col: 4},
	{name: "back tab", in: "\x1b[1;12H\x1b[Zx", screen: "        x", row: 0, col: 9},
	{name: "insert mode", in: "abc\r\x1b[4hX\x1b[4lY", screen: "XYbc", row: 0, col: 2},
	{name: "decaln", cols: 3, rows: 2, in: "\x1b#8", screen: "EEE\nEEE", row: 0, col: 0},
	{name: "origin mode", cols: 5, rows: 4, in: "\x1b[2;3r\x1b[?6h\x1b[1;1Hx", screen: "\nx", row: 1, col: 1},
	{name: "linefeed mode", in: "\x1b[20hab\ncd", screen: "ab\ncd", row: 1, col: 2},
	{name: "wide at end wraps", cols: 3, rows: 2, in: "ab世", screen: "ab\n世", row: 1, col: 2},
	{name: "utf8 invalid", in: "a\xffb", screen: "a�b", row: 0, col: 3},
}

func TestFixtures(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			cols, rows := f.cols, f.rows
			if cols == 0 {
				cols, rows = 20, 5
			}
			term := New(cols, rows, 100)
			feed(term, f.in)
			if got := term.ScreenText(); got != f.screen {
				t.Errorf("screen\n got %q\nwant %q", got, f.screen)
			}
			c := term.Cursor()
			if c.Row != f.row || c.Col != f.col {
				t.Errorf("cursor %d,%d want %d,%d", c.Row, c.Col, f.row, f.col)
			}
		})
	}
}

// TestSplitWrites: every fixture gives the same screen when fed one byte
// at a time (sequences and UTF-8 split across writes).
func TestSplitWrites(t *testing.T) {
	for _, f := range fixtures {
		cols, rows := f.cols, f.rows
		if cols == 0 {
			cols, rows = 20, 5
		}
		term := New(cols, rows, 100)
		for i := 0; i < len(f.in); i++ {
			_, _ = term.Write([]byte{f.in[i]})
		}
		if got := term.ScreenText(); got != f.screen {
			t.Errorf("%s: screen %q want %q", f.name, got, f.screen)
		}
	}
}

func TestSGR(t *testing.T) {
	term := New(40, 2, 0)
	feed(term, "\x1b[1;3;4;31;42ma\x1b[0;38;5;200;48;2;1;2;3mb\x1b[38:2::10:20:30;7mc\x1b[22;23;24;39;49;27md\x1b[91;103;2;9me\x1b[mf")
	l := term.Line(0)
	want := []Style{
		{FG: Indexed(1), BG: Indexed(2), Attr: Bold | Italic | Underline},
		{FG: Indexed(200), BG: RGB(1, 2, 3)},
		{FG: RGB(10, 20, 30), BG: RGB(1, 2, 3), Attr: Inverse},
		{},
		{FG: Indexed(9), BG: Indexed(11), Attr: Faint | Strike},
		{},
	}
	for i, w := range want {
		if l.Cells[i].Style != w {
			t.Errorf("cell %d style %+v want %+v", i, l.Cells[i].Style, w)
		}
	}
	feed(term, "\x1b[4:0m\x1b[4:3mx\x1b[4:0my\x1b[5;8m\x1b[25;28mz\x1b[38:5:17mw")
	l = term.Line(0)
	if l.Cells[6].Style.Attr != Underline || l.Cells[7].Style.Attr != 0 || l.Cells[8].Style.Attr != 0 || l.Cells[9].Style.FG != Indexed(17) {
		t.Errorf("sub-params: %+v %+v %+v %+v", l.Cells[6].Style, l.Cells[7].Style, l.Cells[8].Style, l.Cells[9].Style)
	}
	if r, g, b, ok := RGB(1, 2, 3).RGB(); !ok || r != 1 || g != 2 || b != 3 {
		t.Error("RGB components")
	}
	if i, ok := Indexed(7).Index(); !ok || i != 7 {
		t.Error("Index")
	}
	if _, ok := Default.Index(); ok || !Default.IsDefault() {
		t.Error("default color")
	}
	if _, _, _, ok := Indexed(1).RGB(); ok {
		t.Error("indexed is not RGB")
	}
}

func TestEraseUsesBackground(t *testing.T) {
	term := New(5, 2, 0)
	feed(term, "\x1b[44m\x1b[2J")
	if bg := term.Line(0).Cells[3].Style.BG; bg != Indexed(4) {
		t.Fatalf("erased cell bg %v", bg)
	}
}

func TestScrollback(t *testing.T) {
	term := New(10, 3, 5)
	for i := 0; i < 10; i++ {
		feed(term, strings.Repeat(string(rune('a'+i)), 3)+"\r\n")
	}
	if term.History() != 5 || term.Dropped() != 3 {
		t.Fatalf("history %d dropped %d", term.History(), term.Dropped())
	}
	if got := term.LineText(0); got != "ddd" {
		t.Fatalf("oldest %q", got)
	}
	if got := term.LineText(4); got != "hhh" {
		t.Fatalf("newest %q", got)
	}
	if term.Total() != 8 {
		t.Fatalf("total %d", term.Total())
	}
	if got := term.Text(0, 0, 2, 10); got != "ddd\neee\nfff" {
		t.Fatalf("text %q", got)
	}
	feed(term, "\x1b[3J")
	if term.History() != 0 {
		t.Fatal("ED 3 keeps scrollback")
	}
	if l := term.Line(-1); l.Cells != nil {
		t.Fatal("negative line")
	}
	if l := term.Line(99); l.Cells != nil {
		t.Fatal("line past end")
	}
}

func TestNoScrollback(t *testing.T) {
	term := New(5, 1, 0)
	feed(term, "a\r\nb")
	if term.History() != 0 || term.Dropped() != 1 {
		t.Fatalf("history %d dropped %d", term.History(), term.Dropped())
	}
}

func TestAltScreen(t *testing.T) {
	term := New(10, 3, 10)
	feed(term, "main\x1b[?1049h")
	if !term.Modes().AltScreen {
		t.Fatal("not on alt screen")
	}
	feed(term, "\x1b[Halt\r\n1\r\n2\r\n3\r\n4")
	if term.History() != 0 {
		t.Fatal("alt screen fed the scrollback")
	}
	if got := term.ScreenText(); got != "2\n3\n4" {
		t.Fatalf("alt %q", got)
	}
	feed(term, "\x1b[?1049l")
	if got := term.ScreenText(); got != "main" {
		t.Fatalf("back %q", got)
	}
	if c := term.Cursor(); c.Row != 0 || c.Col != 4 {
		t.Fatalf("cursor restored to %+v", c)
	}
	feed(term, "\x1b[?47hX\x1b[?47l")
	if got := term.ScreenText(); got != "main" {
		t.Fatalf("47: %q", got)
	}
	feed(term, "\x1b[?1048h\x1b[3;3H\x1b[?1048lY")
	if got := term.ScreenText(); got != "main Y" { // 47 keeps the cursor column
		t.Fatalf("1048: %q", got)
	}
}

func TestModes(t *testing.T) {
	term := New(10, 3, 10)
	feed(term, "\x1b[?1h\x1b=\x1b[?2004h\x1b[?25l\x1b[?1002h\x1b[?1006h\x1b[?1004h\x1b[?5h\x1b[4 q")
	m := term.Modes()
	if !m.AppCursor || !m.AppKeypad || !m.BracketedPaste || m.Mouse != 1002 || !m.MouseSGR || !m.FocusEvents || !m.ReverseVideo {
		t.Fatalf("modes %+v", m)
	}
	if c := term.Cursor(); c.Visible || c.Shape != 4 {
		t.Fatalf("cursor %+v", c)
	}
	feed(term, "\x1b[?1l\x1b>\x1b[?2004l\x1b[?25h\x1b[?1002l")
	m = term.Modes()
	if m.AppCursor || m.AppKeypad || m.BracketedPaste || m.Mouse != 0 {
		t.Fatalf("modes after reset %+v", m)
	}
	feed(term, "\x1b[?1h\x1b[1;31m\x1b[!p")
	if term.Modes().AppCursor || term.pen != (Style{}) {
		t.Fatal("DECSTR")
	}
	feed(term, "abc\x1bc")
	if term.ScreenText() != "" || term.Cursor().Row != 0 {
		t.Fatal("RIS")
	}
}

func TestReplies(t *testing.T) {
	term := New(10, 5, 10)
	var got []string
	term.Reply = func(b []byte) { got = append(got, string(b)) }
	feed(term, "\x1b[3;4H\x1b[6n\x1b[5n\x1b[c\x1b[>c")
	want := []string{"\x1b[3;4R", "\x1b[0n", "\x1b[?62;22c", "\x1b[>1;10;0c"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("replies %q", got)
	}
}

func TestTitleAndDir(t *testing.T) {
	term := New(10, 2, 0)
	feed(term, "\x1b]0;my\x01 title\x07\x1b]7;file://host/Users/a%20b/x\x1b\\")
	if term.Title() != "my title" {
		t.Fatalf("title %q", term.Title())
	}
	if term.Dir() != "/Users/a b/x" {
		t.Fatalf("dir %q", term.Dir())
	}
	feed(term, "\x1b]7;http://x/y\x07")
	if term.Dir() != "" {
		t.Fatal("non-file URL")
	}
	feed(term, "\x1b]2;t\x1b[1mX")
	if term.Title() != "t" || term.ScreenText() != "X" {
		t.Fatalf("OSC ended by ESC: %q %q", term.Title(), term.ScreenText())
	}
	feed(term, "\a\a")
	if term.Bells() != 2 {
		t.Fatal("bells")
	}
}

func TestWideAndSpans(t *testing.T) {
	term := New(10, 2, 0)
	feed(term, "a世\x1b[31mbc\x1b[0m d")
	sp := term.Spans(0)
	type s struct {
		col, w int
		text   string
	}
	var got []s
	for _, x := range sp {
		got = append(got, s{x.Col, x.Width, x.Text})
	}
	want := []s{{0, 1, "a"}, {1, 2, "世"}, {3, 2, "bc"}, {5, 2, " d"}}
	if len(got) != len(want) {
		t.Fatalf("spans %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("span %d %+v want %+v", i, got[i], want[i])
		}
	}
	// Overwriting half of a wide character clears it.
	feed(term, "\x1b[1;3Hx")
	if got := term.LineText(0); got != "a xbc d" {
		t.Fatalf("after overwrite %q", got)
	}
	feed(term, "\x1b[1;2H世\x1b[1;2Hy")
	if got := term.LineText(0); got != "ay bc d" {
		t.Fatalf("after overwrite left half %q", got)
	}
}

func TestWrappedText(t *testing.T) {
	term := New(4, 3, 10)
	feed(term, "abcdefg\r\nxy")
	if got := term.Text(0, 0, 2, 4); got != "abcdefg\nxy" {
		t.Fatalf("text %q", got)
	}
	if got := term.Text(1, 2, 0, 1); got != "bcdef" {
		t.Fatalf("reversed range %q", got)
	}
}

func TestResize(t *testing.T) {
	term := New(10, 4, 10)
	feed(term, "1\r\n2\r\n3\r\n4")
	term.Resize(10, 2)
	if got := term.ScreenText(); got != "3\n4" {
		t.Fatalf("shrunk %q", got)
	}
	if term.History() != 2 || term.Cursor().Row != 1 {
		t.Fatalf("history %d cursor %+v", term.History(), term.Cursor())
	}
	term.Resize(10, 4)
	if got := term.ScreenText(); got != "1\n2\n3\n4" {
		t.Fatalf("grown %q", got)
	}
	if term.History() != 0 || term.Cursor().Row != 3 {
		t.Fatalf("history %d cursor %+v", term.History(), term.Cursor())
	}
	term.Resize(3, 4)
	if got := term.LineText(term.History()); got != "1" {
		t.Fatalf("narrow %q", got)
	}
	feed(term, "\x1b[1;1Habcdef")
	term.Resize(20, 6)
	cols, rows := term.Size()
	if cols != 20 || rows != 6 {
		t.Fatalf("size %d,%d", cols, rows)
	}
	// Cursor at the top: shrinking drops rows below it.
	term = New(5, 4, 10)
	feed(term, "a\r\nb\r\nc\x1b[H")
	term.Resize(5, 2)
	if got := term.ScreenText(); got != "a\nb" || term.History() != 0 {
		t.Fatalf("shrink below cursor %q history %d", got, term.History())
	}
	term.Resize(5, 2)
	term.Resize(0, 0)
	if c, r := term.Size(); c != 1 || r != 1 {
		t.Fatal("minimum size")
	}
}

func TestResizeWide(t *testing.T) {
	term := New(4, 1, 0)
	feed(term, "ab世")
	term.Resize(3, 1)
	if got := term.LineText(0); got != "ab" {
		t.Fatalf("cut wide %q", got)
	}
}

func TestDefaultScrollback(t *testing.T) {
	if New(2, 2, -1).maxScroll != DefaultScrollback {
		t.Fatal("default scrollback")
	}
}

func TestGrapheme(t *testing.T) {
	term := New(5, 1, 0)
	if term.Grapheme(Cell{}) != " " || term.Grapheme(Cell{R: graphemeBase + 99}) != " " {
		t.Fatal("blank graphemes")
	}
	feed(term, "́") // combining mark at column 0: dropped
	feed(term, "á̂b")
	if got := term.LineText(0); got != "á̂b" {
		t.Fatalf("%q", got)
	}
	feed(term, "\x1b[2b")
	if got := term.LineText(0); got != "á̂bbb" && !strings.HasPrefix(got, "á̂bb") {
		t.Fatalf("rep %q", got)
	}
}

func TestEncodeKey(t *testing.T) {
	cases := []struct {
		k    Key
		m    Modes
		want string
	}{
		{Key{Name: "Up"}, Modes{}, "\x1b[A"},
		{Key{Name: "Up"}, Modes{AppCursor: true}, "\x1bOA"},
		{Key{Name: "Left", Ctrl: true}, Modes{}, "\x1b[1;5D"},
		{Key{Name: "Left", Alt: true}, Modes{}, "\x1bb"},
		{Key{Name: "Right", Alt: true}, Modes{AppCursor: true}, "\x1bf"},
		{Key{Name: "Right", Alt: true, Shift: true}, Modes{}, "\x1b[1;4C"},
		{Key{Name: "Home", Shift: true}, Modes{}, "\x1b[1;2H"},
		{Key{Name: "Delete"}, Modes{}, "\x1b[3~"},
		{Key{Name: "PageUp", Alt: true}, Modes{}, "\x1b[5;3~"},
		{Key{Name: "F1"}, Modes{}, "\x1bOP"},
		{Key{Name: "F2", Shift: true}, Modes{}, "\x1b[1;2Q"},
		{Key{Name: "F12"}, Modes{}, "\x1b[24~"},
		{Key{Name: "Enter"}, Modes{}, "\r"},
		{Key{Name: "Enter", Alt: true}, Modes{}, "\x1b\r"},
		{Key{Name: "Tab"}, Modes{}, "\t"},
		{Key{Name: "Tab", Shift: true}, Modes{}, "\x1b[Z"},
		{Key{Name: "Backspace"}, Modes{}, "\x7f"},
		{Key{Name: "Backspace", Ctrl: true}, Modes{}, "\x08"},
		{Key{Name: "Backspace", Alt: true}, Modes{}, "\x1b\x7f"},
		{Key{Name: "Escape"}, Modes{}, "\x1b"},
		{Key{Name: "Space", Ctrl: true}, Modes{}, "\x00"},
		{Key{Name: "Space"}, Modes{}, " "},
		{Key{Name: "C", Ctrl: true}, Modes{}, "\x03"},
		{Key{Name: "c", Ctrl: true}, Modes{}, "\x03"},
		{Key{Name: "[", Ctrl: true}, Modes{}, "\x1b"},
		{Key{Name: "\\", Ctrl: true}, Modes{}, "\x1c"},
		{Key{Name: "]", Ctrl: true}, Modes{}, "\x1d"},
		{Key{Name: "^", Ctrl: true}, Modes{}, "\x1e"},
		{Key{Name: "_", Ctrl: true}, Modes{}, "\x1f"},
		{Key{Name: "?", Ctrl: true}, Modes{}, "\x7f"},
		{Key{Name: "@", Ctrl: true}, Modes{}, "\x00"},
		{Key{Name: "B", Alt: true}, Modes{}, "\x1bb"},
		{Key{Name: "B", Alt: true, Shift: true}, Modes{}, "\x1bB"},
		{Key{Name: "x", Ctrl: true, Alt: true}, Modes{}, "\x1b\x18"},
		{Key{Name: "!", Ctrl: true}, Modes{}, ""},
		{Key{Name: "Nope"}, Modes{}, ""},
		{Key{Name: ""}, Modes{}, ""},
	}
	for _, c := range cases {
		if got := string(EncodeKey(c.k, c.m)); got != c.want {
			t.Errorf("%+v: %q want %q", c.k, got, c.want)
		}
	}
}

func TestEncodePaste(t *testing.T) {
	if got := string(EncodePaste("a\nb\r\nc", Modes{})); got != "a\rb\rc" {
		t.Fatalf("plain %q", got)
	}
	if got := string(EncodePaste("x\x1b[201~y", Modes{BracketedPaste: true})); got != "\x1b[200~xy\x1b[201~" {
		t.Fatalf("bracketed %q", got)
	}
}

func FuzzWrite(f *testing.F) {
	for _, fx := range fixtures {
		f.Add([]byte(fx.in))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		term := New(7, 3, 5)
		_, _ = term.Write(b)
		term.Resize(4, 5)
		_, _ = term.Write(b)
		_ = term.ScreenText()
		_ = term.Text(0, 0, term.Total(), 10)
	})
}
