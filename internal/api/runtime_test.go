package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// interpOptions restricts interpreter discovery to dirs (none if nil).
func interpOptions(dirs []string) interp.DiscoverOptions {
	if dirs == nil {
		dirs = []string{}
	}
	return interp.DiscoverOptions{Dirs: dirs, Timeout: 5 * time.Second}
}

// fakePHP writes a shell script that behaves like `php` for the queries
// the interp package runs.
func fakePHP(t *testing.T, dir, name, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake php scripts need a POSIX shell")
	}
	script := `#!/bin/sh
case "$1" in
  -m) printf '[PHP Modules]\nCore\njson\nxdebug\n' ;;
  -r)
    case "$2" in
      *PHP_VERSION*) printf '` + version + `' ;;
      *ini_get*) printf 'xdebug.mode=debug\nxdebug.client_port=9003\n' ;;
      *) exit 3 ;;
    esac ;;
  *) exit 2 ;;
esac
`
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInterpreters(t *testing.T) {
	bin := t.TempDir()
	php := fakePHP(t, bin, "php", "8.3.4")
	other := fakePHP(t, t.TempDir(), "php8.2", "8.2.1")
	a := newTestApp(t, func(a *App) error { a.opts.Interp = interpOptions([]string{bin}); return nil })

	wantCode(t, a, "interp.version", InterpVersionParams{}, CodeInvalidParams)
	wantCode(t, a, "interp.select", InterpSelectParams{}, CodeInvalidParams)
	wantCode(t, a, "interp.select", InterpSelectParams{ID: "nope"}, CodeNotFound)

	var list []InterpreterInfo
	call(t, a, "interp.list", nil, &list)
	if len(list) != 1 || list[0].Version != "8.3.4" || list[0].Kind != "local" || list[0].Selected {
		t.Fatalf("list %+v", list)
	}
	id := list[0].ID
	if !strings.HasSuffix(id, "/php") {
		t.Fatalf("id %s", id)
	}
	var sel InterpreterInfo
	call(t, a, "interp.select", InterpSelectParams{ID: id}, &sel)
	if !sel.Selected || sel.ID != id {
		t.Fatalf("select %+v", sel)
	}
	call(t, a, "interp.list", nil, &list)
	if !list[0].Selected {
		t.Fatal("selection not persisted (global settings)")
	}
	var v InterpVersionResult
	call(t, a, "interp.version", InterpVersionParams{Modules: true, Ini: true}, &v)
	if v.Version != "8.3.4" || len(v.Modules) != 3 || v.Ini["xdebug.mode"] != "debug" {
		t.Fatalf("version %+v", v)
	}
	call(t, a, "interp.version", InterpVersionParams{ID: "local:" + other}, &v)
	if v.Version != "8.2.1" {
		t.Fatalf("ad-hoc local %+v", v)
	}
	wantCode(t, a, "interp.version", InterpVersionParams{ID: "local:" + filepath.Join(bin, "missing")}, CodeNotFound)

	broken := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a, "interp.version", InterpVersionParams{ID: "local:" + broken}, CodeUnavailable)
	_ = php

	// With a project, compose services are listed too and the selection is
	// stored per project.
	root := openProject(t, a, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n  db:\n    image: mariadb\n",
	})
	call(t, a, "interp.list", nil, &list)
	if len(list) != 2 || list[1].Kind != "compose" || list[1].Service != "app" || list[1].Score <= 0 || list[0].Selected {
		t.Fatalf("project list %+v", list)
	}
	call(t, a, "interp.select", InterpSelectParams{ID: list[1].ID}, &sel)
	var info ProjectInfo
	call(t, a, "project.info", nil, &info)
	if info.Interpreter != list[1].ID || info.Root != root {
		t.Fatalf("project interpreter %+v", info)
	}
	noCompose := false
	call(t, a, "interp.list", InterpListParams{Compose: &noCompose}, &list)
	if len(list) != 1 {
		t.Fatalf("compose disabled %+v", list)
	}
	// Selecting from an empty cache lists first.
	a.mu.Lock()
	a.interps = map[string]interp.Interpreter{}
	a.mu.Unlock()
	call(t, a, "interp.select", InterpSelectParams{ID: id}, &sel)
}

func TestComposeDetect(t *testing.T) {
	a := newTestApp(t)
	wantCode(t, a, "compose.detect", nil, CodeNoProject)
	var res ComposeResult
	call(t, a, "compose.detect", ComposeParams{Dir: "../compose/testdata/sail"}, &res)
	if !res.Found || len(res.Candidates) == 0 || res.Candidates[0].Service != "laravel.test" || len(res.Candidates[0].Reasons) == 0 || len(res.Candidates[0].Mappings) == 0 {
		t.Fatalf("sail %+v", res)
	}
	openProject(t, a, map[string]string{"README.md": "x"})
	call(t, a, "compose.detect", nil, &res)
	if res.Found || res.Candidates == nil {
		t.Fatalf("no compose %+v", res)
	}
	root := openProject(t, a, map[string]string{"compose.yaml": "services: [broken"})
	wantCode(t, a, "compose.detect", ComposeParams{Dir: root}, CodeFailed)
	openProject(t, a, map[string]string{"compose.yaml": "services:\n  php:\n    image: php:8.3-fpm\n"})
	call(t, a, "compose.detect", nil, &res)
	if len(res.Candidates) != 1 || len(res.Candidates[0].XdebugHints) == 0 || res.Candidates[0].XdebugHints[0].Code != "no-host-gateway" {
		t.Fatalf("expected xdebug hints on linux: %+v", res)
	}
	openProject(t, a, map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx\n"})
	call(t, a, "compose.detect", nil, &res)
	if !res.Found || len(res.Candidates) != 0 || len(res.Services) != 1 {
		t.Fatalf("no php %+v", res)
	}
}

func TestLSPServers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executables")
	}
	a := newTestApp(t)
	var list []LSPServerInfo
	call(t, a, "lsp.servers", nil, &list)
	if len(list) != 0 {
		t.Fatalf("expected none, got %+v", list)
	}
	root := openProject(t, a, map[string]string{})
	bin := filepath.Join(root, "vendor", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"phpactor", "intelephense"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	call(t, a, "lsp.servers", nil, &list)
	if len(list) != 1 || list[0].ID != "phpactor" || list[0].Path != filepath.Join(bin, "phpactor") {
		t.Fatalf("project servers %+v", list)
	}
	call(t, a, "lsp.servers", LSPServersParams{All: true}, &list)
	if len(list) != 2 || !list[1].Proprietary {
		t.Fatalf("all servers %+v", list)
	}
	call(t, a, "lsp.known", nil, &list)
	if len(list) < 3 {
		t.Fatalf("known %+v", list)
	}
}

type fakeModule struct {
	id   string
	deps []string
}

func (m fakeModule) ID() string                 { return m.id }
func (m fakeModule) DependsOn() []string        { return m.deps }
func (m fakeModule) Start(module.Context) error { return nil }
func (m fakeModule) Stop() error                { return nil }

func TestModules(t *testing.T) {
	a := newTestApp(t)
	var list []ModuleInfo
	call(t, a, "modules.list", nil, &list)
	if len(list) != 0 {
		t.Fatal("no modules expected")
	}
	if err := a.Modules().Register(fakeModule{id: "php-runtime"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Modules().Register(fakeModule{id: "debug-xdebug", deps: []string{"php-runtime"}}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a, "modules.enable", ModuleParams{ID: "debug-xdebug"}, CodeFailed)
	var m ModuleInfo
	call(t, a, "modules.enable", ModuleParams{ID: "debug-xdebug", WithDependencies: true}, &m)
	if !m.Enabled || m.ID != "debug-xdebug" {
		t.Fatalf("enable %+v", m)
	}
	wantCode(t, a, "modules.disable", ModuleParams{ID: "php-runtime"}, CodeFailed)
	call(t, a, "modules.disable", ModuleParams{ID: "debug-xdebug"}, &m)
	if m.Enabled {
		t.Fatal("still enabled")
	}
	call(t, a, "modules.list", nil, &list)
	if len(list) != 2 || list[1].DependsOn[0] != "php-runtime" || !list[0].Enabled {
		t.Fatalf("list %+v", list)
	}
	wantCode(t, a, "modules.enable", ModuleParams{ID: "nope"}, CodeNotFound)
	if _, err := a.moduleResult("nope"); ErrorCode(err) != CodeNotFound {
		t.Fatal(err)
	}
}

// fakeXdebug is a minimal DBGp engine: it sends an init packet and answers
// the commands used by the debug namespace.
type fakeXdebug struct {
	conn net.Conn
	mu   sync.Mutex
}

const dbgpNS = `xmlns="urn:debugger_protocol_v1" xmlns:xdebug="https://xdebug.org/dbgp/xdebug"`

func dialXdebug(t *testing.T, addr string) *fakeXdebug {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeXdebug{conn: conn}
	f.write(fmt.Sprintf(`<init %s fileuri="file:///app/index.php" language="PHP" xdebug:language_version="8.3.4" protocol_version="1.0" appid="1" idekey="TIEGEL"><engine version="3.3.1"><![CDATA[Xdebug]]></engine></init>`, dbgpNS))
	go f.serve()
	return f
}

func (f *fakeXdebug) write(x string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msg := `<?xml version="1.0" encoding="iso-8859-1"?>` + "\n" + x
	_, _ = fmt.Fprintf(f.conn, "%d\x00%s\x00", len(msg), msg)
}

func (f *fakeXdebug) serve() {
	r := bufio.NewReader(f.conn)
	for {
		line, err := r.ReadString(0)
		if err != nil {
			return
		}
		fields := strings.Fields(strings.TrimSuffix(line, "\x00"))
		name, tx := fields[0], ""
		for i := 1; i+1 < len(fields); i++ {
			if fields[i] == "-i" {
				tx = fields[i+1]
			}
		}
		resp := func(attrs, body string) {
			f.write(fmt.Sprintf(`<response %s command="%s" transaction_id="%s"%s>%s</response>`, dbgpNS, name, tx, attrs, body))
		}
		brk := `<xdebug:message filename="file:///app/index.php" lineno="3"></xdebug:message>`
		switch name {
		case "feature_set":
			resp(` success="1"`, "")
		case "run", "step_into", "step_over", "step_out":
			f.write(fmt.Sprintf(`<stream %s type="stdout" encoding="base64"><![CDATA[SGk=]]></stream>`, dbgpNS))
			resp(` status="break" reason="ok"`, brk)
		case "stack_get":
			resp("", `<stack where="{main}" level="0" type="file" filename="file:///app/index.php" lineno="3"></stack>`)
		case "breakpoint_set":
			resp(` id="7" resolved="resolved"`, "")
		case "eval":
			resp("", `<error code="206"><message><![CDATA[error evaluating code]]></message></error>`)
		case "stop":
			resp(` status="stopped" reason="ok"`, "")
			_ = f.conn.Close()
			return
		default:
			resp("", "")
		}
	}
}

func TestDebug(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"index.php": "<?php\n"})
	var mu sync.Mutex
	topics := map[string]int{}
	sess := a.NewSession(func(e Event) {
		mu.Lock()
		topics[e.Topic]++
		mu.Unlock()
	})
	defer sess.Close()
	sess.Subscribe("debug.*")
	waitTopic := func(topic string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := topics[topic]
			mu.Unlock()
			if n > 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("no %s event (got %v)", topic, topics)
	}

	wantCode(t, a, "debug.continue", DebugContinueParams{Action: "run"}, CodeUnavailable)
	var sessions []DebugSessionInfo
	call(t, a, "debug.sessions", nil, &sessions)
	if len(sessions) != 0 {
		t.Fatal("no sessions expected")
	}

	var lr DebugListenResult
	call(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0", Mappings: []MappingInfo{{Host: root, Remote: "/app"}}}, &lr)
	if !lr.Listening || lr.Addr == "" {
		t.Fatalf("listen %+v", lr)
	}
	var again DebugListenResult
	call(t, a, "debug.listen", DebugListenParams{}, &again)
	if again.Addr != lr.Addr {
		t.Fatal("second listen should report the running listener")
	}
	waitTopic(TopicDebugListen)
	wantCode(t, a, "debug.continue", DebugContinueParams{Action: "run"}, CodeInvalidParams) // no session yet

	f := dialXdebug(t, lr.Addr)
	defer f.conn.Close()
	waitTopic(TopicDebugStarted)
	call(t, a, "debug.sessions", nil, &sessions)
	if len(sessions) != 1 || sessions[0].File != filepath.Join(root, "index.php") || sessions[0].IDEKey != "TIEGEL" || sessions[0].Engine != "Xdebug" {
		t.Fatalf("sessions %+v", sessions)
	}
	id := sessions[0].ID

	var bp dbgp.Breakpoint
	call(t, a, "debug.setBreakpoint", DebugBreakpointParams{File: "index.php", Line: 3}, &bp)
	if bp.ID != "7" || !bp.Resolved {
		t.Fatalf("breakpoint %+v", bp)
	}
	wantCode(t, a, "debug.setBreakpoint", DebugBreakpointParams{File: "index.php"}, CodeInvalidParams)
	wantCode(t, a, "debug.setBreakpoint", DebugBreakpointParams{Line: 1}, CodeInvalidParams)

	var st DebugStatusEvent
	for _, action := range []string{"run", "stepInto", "stepOver", "stepOut"} {
		call(t, a, "debug.continue", DebugContinueParams{Session: id, Action: action}, &st)
		if st.Status != "break" || st.Line != 3 || st.File != filepath.Join(root, "index.php") {
			t.Fatalf("%s: %+v", action, st)
		}
	}
	waitTopic(TopicDebugStatus)
	waitTopic(TopicDebugOutput)
	wantCode(t, a, "debug.continue", DebugContinueParams{Session: id, Action: "jump"}, CodeInvalidParams)
	wantCode(t, a, "debug.continue", DebugContinueParams{Session: "zzz", Action: "run"}, CodeNotFound)

	var frames []dbgp.StackFrame
	call(t, a, "debug.stack", DebugSessionParams{Session: id}, &frames)
	if len(frames) != 1 || frames[0].Line != 3 || frames[0].File != filepath.Join(root, "index.php") {
		t.Fatalf("stack %+v", frames)
	}
	wantCode(t, a, "debug.eval", DebugEvalParams{Session: id, Expr: "1+"}, CodeFailed)

	call(t, a, "debug.continue", DebugContinueParams{Session: id, Action: "stop"}, &st)
	if st.Status != "stopped" {
		t.Fatalf("stop %+v", st)
	}
	waitTopic(TopicDebugEnded)
	wantCode(t, a, "debug.stack", DebugSessionParams{Session: id}, CodeNotFound)
	wantCode(t, a, "debug.eval", DebugEvalParams{Session: id}, CodeNotFound)
	wantCode(t, a, "debug.setBreakpoint", DebugBreakpointParams{Session: id, File: "x", Line: 1}, CodeNotFound)

	call(t, a, "debug.stop", nil, &lr)
	if lr.Listening {
		t.Fatal("still listening")
	}
	waitTopic(TopicDebugStopped)
	call(t, a, "debug.stop", nil, &lr) // idempotent

	// Detach on a fresh session.
	call(t, a, "debug.listen", DebugListenParams{Addr: "127.0.0.1:0"}, &lr)
	f2 := dialXdebug(t, lr.Addr)
	defer f2.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		call(t, a, "debug.sessions", nil, &sessions)
		if len(sessions) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	call(t, a, "debug.continue", DebugContinueParams{Action: "detach"}, &st)

	// A busy port is reported as unavailable.
	b := newTestApp(t)
	wantCode(t, b, "debug.listen", DebugListenParams{Addr: lr.Addr}, CodeUnavailable)

	if err := debugErr(errors.New("x")); ErrorCode(err) != CodeFailed {
		t.Fatal(err)
	}
	if err := debugErr(dbgp.ErrSessionClosed); ErrorCode(err) != CodeNotFound {
		t.Fatal(err)
	}
	_ = context.Background
}
