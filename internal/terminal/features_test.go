package terminal

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// fgProc is a fake process with a foreground job.
type fgProc struct {
	*fakeProc
	mu sync.Mutex
	fg string
}

func (p *fgProc) Foreground() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fg
}

func (p *fgProc) set(name string) {
	p.mu.Lock()
	p.fg = name
	p.mu.Unlock()
}

// TestForegroundPoll: a job that prints nothing still shows up.
func TestForegroundPoll(t *testing.T) {
	inner := newFake()
	f := &fgProc{fakeProc: inner}
	ev := &events{}
	m := newManager(inner, ev)
	m.Start = func(Command) (Process, error) { return f, nil }
	info, _ := m.Open(Options{Command: Command{Program: "/bin/fake"}})
	f.set("sleep")
	eventually(t, "poll", func() bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		return len(ev.changed) > 0 && ev.changed[len(ev.changed)-1].Process == "sleep"
	})
	_ = m.Close(info.ID)
}

func TestSessionInputKinds(t *testing.T) {
	f := newFake()
	ev := &events{}
	m := newManager(f, ev)
	info, err := m.Open(Options{Command: Command{Program: "/bin/fake", Cols: 20, Rows: 4}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get(info.ID)
	ctx := context.Background()
	// No reporting modes: nothing is sent.
	if rep, err := s.Mouse(ctx, vt.Mouse{Action: vt.MousePress, Button: vt.MouseLeft}); rep || err != nil {
		t.Fatal("mouse without mode", rep, err)
	}
	if err := s.Focus(ctx, true); err != nil || f.in() != "" {
		t.Fatal("focus without mode")
	}
	_, _ = f.outW.Write([]byte("\x1b[?1000h\x1b[?1006h\x1b[?1004h"))
	eventually(t, "modes", func() bool { m := s.Screen(ScreenOptions{Top: -1}).Modes; return m.MouseSGR && m.FocusEvents })
	if rep, err := s.Mouse(ctx, vt.Mouse{Action: vt.MousePress, Button: vt.MouseLeft, Row: 1, Col: 2}); !rep || err != nil {
		t.Fatal("mouse", rep, err)
	}
	if err := s.Focus(ctx, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "input", func() bool { return f.in() == "\x1b[<0;3;2M\x1b[O" })
	_ = m.Close(info.ID)
}

func TestSessionClipboardColorsForeground(t *testing.T) {
	inner := newFake()
	f := &fgProc{fakeProc: inner}
	ev := &events{}
	m := newManager(inner, ev)
	m.Start = func(Command) (Process, error) { return f, nil }
	var mu sync.Mutex
	var clips []string
	m.OnClipboard = func(id, text string) { mu.Lock(); clips = append(clips, id+":"+text); mu.Unlock() }
	c := vt.DefaultColors()
	c.BG = [3]uint8{0xff, 0xff, 0xff}
	m.SetColors(c)
	info, err := m.Open(Options{Command: Command{Program: "/bin/fake", Cols: 20, Rows: 4}, Clipboard: true})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get(info.ID)
	enc := base64.StdEncoding.EncodeToString([]byte("hello"))
	_, _ = inner.outW.Write([]byte("\x1b]52;c;" + enc + "\x07\x1b]11;?\x07"))
	eventually(t, "clipboard", func() bool { mu.Lock(); defer mu.Unlock(); return len(clips) == 1 && clips[0] == info.ID+":hello" })
	eventually(t, "color reply", func() bool { return inner.in() == "\x1b]11;rgb:ffff/ffff/ffff\x07" })
	// Colors set later reach running sessions.
	c.BG = [3]uint8{0, 0, 0}
	m.SetColors(c)
	_, _ = inner.outW.Write([]byte("\x1b]11;?\x07"))
	eventually(t, "new colors", func() bool { return strings.HasSuffix(inner.in(), "rgb:0000/0000/0000\x07") })
	// The foreground job shows in Info and in a change event.
	f.set("vim")
	time.Sleep(fgInterval)
	_, _ = inner.outW.Write([]byte("x"))
	eventually(t, "busy", func() bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		for _, i := range ev.changed {
			if i.Busy && i.Process == "vim" {
				return true
			}
		}
		return false
	})
	f.set("")
	time.Sleep(fgInterval)
	if i := s.Info(); i.Busy || i.Process != "" {
		t.Fatalf("idle %+v", i)
	}
	if i := s.Rename("mine"); !i.Renamed {
		t.Fatal("renamed")
	}
	_ = m.Close(info.ID)
	// A session without the clipboard option drops writes.
	g := newFake()
	m.Start = func(Command) (Process, error) { return g, nil }
	info, _ = m.Open(Options{Command: Command{Program: "/bin/fake"}})
	_, _ = g.outW.Write([]byte("\x1b]52;c;" + enc + "\x07ok"))
	s, _ = m.Get(info.ID)
	eventually(t, "output", func() bool { return s.ScreenText() == "ok" })
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	n := len(clips)
	mu.Unlock()
	if n != 1 {
		t.Fatal("clipboard without permission")
	}
	_ = m.Close(info.ID)
}

// TestSyncHoldsOutput: output events wait while a synchronized update
// (mode 2026) is open.
func TestSyncHoldsOutput(t *testing.T) {
	f := newFake()
	ev := &events{}
	m := newManager(f, ev)
	m.Interval = 5 * time.Millisecond
	info, _ := m.Open(Options{Command: Command{Program: "/bin/fake", Cols: 20, Rows: 4}})
	_, _ = f.outW.Write([]byte("\x1b[?2026hhalf"))
	time.Sleep(60 * time.Millisecond)
	if strings.Contains(ev.data(), "half") {
		t.Fatal("published inside a synchronized update")
	}
	_, _ = f.outW.Write([]byte(" done\x1b[?2026l"))
	eventually(t, "frame", func() bool { return strings.Contains(ev.data(), "half done") })
	// An update that never ends is published after maxSync.
	_, _ = f.outW.Write([]byte("\x1b[?2026hstuck"))
	eventually(t, "timeout", func() bool { return strings.Contains(ev.data(), "stuck") })
	_ = m.Close(info.ID)
}

func TestFind(t *testing.T) {
	term := vt.New(10, 3, 100)
	_, _ = term.Write([]byte("foo bar\r\nxxfooyyFOO日本foo"))
	res, err := find(term, FindOptions{Query: "foo"})
	if err != nil || len(res.Matches) != 4 {
		t.Fatalf("%+v %v", res, err)
	}
	// A match over the wrap of line 1 into line 2, columns after the
	// wide characters.
	if m := res.Matches[2]; m.Line != 1 || m.Col != 7 || m.EndLine != 1 || m.EndCol != 10 {
		t.Fatalf("third %+v", m)
	}
	if m := res.Matches[3]; m.Line != 2 || m.Col != 4 || m.EndCol != 7 {
		t.Fatalf("after wide %+v", m)
	}
	res, _ = find(term, FindOptions{Query: "yyFOO日", CaseSensitive: true})
	if len(res.Matches) != 1 || res.Matches[0].Line != 1 || res.Matches[0].EndLine != 2 || res.Matches[0].EndCol != 2 {
		t.Fatalf("over wrap %+v", res)
	}
	res, _ = find(term, FindOptions{Query: "FOO", CaseSensitive: true})
	if len(res.Matches) != 1 {
		t.Fatalf("case %+v", res)
	}
	res, _ = find(term, FindOptions{Query: `f.o`, Regex: true, Limit: 2})
	if len(res.Matches) != 2 || !res.Truncated {
		t.Fatalf("limit %+v", res)
	}
	if _, err := find(term, FindOptions{Query: "(", Regex: true}); err == nil {
		t.Fatal("bad regex")
	}
	if res, _ := find(term, FindOptions{}); len(res.Matches) != 0 {
		t.Fatal("empty query")
	}
	if res, _ := find(term, FindOptions{Query: "x*", Regex: true}); len(res.Matches) != 1 {
		t.Fatalf("empty matches skipped %+v", res)
	}
	// Through a session.
	f := newFake()
	m := newManager(f, &events{})
	info, _ := m.Open(Options{Command: Command{Program: "/bin/fake"}})
	s, _ := m.Get(info.ID)
	_, _ = f.outW.Write([]byte("needle"))
	eventually(t, "output", func() bool { return s.ScreenText() == "needle" })
	if res, err := s.Find(FindOptions{Query: "eed"}); err != nil || len(res.Matches) != 1 {
		t.Fatal(res, err)
	}
	_ = m.Close(info.ID)
}

func TestFindURLs(t *testing.T) {
	cases := map[string][]string{
		"see https://example.com/a.":           {"https://example.com/a"},
		"(https://x.org/wiki/A_(b)) and more":  {"https://x.org/wiki/A_(b)"},
		"<http://h.test/?q=1&r=2>, next":       {"http://h.test/?q=1&r=2"},
		"https:// broken, ftp://no":            nil,
		"two http://a.test/x http://b.test/y]": {"http://a.test/x", "http://b.test/y"},
		"quoted 'https://q.test/p'":            {"https://q.test/p"},
	}
	for text, want := range cases {
		var got []string
		for _, m := range findURLs(text) {
			got = append(got, text[m[0]:m[1]])
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%q: %q want %q", text, got, want)
		}
	}
}

func TestScreenLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/a.php", []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := "日 https://e.test/x a.php:3\r\n\x1b]8;;https://osc.test\x1b\\https://e.test/y\x1b]8;;\x1b\\ b.php:9\r\n\x1b[1mX"
	r := Emulate([]byte(data), EmulateOptions{Cols: 40, Rows: 3, Screen: ScreenOptions{Top: -1, URLs: true,
		Linker: &run.Linker{Dirs: []string{dir}, Exists: run.FileExists}}})
	l0 := r.Screen.Lines[0].Links
	if len(l0) != 2 || l0[0].URL != "https://e.test/x" || l0[0].Col != 3 || l0[0].EndCol != 19 || l0[1].Line != 3 || l0[1].Col != 20 {
		t.Fatalf("line 0 %+v", l0)
	}
	// The OSC 8 target wins over the printed URL under it; b.php does
	// not exist.
	l1 := r.Screen.Lines[1].Links
	if len(l1) != 1 || l1[0].URL != "https://osc.test" || l1[0].EndCol != 16 {
		t.Fatalf("line 1 %+v", l1)
	}
	if sp := r.Screen.Lines[1].Spans; sp[0].URL != "https://osc.test" {
		t.Fatalf("span url %+v", sp)
	}
	if r.Text != "日 https://e.test/x a.php:3\nhttps://e.test/y b.php:9\nX" {
		t.Fatalf("text %q", r.Text)
	}
	// Without URLs no links.
	r = Emulate([]byte(data), EmulateOptions{Cols: 40, Rows: 3, Screen: ScreenOptions{Top: -1}})
	if len(r.Screen.Lines[0].Links) != 0 {
		t.Fatal("links without option")
	}
}

func TestEmulateOptions(t *testing.T) {
	r := Emulate([]byte("abcdef\x1b]7;file://h/tmp\x07\x1b]777;x\x07\x1b[c"), EmulateOptions{ResizeCols: 3, ResizeRows: 2, Scrollback: -1})
	if r.Screen.Cols != 3 || r.Screen.Rows != 2 || r.Text != "abcdef" || r.Cwd != "/tmp" || len(r.OSC) != 1 || r.Replies != "\x1b[?62;22c" {
		t.Fatalf("%+v", r)
	}
	r = Emulate([]byte("x"), EmulateOptions{ResizeRows: 5})
	if r.Screen.Cols != DefaultCols || r.Screen.Rows != 5 {
		t.Fatalf("defaults %+v", r.Screen)
	}
	r = Emulate(nil, EmulateOptions{Cols: 5, Rows: 2, Screen: ScreenOptions{Top: 99, NoSpans: true}})
	if len(r.Screen.Lines) != 0 || r.Text != "" {
		t.Fatalf("empty %+v", r)
	}
}

func TestSpanAttributes(t *testing.T) {
	r := Emulate([]byte("\x1b[4:3;58;5;1;53;5;8;9;7;2;3ma"), EmulateOptions{Cols: 5, Rows: 1, Screen: ScreenOptions{Top: -1}})
	sp := r.Screen.Lines[0].Spans[0]
	if sp.UnderlineStyle != 3 || sp.UL != "p1" || !sp.Overline || !sp.Blink || !sp.Hidden || !sp.Strike || !sp.Inverse || !sp.Faint || !sp.Italic || !sp.Underline {
		t.Fatalf("%+v", sp)
	}
}

// TestRealForeground runs sleep in a real shell and sees it as the
// foreground job.
func TestRealForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	m := &Manager{}
	info, err := m.Open(Options{Command: Command{Program: sh, Args: []string{"-i"}, Env: []string{"PS1=$ ", "PATH=" + os.Getenv("PATH")}, Cols: 40, Rows: 5}})
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer m.Close(info.ID)
	s, _ := m.Get(info.ID)
	_ = s.Input(context.Background(), []byte("sleep 5\r"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if i := s.Info(); i.Busy {
			if i.Process != "sleep" {
				t.Fatalf("process %q", i.Process)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Skip("job control not available (no foreground job seen)")
}
