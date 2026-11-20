package api

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

func TestTerminalEmulateAndColors(t *testing.T) {
	a := newTestApp(t)
	var res terminal.EmulateResult
	call(t, a, "terminal.emulate", TerminalEmulateParams{Data: "a\x1b[1mb\x1b]11;?\x07", Cols: 10, Rows: 2}, &res)
	if res.Text != "ab" || res.Screen.Cols != 10 || !strings.Contains(res.Replies, "]11;rgb:") || len(res.Screen.Lines[0].Spans) != 2 {
		t.Fatalf("emulate %+v", res)
	}
	zero := 0
	call(t, a, "terminal.emulate", TerminalEmulateParams{DataBase64: base64.StdEncoding.EncodeToString([]byte("1\r\n2\r\n3\xe4")),
		Cols: 5, Rows: 1, Scrollback: -1, Top: &zero, Count: 5, NoSpans: true, Chunk: 1, ResizeCols: 4, Links: true}, &res)
	// The incomplete UTF-8 sequence at the end waits for more bytes.
	if res.Screen.History != 0 || res.Text != "3" || res.Screen.Cols != 4 {
		t.Fatalf("base64 %+v", res)
	}
	call(t, a, "terminal.emulate", TerminalEmulateParams{Data: "x", Scrollback: 5}, &res)
	for _, bad := range []TerminalEmulateParams{
		{Data: "x", DataBase64: "eA=="},
		{DataBase64: "!"},
		{Data: "x", Cols: 2000},
		{Data: "x", Chunk: -1},
	} {
		if err := callErr(a, "terminal.emulate", bad); ErrorCode(err) != CodeInvalidParams {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// Colors for queries.
	call(t, a, "terminal.colors", TerminalColorsParams{FG: "#010203", BG: "#ffffff", Cursor: "#00ff00", ANSI: []string{"#000000"}}, nil)
	for _, bad := range []TerminalColorsParams{
		{FG: "x", BG: "#000000"},
		{FG: "#000000", BG: "p1"},
		{FG: "#000000", BG: "#000000", Cursor: "red"},
		{FG: "#000000", BG: "#000000", ANSI: make([]string, 17)},
		{FG: "#000000", BG: "#000000", ANSI: []string{"#00"}},
	} {
		if err := callErr(a, "terminal.colors", bad); ErrorCode(err) != CodeInvalidParams {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestTerminalFindMouseClipboard(t *testing.T) {
	needSh(t)
	a := newTestApp(t)
	dir := t.TempDir()
	call(t, a, "project.open", map[string]string{"path": dir}, nil)
	if err := settings.Set(a.global, terminal.KeyClipboard, true); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var clip []byte
	unsub := a.Bus().Subscribe(TopicTerminalClipboard, func(e bus.Event) {
		mu.Lock()
		clip = append(clip, []byte(e.Payload.(TerminalClipboard).Text)...)
		mu.Unlock()
	})
	defer unsub()
	call(t, a, "terminal.colors", TerminalColorsParams{FG: "#010203", BG: "#fefefe"}, nil)
	var info terminal.Info
	call(t, a, "terminal.open", TerminalOpenParams{Shell: "/bin/sh", Cols: 60, Rows: 8, Env: []string{"PS1=$ "}}, &info)
	enc := base64.StdEncoding.EncodeToString([]byte("from-osc52"))
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Data: "printf 'needle\\033]52;c;" + enc + "\\007\\033[?1000;1006h'\r"}, nil)
	waitScreen(t, a, info.ID, "needle$")
	var found terminal.FindResult
	call(t, a, "terminal.find", TerminalFindParams{ID: info.ID, FindOptions: terminal.FindOptions{Query: "needle"}}, &found)
	if len(found.Matches) < 2 {
		t.Fatalf("find %+v", found)
	}
	if err := callErr(a, "terminal.find", TerminalFindParams{ID: info.ID, FindOptions: terminal.FindOptions{Query: "(", Regex: true}}); ErrorCode(err) != CodeInvalidParams {
		t.Fatal("bad regex", err)
	}
	if err := callErr(a, "terminal.find", TerminalFindParams{ID: "nope"}); ErrorCode(err) != CodeNotFound {
		t.Fatal("unknown session", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := string(clip)
		mu.Unlock()
		if got == "from-osc52" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clipboard %q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var in TerminalInputResult
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Mouse: &vt.Mouse{Action: vt.MousePress, Button: vt.MouseLeft}}, &in)
	if !in.Reported {
		t.Fatal("mouse not reported")
	}
	focus := true
	call(t, a, "terminal.input", TerminalInputParams{ID: info.ID, Focus: &focus}, &in)
	if err := callErr(a, "terminal.input", TerminalInputParams{ID: info.ID, Focus: &focus, Data: "x"}); ErrorCode(err) != CodeInvalidParams {
		t.Fatal("two inputs", err)
	}
	call(t, a, "terminal.close", TerminalIDParams{ID: info.ID}, nil)
}

func TestTerminalOpenURL(t *testing.T) {
	a := newTestApp(t)
	var got []string
	a.start = func(name string, args ...string) error { got = append([]string{name}, args...); return nil }
	var res TerminalURLResult
	call(t, a, "terminal.openURL", TerminalURLParams{URL: "https://example.com/x"}, &res)
	if len(got) < 2 || got[len(got)-1] != "https://example.com/x" || len(res.Command) != len(got) {
		t.Fatalf("started %q result %+v", got, res)
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://"} {
		if err := callErr(a, "terminal.openURL", TerminalURLParams{URL: bad}); ErrorCode(err) != CodeInvalidParams {
			t.Errorf("%s: %v", bad, err)
		}
	}
	a.start = func(string, ...string) error { return errors.New("no browser") }
	if err := callErr(a, "terminal.openURL", TerminalURLParams{URL: "http://h.test"}); ErrorCode(err) != CodeUnavailable {
		t.Fatal(err)
	}
}
