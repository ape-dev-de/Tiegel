package protocol

import (
	"runtime"
	"strings"
	"testing"
)

func TestUTF16Len(t *testing.T) {
	cases := map[string]int{
		"":         0,
		"abc":      3,
		"äöü":      3, // 2 bytes each, 1 unit each
		"€":        1, // 3 bytes, 1 unit
		"😀":        2, // 4 bytes, surrogate pair
		"a😀b":      4,
		"👩‍💻":      5, // woman + ZWJ + laptop
		"\xff\xfe": 2, // invalid bytes count one unit each
	}
	for s, want := range cases {
		if got := UTF16Len(s); got != want {
			t.Errorf("UTF16Len(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestByteUTF16Conversion(t *testing.T) {
	line := "$ä = '😀x';" // $=1B ä=2B space... emoji=4B
	// byte offsets of each rune start and expected utf16 column
	type pt struct{ b, u int }
	var pts []pt
	u := 0
	for i, r := range line {
		pts = append(pts, pt{i, u})
		if r >= 0x10000 {
			u += 2
		} else {
			u++
		}
	}
	pts = append(pts, pt{len(line), u})
	for _, p := range pts {
		if got := ByteToUTF16(line, p.b); got != p.u {
			t.Errorf("ByteToUTF16(%d) = %d, want %d", p.b, got, p.u)
		}
		if got := UTF16ToByte(line, p.u); got != p.b {
			t.Errorf("UTF16ToByte(%d) = %d, want %d", p.u, got, p.b)
		}
	}
	emoji := strings.Index(line, "😀")
	// Inside the multi-byte emoji: round down.
	if got := ByteToUTF16(line, emoji+2); got != ByteToUTF16(line, emoji) {
		t.Errorf("mid-rune byte offset not rounded down: %d", got)
	}
	// Between the surrogates: round down to the emoji start.
	if got := UTF16ToByte(line, ByteToUTF16(line, emoji)+1); got != emoji {
		t.Errorf("mid-surrogate column = %d, want %d", got, emoji)
	}
	// Clamping.
	if got := ByteToUTF16(line, 1000); got != UTF16Len(line) {
		t.Errorf("clamp byte: %d", got)
	}
	if got := UTF16ToByte(line, 1000); got != len(line) {
		t.Errorf("clamp col: %d", got)
	}
}

func TestOffsetPosition(t *testing.T) {
	text := "<?php\n$ä = '😀';\r\necho $ä;\n"
	cases := []struct {
		off int
		pos Position
	}{
		{0, Position{0, 0}},
		{5, Position{0, 5}},
		{6, Position{1, 0}},
		{6 + 1, Position{1, 1}},                        // after $
		{6 + 3, Position{1, 2}},                        // after ä
		{strings.Index(text, "😀") + 4, Position{1, 8}}, // after emoji: $ ä space = space quote (6) + surrogate pair (2)
		{strings.Index(text, "echo"), Position{2, 0}},
		{len(text), Position{3, 0}},
	}
	for _, c := range cases {
		if got := PositionOf(text, c.off); got != c.pos {
			t.Errorf("PositionOf(%d) = %v, want %v", c.off, got, c.pos)
		}
		got, err := OffsetOf(text, c.pos)
		if err != nil || got != c.off {
			t.Errorf("OffsetOf(%v) = %d, %v, want %d", c.pos, got, err, c.off)
		}
	}
	// Character beyond line end clamps before "\r\n".
	if got, _ := OffsetOf(text, Position{1, 100}); got != strings.Index(text, "\r\n") {
		t.Errorf("clamp = %d", got)
	}
	if _, err := OffsetOf(text, Position{10, 0}); err == nil {
		t.Error("expected out of range error")
	}
	if _, err := OffsetOf(text, Position{-1, 0}); err == nil {
		t.Error("expected negative error")
	}
	if got := PositionOf(text, -5); got != (Position{}) {
		t.Errorf("negative offset = %v", got)
	}
	if got := PositionOf(text, 1e6); got != (Position{3, 0}) {
		t.Errorf("large offset = %v", got)
	}
}

func TestApplyChanges(t *testing.T) {
	text := "Grüße 😀\nzwei\n"
	r := func(l1, c1, l2, c2 int) *Range {
		return &Range{Start: Position{l1, c1}, End: Position{l2, c2}}
	}
	got, err := ApplyChanges(text, []TextDocumentContentChangeEvent{
		{Range: r(0, 6, 0, 8), Text: "Welt"}, // replace emoji (2 units)
		{Range: r(1, 0, 1, 4), Text: "drei"},
		{Range: r(0, 2, 0, 3), Text: "ue"}, // ü -> ue
		{Range: r(2, 0, 2, 0), Text: "ende"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Grueße Welt\ndrei\nende"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got, err = ApplyChanges("x", []TextDocumentContentChangeEvent{{Text: "full"}, {Range: r(0, 4, 0, 4), Text: "!"}})
	if err != nil || got != "full!" {
		t.Fatalf("full: %q %v", got, err)
	}
	if _, err := ApplyChanges("x", []TextDocumentContentChangeEvent{{Range: r(5, 0, 5, 0)}}); err == nil {
		t.Error("expected start error")
	}
	if _, err := ApplyChanges("x", []TextDocumentContentChangeEvent{{Range: r(0, 0, 5, 0)}}); err == nil {
		t.Error("expected end error")
	}
	if _, err := ApplyChanges("xy", []TextDocumentContentChangeEvent{{Range: r(0, 2, 0, 1)}}); err == nil {
		t.Error("expected order error")
	}
}

func TestURI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	u := URIFromPath("/tmp/a b/ä.php")
	if u != "file:///tmp/a%20b/%C3%A4.php" {
		t.Fatalf("uri = %s", u)
	}
	p, err := u.Path()
	if err != nil || p != "/tmp/a b/ä.php" {
		t.Fatalf("path = %q %v", p, err)
	}
	if _, err := DocumentURI("http://x/y").Path(); err == nil {
		t.Error("expected scheme error")
	}
	if _, err := DocumentURI("file://%zz").Path(); err == nil {
		t.Error("expected parse error")
	}
	if p, err := DocumentURI("file:///C:/x/y.php").Path(); err != nil || !strings.HasPrefix(p, "C:") {
		t.Errorf("drive path = %q %v", p, err)
	}
	if u := URIFromPath("C:/x.php"); u != "file:///C:/x.php" {
		t.Errorf("drive uri = %s", u)
	}
}
