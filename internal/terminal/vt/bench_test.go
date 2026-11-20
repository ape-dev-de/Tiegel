package vt

import (
	"bytes"
	"strconv"
	"testing"
)

// catOutput returns about n bytes of output like `cat` of source code
// with some colored lines (grep --color, ls --color, a highlighter).
func catOutput(n int) []byte {
	var b bytes.Buffer
	words := []string{"function", "return", "$value", "array", "public", "static", "=>", "if", "(", ")", "{", "}", "null", "true", "// comment", "'string'"}
	for i := 0; b.Len() < n; i++ {
		if i%5 == 0 {
			b.WriteString("\x1b[38;5;" + strconv.Itoa(i%256) + "m")
		}
		if i%11 == 0 {
			b.WriteString("\x1b[1;38;2;200;100;" + strconv.Itoa(i%256) + "m")
		}
		w := 0
		for j := 0; w < 20+i%90; j++ {
			s := words[(i+j)%len(words)]
			b.WriteString(s)
			b.WriteByte(' ')
			w += len(s) + 1
		}
		if i%5 == 0 || i%11 == 0 {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

// BenchmarkCat10MB parses 10 MB of colored text output in 32 KiB reads
// (like the session reader) into a 160×50 terminal with the default
// scrollback.
func BenchmarkCat10MB(b *testing.B) {
	data := catOutput(10 << 20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		t := New(160, 50, -1)
		for p := data; len(p) > 0; {
			n := min(len(p), 32<<10)
			_, _ = t.Write(p[:n])
			p = p[n:]
		}
	}
}

// BenchmarkFullRedraw is a TUI repainting the whole alternate screen
// (htop-like: cursor addressing and colors on every row), 100 frames.
func BenchmarkFullRedraw(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString("\x1b[?1049h")
	for f := 0; f < 100; f++ {
		buf.WriteString("\x1b[?2026h\x1b[H")
		for r := 1; r <= 50; r++ {
			buf.WriteString("\x1b[" + strconv.Itoa(r) + ";1H\x1b[48;5;236m\x1b[38;2;120;200;80m")
			for c := 0; c < 160; c++ {
				buf.WriteByte(byte('a' + (c+r+f)%26))
			}
			buf.WriteString("\x1b[0m")
		}
		buf.WriteString("\x1b[?2026l")
	}
	data := buf.Bytes()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	t := New(160, 50, -1)
	for b.Loop() {
		_, _ = t.Write(data)
	}
}
