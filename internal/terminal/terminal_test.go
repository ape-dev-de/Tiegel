package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// fakeProc is a program whose output the test writes and whose input
// it reads.
type fakeProc struct {
	outR *io.PipeReader
	outW *io.PipeWriter

	mu      sync.Mutex
	input   []byte
	size    [2]int
	hangups int
	kills   int
	exit    chan int
	once    sync.Once
	// ignoreHangup keeps running after Hangup (Kill ends it).
	ignoreHangup bool
}

func newFake() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{outR: r, outW: w, exit: make(chan int, 1)}
}

func (f *fakeProc) Read(b []byte) (int, error) { return f.outR.Read(b) }
func (f *fakeProc) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, b...)
	return len(b), nil
}
func (f *fakeProc) Resize(c, r int) error {
	f.mu.Lock()
	f.size = [2]int{c, r}
	f.mu.Unlock()
	return nil
}
func (f *fakeProc) finish(code int) {
	f.once.Do(func() {
		f.exit <- code
		_ = f.outW.Close()
	})
}
func (f *fakeProc) Wait() (int, error) { return <-f.exit, nil }
func (f *fakeProc) Hangup() error {
	f.mu.Lock()
	f.hangups++
	ign := f.ignoreHangup
	f.mu.Unlock()
	if !ign {
		f.finish(-1)
	}
	return nil
}
func (f *fakeProc) Kill() error {
	f.mu.Lock()
	f.kills++
	f.mu.Unlock()
	f.finish(-1)
	return nil
}
func (f *fakeProc) Close() error { return f.outR.Close() }
func (f *fakeProc) Pid() int     { return 42 }
func (f *fakeProc) in() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.input)
}

type events struct {
	mu      sync.Mutex
	opened  []Info
	out     []Output
	changed []Info
	exited  []Info
	closed  []string
}

func (e *events) data() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	for _, o := range e.out {
		b.WriteString(o.Data)
	}
	return b.String()
}

func newManager(f *fakeProc, ev *events) *Manager {
	return &Manager{
		Start:    func(Command) (Process, error) { return f, nil },
		Interval: time.Millisecond,
		OnOpen:   func(i Info) { ev.mu.Lock(); ev.opened = append(ev.opened, i); ev.mu.Unlock() },
		OnOutput: func(o Output) { ev.mu.Lock(); ev.out = append(ev.out, o); ev.mu.Unlock() },
		OnChange: func(i Info) { ev.mu.Lock(); ev.changed = append(ev.changed, i); ev.mu.Unlock() },
		OnExit:   func(i Info) { ev.mu.Lock(); ev.exited = append(ev.exited, i); ev.mu.Unlock() },
		OnClose:  func(id string) { ev.mu.Lock(); ev.closed = append(ev.closed, id); ev.mu.Unlock() },
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestSessionLifecycle(t *testing.T) {
	f := newFake()
	ev := &events{}
	m := newManager(f, ev)
	info, err := m.Open(Options{Command: Command{Program: "/bin/fake", Args: []string{"-l"}, Dir: "/tmp", Cols: 20, Rows: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "t1" || info.Name != "Local" || info.Kind != KindShell || !info.Running || info.PID != 42 || info.Command[1] != "-l" {
		t.Fatalf("info %+v", info)
	}
	s, err := m.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Output: screen and events; a UTF-8 sequence split over writes is
	// not split over events.
	_, _ = f.outW.Write([]byte("hello \xe2\x82"))
	_, _ = f.outW.Write([]byte("\xac\x1b]0;my title\x07\x1b]7;file:///tmp/x\x07"))
	eventually(t, "output", func() bool { return strings.Contains(s.ScreenText(), "hello €") })
	eventually(t, "events", func() bool { return strings.Contains(ev.data(), "hello €") })
	eventually(t, "change", func() bool {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		return len(ev.changed) > 0 && ev.changed[len(ev.changed)-1].Title == "my title"
	})
	if got := s.Info(); got.Title != "my title" || got.Cwd != "/tmp/x" {
		t.Fatalf("title/cwd %+v", got)
	}
	// Input, keys, paste in order.
	ctx := context.Background()
	if err := s.Input(ctx, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	if err := s.Key(ctx, vt.Key{Name: "Up"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Key(ctx, vt.Key{Name: "Bogus"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	_, _ = f.outW.Write([]byte("\x1b[?2004h\x1b[?1h"))
	eventually(t, "modes", func() bool { return s.Screen(ScreenOptions{Top: -1}).Modes.BracketedPaste })
	if err := s.Paste(ctx, "a\nb"); err != nil {
		t.Fatal(err)
	}
	if err := s.Key(ctx, vt.Key{Name: "Up"}); err != nil {
		t.Fatal(err)
	}
	// Device status report goes back to the program.
	_, _ = f.outW.Write([]byte("\x1b[6n"))
	want := "ls\r\x1b[A\x1b[200~a\rb\x1b[201~\x1bOA\x1b[1;8R"
	eventually(t, "input", func() bool { return f.in() == want })
	_ = s.Input(ctx, nil)

	// Resize.
	if err := s.Resize(30, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(30, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(0, 5); err == nil {
		t.Fatal("zero size accepted")
	}
	f.mu.Lock()
	sz := f.size
	f.mu.Unlock()
	if sz != [2]int{30, 5} || s.Info().Cols != 30 {
		t.Fatalf("size %v", sz)
	}
	// Rename and a second session's default name.
	if got := s.Rename("Build"); got.Name != "Build" {
		t.Fatalf("rename %+v", got)
	}
	// Exit: state, event; input is refused afterwards.
	f.finish(3)
	got, err := s.Wait(ctx)
	if err != nil || got.Running || got.ExitCode != 3 || got.Finished == nil {
		t.Fatalf("wait %+v %v", got, err)
	}
	eventually(t, "exit event", func() bool { ev.mu.Lock(); defer ev.mu.Unlock(); return len(ev.exited) == 1 })
	if err := s.Input(ctx, []byte("x")); !errors.Is(err, ErrExited) {
		t.Fatalf("input after exit: %v", err)
	}
	if err := s.Resize(10, 3); err != nil {
		t.Fatalf("resize after exit: %v", err)
	}
	if l := m.List(); len(l) != 1 || l[0].Running {
		t.Fatalf("list %+v", l)
	}
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(info.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("closed session still there")
	}
	if err := m.Close(info.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("double close")
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.opened) != 1 || len(ev.closed) != 1 {
		t.Fatalf("events opened %d closed %d", len(ev.opened), len(ev.closed))
	}
}

func TestCloseRunningSession(t *testing.T) {
	f := newFake()
	f.ignoreHangup = true
	ev := &events{}
	m := newManager(f, ev)
	info, err := m.Open(Options{Command: Command{Program: "sh"}, Kind: "docker.service", Target: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "app" || info.Cols != DefaultCols || info.Rows != DefaultRows {
		t.Fatalf("info %+v", info)
	}
	start := time.Now()
	m.CloseAll()
	if time.Since(start) > 3*time.Second {
		t.Fatal("close took too long")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hangups != 1 || f.kills != 1 {
		t.Fatalf("hangups %d kills %d", f.hangups, f.kills)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.exited) != 0 {
		t.Fatal("closing published an exit")
	}
}

func TestOpenErrors(t *testing.T) {
	m := &Manager{Start: func(Command) (Process, error) { return nil, errors.New("boom") }}
	if _, err := m.Open(Options{}); err == nil {
		t.Fatal("no program accepted")
	}
	if _, err := m.Open(Options{Command: Command{Program: "x"}}); err == nil || err.Error() != "boom" {
		t.Fatalf("start error %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatal("failed session listed")
	}
}

func TestNames(t *testing.T) {
	var fakes []*fakeProc
	m := &Manager{Start: func(Command) (Process, error) { f := newFake(); fakes = append(fakes, f); return f, nil }}
	a, _ := m.Open(Options{Command: Command{Program: "x"}})
	b, _ := m.Open(Options{Command: Command{Program: "x"}})
	c, _ := m.Open(Options{Command: Command{Program: "x"}, Name: "Mine"})
	if a.Name != "Local" || b.Name != "Local (2)" || c.Name != "Mine" {
		t.Fatalf("names %q %q %q", a.Name, b.Name, c.Name)
	}
	if l := m.List(); len(l) != 3 || l[0].ID != a.ID || l[2].ID != c.ID {
		t.Fatalf("order %+v", l)
	}
	m.CloseAll()
}

func TestScreenSnapshotAndLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	m := newManager(f, &events{})
	info, _ := m.Open(Options{Command: Command{Program: "x", Dir: dir, Cols: 30, Rows: 3}, Scrollback: 10})
	s, _ := m.Get(info.ID)
	_, _ = f.outW.Write([]byte("one\r\n\x1b[31m世 a.php:12\x1b[0m\r\ntwo\r\nthree"))
	eventually(t, "output", func() bool { return strings.Contains(s.ScreenText(), "three") })
	sc := s.Screen(ScreenOptions{Top: -1, Linker: &run.Linker{Exists: run.FileExists}})
	if sc.History != 1 || sc.Total != 4 || sc.Top != 1 || len(sc.Lines) != 3 || sc.Cursor.Row != 2 || sc.Cursor.Col != 5 || !sc.Running {
		t.Fatalf("screen %+v", sc)
	}
	l := sc.Lines[0]
	if l.Text != "世 a.php:12" || len(l.Links) != 1 {
		t.Fatalf("line %+v", l)
	}
	lk := l.Links[0]
	if lk.Col != 3 || lk.EndCol != 11 || lk.Line != 12 || lk.File != filepath.Join(dir, "a.php") {
		t.Fatalf("link %+v", lk)
	}
	if l.Spans[0].FG != "p1" || l.Spans[0].Text != "世" || l.Spans[0].Width != 2 {
		t.Fatalf("spans %+v", l.Spans)
	}
	all := s.Screen(ScreenOptions{Top: 0, Count: 100, NoSpans: true})
	if len(all.Lines) != 4 || all.Lines[0].Text != "one" || all.Lines[0].Spans != nil {
		t.Fatalf("all %+v", all.Lines)
	}
	if got := s.AllText(); got != "one\n世 a.php:12\ntwo\nthree" {
		t.Fatalf("all text %q", got)
	}
	if got := s.Text(1, 3, 2, 3); got != "a.php:12\ntwo" {
		t.Fatalf("range %q", got)
	}
	m.CloseAll()
}

func TestColors(t *testing.T) {
	cases := map[vt.Color]string{vt.Default: "", vt.Indexed(200): "p200", vt.RGB(1, 0xab, 255): "#01abff"}
	for c, want := range cases {
		if got := ColorString(c); got != want {
			t.Errorf("%v: %q want %q", c, got, want)
		}
	}
	if i, _, _, _, ok := ParseColor("p7"); !ok || i != 7 {
		t.Error("parse palette")
	}
	if i, r, g, b, ok := ParseColor("#01abff"); !ok || i != -1 || r != 1 || g != 0xab || b != 255 {
		t.Error("parse rgb")
	}
	for _, bad := range []string{"", "p", "p300", "#12", "#zzzzzz", "x"} {
		if _, _, _, _, ok := ParseColor(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestShellDefaults(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	none := func(string) (string, error) { return "", errors.New("no") }
	if got := DefaultShell("linux", env(map[string]string{"SHELL": "/usr/bin/fish"}), none); got != "/usr/bin/fish" {
		t.Fatal(got)
	}
	if got := DefaultShell("darwin", env(nil), func(p string) (string, error) {
		if p == "/bin/bash" {
			return p, nil
		}
		return "", errors.New("no")
	}); got != "/bin/bash" {
		t.Fatal(got)
	}
	if got := DefaultShell("linux", env(nil), none); got != "/bin/sh" {
		t.Fatal(got)
	}
	if got := DefaultShell("windows", env(map[string]string{"ComSpec": `C:\cmd.exe`}), none); got != `C:\cmd.exe` {
		t.Fatal(got)
	}
	if got := DefaultShell("windows", env(nil), func(p string) (string, error) { return `C:\` + p, nil }); got != `C:\pwsh.exe` {
		t.Fatal(got)
	}
	if got := DefaultShell("windows", env(nil), none); got != "cmd.exe" {
		t.Fatal(got)
	}
	if a := DefaultArgs("/bin/zsh"); len(a) != 1 || a[0] != "-l" {
		t.Fatal(a)
	}
	if a := DefaultArgs(`C:\powershell.exe`); len(a) != 1 || a[0] != "-NoLogo" {
		t.Fatal(a)
	}
	if a := DefaultArgs("/bin/sh"); a != nil {
		t.Fatal(a)
	}
	e := Env([]string{"HOME=/h", "PATH=/old", "TERM=dumb", "bogus"}, "/new", []string{"X=1", "HOME=/o"})
	got := strings.Join(e, " ")
	for _, want := range []string{"HOME=/o", "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=Tiegel", "X=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("env %q lacks %s", got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if !strings.Contains(got, "PATH=/new") || !strings.Contains(got, "LANG=en_US.UTF-8") {
			t.Errorf("env %q", got)
		}
		if e := Env([]string{"LC_ALL=C"}, "", nil); strings.Contains(strings.Join(e, " "), "LANG=") {
			t.Error("LANG added despite LC_ALL")
		}
	}
	if strings.Contains(got, "bogus") || strings.Contains(got, "TERM=dumb") {
		t.Errorf("env %q", got)
	}
}

func TestSettingSpecs(t *testing.T) {
	if registerErr != nil {
		t.Fatal(registerErr)
	}
	for _, s := range SettingSpecs() {
		if err := s.Validate(); err != nil {
			t.Error(err)
		}
	}
}

// TestRealShell runs /bin/sh in a real pseudo terminal.
func TestRealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	ev := &events{}
	m := &Manager{OnOutput: func(o Output) { ev.mu.Lock(); ev.out = append(ev.out, o); ev.mu.Unlock() }}
	dir := t.TempDir()
	info, err := m.Open(Options{Command: Command{Program: "/bin/sh", Dir: dir, Env: Env(os.Environ(), "", []string{"PS1=$ "}), Cols: 40, Rows: 10}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	s, _ := m.Get(info.ID)
	ctx := context.Background()
	if err := s.Input(ctx, []byte("printf 'A%sB\\n' \"$(tput cols 2>/dev/null || stty size)\"; pwd\r")); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	eventually(t, "shell output", func() bool {
		txt := s.AllText()
		return strings.Contains(txt, "A") && strings.Contains(txt, "B") && (strings.Contains(txt, dir) || strings.Contains(txt, real))
	})
	if err := s.Resize(50, 12); err != nil {
		t.Fatal(err)
	}
	if err := s.Input(ctx, []byte("stty size; exit 7\r")); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got, err := s.Wait(wctx)
	if err != nil {
		t.Fatalf("wait: %v; screen %q", err, s.AllText())
	}
	if got.ExitCode != 7 {
		t.Fatalf("exit %d", got.ExitCode)
	}
	if !strings.Contains(s.AllText(), "12 50") {
		t.Fatalf("stty size after resize missing: %q", s.AllText())
	}
}

// TestRealShellCloseKillsTree: closing a session ends the shell and a
// child it started.
func TestRealShellCloseKillsTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	m := &Manager{}
	pidFile := filepath.Join(t.TempDir(), "pid")
	info, err := m.Open(Options{Command: Command{Program: "/bin/sh", Env: Env(os.Environ(), "", nil), Cols: 40, Rows: 5}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get(info.ID)
	// A foreground job (its own process group under job control) that
	// ignores SIGHUP: only the kill of the terminal's foreground group
	// ends it.
	_ = s.Input(context.Background(), []byte("sh -c 'trap \"\" HUP; echo $$ > "+pidFile+"; exec sleep 60'\r"))
	var pid int
	eventually(t, "child pid", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && pid > 0
	})
	if !processAlive(pid) {
		t.Fatal("child not running")
	}
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "child killed", func() bool { return !processAlive(pid) })
}
