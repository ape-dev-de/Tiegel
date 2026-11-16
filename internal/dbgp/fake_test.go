package dbgp

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCmd is a command as received by the fake engine.
type fakeCmd struct {
	Name string
	TxID string
	Args map[string]string
	Data string // decoded
}

// parseFakeCmd parses an IDE command line like Xdebug does: values may be
// double-quoted with backslash escapes, data follows "--" base64-encoded.
func parseFakeCmd(line string) (fakeCmd, error) {
	var toks []string
	var data string
	i := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		if strings.HasPrefix(line[i:], "-- ") || line[i:] == "--" {
			raw := strings.TrimSpace(strings.TrimPrefix(line[i:], "--"))
			b, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				return fakeCmd{}, err
			}
			data = string(b)
			break
		}
		var tok strings.Builder
		if line[i] == '"' {
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				tok.WriteByte(line[i])
				i++
			}
			if i >= len(line) {
				return fakeCmd{}, fmt.Errorf("unterminated quote in %q", line)
			}
			i++
		} else {
			for i < len(line) && line[i] != ' ' {
				tok.WriteByte(line[i])
				i++
			}
		}
		toks = append(toks, tok.String())
	}
	if len(toks) == 0 {
		return fakeCmd{}, fmt.Errorf("empty command")
	}
	c := fakeCmd{Name: toks[0], Args: map[string]string{}, Data: data}
	for j := 1; j+1 < len(toks); j += 2 {
		c.Args[toks[j]] = toks[j+1]
	}
	c.TxID = c.Args["-i"]
	return c, nil
}

const (
	xmlHeader = `<?xml version="1.0" encoding="iso-8859-1"?>` + "\n"
	xmlNS     = `xmlns="urn:debugger_protocol_v1" xmlns:xdebug="https://xdebug.org/dbgp/xdebug"`
	scriptURI = "file:///var/www/html/index.php"
	libURI    = "file:///var/www/html/lib/greet.php"
)

type fakeBP struct {
	id, typ, state, file, function, exception, expr string
	line, hits                                      int
}

// fakeEngine simulates an Xdebug 3 engine debugging a small script:
// index.php lines 3..12 call greet() in lib/greet.php (lines 20..23) at line 8.
type fakeEngine struct {
	t    testing.TB
	conn net.Conn
	r    *bufio.Reader

	mu       sync.Mutex
	received []fakeCmd
	status   string
	file     string
	line     int
	inFunc   bool
	bps      map[string]*fakeBP
	nextBP   int
	closed   bool
	recvCond chan struct{}
}

func dialFake(t testing.TB, addr net.Addr, appid string) *fakeEngine {
	t.Helper()
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	f := &fakeEngine{
		t: t, conn: conn, r: bufio.NewReader(conn),
		status: "starting", file: scriptURI, line: 3,
		bps: map[string]*fakeBP{}, nextBP: 1, recvCond: make(chan struct{}, 1),
	}
	f.write(fmt.Sprintf(`<init %s fileuri="%s" language="PHP" xdebug:language_version="8.3.4" protocol_version="1.0" appid="%s" idekey="TIEGEL"><engine version="3.3.1"><![CDATA[Xdebug]]></engine><author><![CDATA[Derick Rethans]]></author><url><![CDATA[https://xdebug.org]]></url><copyright><![CDATA[Copyright (c) 2002-2024 by Derick Rethans]]></copyright></init>`, xmlNS, scriptURI, appid))
	go f.serve()
	t.Cleanup(f.close)
	return f
}

func (f *fakeEngine) write(x string) {
	msg := xmlHeader + x
	_, _ = fmt.Fprintf(f.conn, "%d\x00%s\x00", len(msg), msg)
}

func (f *fakeEngine) close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	_ = f.conn.Close()
}

func (f *fakeEngine) commands() []fakeCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCmd(nil), f.received...)
}

func (f *fakeEngine) lastCommand(name string) (fakeCmd, bool) {
	cmds := f.commands()
	for i := len(cmds) - 1; i >= 0; i-- {
		if cmds[i].Name == name {
			return cmds[i], true
		}
	}
	return fakeCmd{}, false
}

func (f *fakeEngine) serve() {
	for {
		line, err := f.r.ReadString(0)
		if err != nil {
			return
		}
		c, err := parseFakeCmd(strings.TrimSuffix(line, "\x00"))
		if err != nil {
			f.t.Errorf("fake: %v", err)
			return
		}
		f.mu.Lock()
		f.received = append(f.received, c)
		f.mu.Unlock()
		if !f.respond(c) {
			f.close()
			return
		}
	}
}

func (f *fakeEngine) resp(c fakeCmd, attrs, body string) {
	f.write(fmt.Sprintf(`<response %s command="%s" transaction_id="%s"%s>%s</response>`, xmlNS, c.Name, c.TxID, attrs, body))
}

func (f *fakeEngine) errResp(c fakeCmd, code int, msg string) {
	f.resp(c, "", fmt.Sprintf(`<error code="%d"><message><![CDATA[%s]]></message></error>`, code, msg))
}

func (f *fakeEngine) breakResp(c fakeCmd) {
	f.resp(c, ` status="break" reason="ok"`, fmt.Sprintf(`<xdebug:message filename="%s" lineno="%d"></xdebug:message>`, f.file, f.line))
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func bpXML(bp *fakeBP, resolved string) string {
	attrs := fmt.Sprintf(` type="%s" state="%s" hit_count="%d" hit_value="0" id="%s"`, bp.typ, bp.state, bp.hits, bp.id)
	if resolved != "" {
		attrs += ` resolved="` + resolved + `"`
	}
	if bp.file != "" {
		attrs += fmt.Sprintf(` filename="%s" lineno="%d"`, bp.file, bp.line)
	}
	if bp.function != "" {
		attrs += ` function="` + bp.function + `"`
	}
	if bp.exception != "" {
		attrs += ` exception="` + bp.exception + `"`
	}
	body := ""
	if bp.expr != "" {
		body = `<expression encoding="base64"><![CDATA[` + b64(bp.expr) + `]]></expression>`
	}
	return `<breakpoint` + attrs + `>` + body + `</breakpoint>`
}

// respond answers one command; it returns false to close the connection.
func (f *fakeEngine) respond(c fakeCmd) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch c.Name {
	case "feature_set":
		ok := "1"
		if c.Args["-n"] == "no_such_feature" {
			ok = "0"
		}
		f.resp(c, fmt.Sprintf(` feature="%s" success="%s"`, c.Args["-n"], ok), "")
	case "status":
		f.resp(c, fmt.Sprintf(` status="%s" reason="ok"`, f.status), "")
	case "breakpoint_set":
		id := strconv.Itoa(10000 + f.nextBP)
		f.nextBP++
		bp := &fakeBP{id: id, typ: c.Args["-t"], state: c.Args["-s"], file: c.Args["-f"],
			function: c.Args["-m"], exception: c.Args["-x"], expr: c.Data}
		bp.line, _ = strconv.Atoi(c.Args["-n"])
		f.bps[id] = bp
		resolved := "unresolved"
		if bp.file != "" && bp.line < 100 {
			resolved = "resolved"
			// Xdebug 3 sends the notification before the response.
			f.write(fmt.Sprintf(`<notify %s name="breakpoint_resolved">%s</notify>`, xmlNS, bpXML(bp, "resolved")))
		}
		f.resp(c, fmt.Sprintf(` id="%s" resolved="%s"`, id, resolved), "")
	case "breakpoint_list":
		ids := make([]string, 0, len(f.bps))
		for id := range f.bps {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var b strings.Builder
		for _, id := range ids {
			b.WriteString(bpXML(f.bps[id], "resolved"))
		}
		f.resp(c, "", b.String())
	case "breakpoint_update":
		bp, ok := f.bps[c.Args["-d"]]
		if !ok {
			f.errResp(c, 205, "no such breakpoint")
			break
		}
		if s := c.Args["-s"]; s != "" {
			bp.state = s
		}
		f.resp(c, "", "")
	case "breakpoint_remove":
		if _, ok := f.bps[c.Args["-d"]]; !ok {
			f.errResp(c, 205, "no such breakpoint")
			break
		}
		delete(f.bps, c.Args["-d"])
		f.resp(c, "", "")
	case "run":
		// Emit some program output and a notice first.
		f.write(fmt.Sprintf(`<stream %s type="stdout" encoding="base64"><![CDATA[%s]]></stream>`, xmlNS, b64("Hello")))
		f.write(fmt.Sprintf(`<notify %s name="error"><xdebug:message filename="%s" lineno="4" type="Warning" code="2"><![CDATA[Undefined variable $x]]></xdebug:message></notify>`, xmlNS, scriptURI))
		next := 0
		hasException := false
		for _, bp := range f.bps {
			if bp.state != "enabled" {
				continue
			}
			if bp.typ == "exception" {
				hasException = true
			}
			if bp.file == scriptURI && bp.line > f.line && (next == 0 || bp.line < next) {
				next = bp.line
			}
		}
		switch {
		case next > 0:
			f.file, f.line, f.inFunc, f.status = scriptURI, next, false, "break"
			f.breakResp(c)
		case hasException:
			f.file, f.line, f.inFunc, f.status = libURI, 22, true, "break"
			f.resp(c, ` status="break" reason="ok"`, fmt.Sprintf(`<xdebug:message filename="%s" lineno="22" exception="RuntimeException" code="42"><![CDATA[boom]]></xdebug:message>`, libURI))
		default:
			f.status = "stopping"
			f.resp(c, ` status="stopping" reason="ok"`, "")
		}
	case "step_over":
		f.line++
		f.status = "break"
		f.breakResp(c)
	case "step_into":
		f.file, f.line, f.inFunc, f.status = libURI, 21, true, "break"
		f.breakResp(c)
	case "step_out":
		f.file, f.line, f.inFunc, f.status = scriptURI, 9, false, "break"
		f.breakResp(c)
	case "stack_get":
		var b strings.Builder
		if f.inFunc {
			fmt.Fprintf(&b, `<stack where="greet" level="0" type="file" filename="%s" lineno="%d"></stack>`, libURI, f.line)
			fmt.Fprintf(&b, `<stack where="{main}" level="1" type="file" filename="%s" lineno="8"></stack>`, scriptURI)
		} else {
			fmt.Fprintf(&b, `<stack where="{main}" level="0" type="file" filename="%s" lineno="%d"></stack>`, scriptURI, f.line)
		}
		f.resp(c, "", b.String())
	case "context_names":
		f.resp(c, "", `<context name="Locals" id="0"></context><context name="Superglobals" id="1"></context><context name="User defined constants" id="2"></context>`)
	case "context_get":
		if c.Args["-c"] != "0" {
			f.resp(c, ` context="`+c.Args["-c"]+`"`, `<property name="$_GET" fullname="$_GET" type="array" children="0" numchildren="0" page="0" pagesize="100"></property>`)
			break
		}
		f.resp(c, ` context="0"`, `<property name="$name" fullname="$name" type="string" size="5" encoding="base64"><![CDATA[`+b64("World")+`]]></property>`+
			`<property name="$count" fullname="$count" type="int"><![CDATA[3]]></property>`+
			`<property name="$list" fullname="$list" type="array" children="1" numchildren="3" page="0" pagesize="2"><property name="0" fullname="$list[0]" type="int"><![CDATA[1]]></property><property name="1" fullname="$list[1]" type="int"><![CDATA[2]]></property></property>`+
			`<property type="object" children="1" numchildren="1" page="0" pagesize="100"><name encoding="base64"><![CDATA[`+b64("$obj")+`]]></name><fullname encoding="base64"><![CDATA[`+b64("$obj")+`]]></fullname><classname encoding="base64"><![CDATA[`+b64(`App\Ünïcode`)+`]]></classname></property>`+
			`<property name="$unset" fullname="$unset" type="uninitialized"></property>`)
	case "property_get":
		switch c.Args["-n"] {
		case "$list":
			if c.Args["-p"] == "1" {
				f.resp(c, "", `<property name="$list" fullname="$list" type="array" children="1" numchildren="3" page="1" pagesize="2"><property name="2" fullname="$list[2]" type="int"><![CDATA[3]]></property></property>`)
			} else {
				f.resp(c, "", `<property name="$list" fullname="$list" type="array" children="1" numchildren="3" page="0" pagesize="2"><property name="0" fullname="$list[0]" type="int"><![CDATA[1]]></property><property name="1" fullname="$list[1]" type="int"><![CDATA[2]]></property></property>`)
			}
		case "$obj":
			f.resp(c, "", `<property name="$obj" fullname="$obj" type="object" classname="App\Greeter" children="1" numchildren="1" page="0" pagesize="100"><property name="greeting" fullname="$obj-&gt;greeting" facet="private" type="string" size="2" encoding="base64"><![CDATA[`+b64("Hi")+`]]></property></property>`)
		case `$map["a key"]`:
			f.resp(c, "", `<property name="a key" fullname="$map[&quot;a key&quot;]" type="bool"><![CDATA[1]]></property>`)
		case "$empty":
			f.resp(c, "", "")
		default:
			f.errResp(c, 300, "can not get property")
		}
	case "property_value":
		f.resp(c, ` type="string" size="5" encoding="base64"`, `<![CDATA[`+b64("World")+`]]>`)
	case "eval":
		switch c.Data {
		case "sleep":
			// never answered
		case "$count + 1":
			f.resp(c, "", `<property type="int"><![CDATA[4]]></property>`)
		case "noop()":
			f.resp(c, "", "")
		default:
			f.errResp(c, 206, "error evaluating code")
		}
	case "break":
		f.resp(c, ` success="1"`, "")
	case "stop":
		f.status = "stopped"
		f.resp(c, ` status="stopped" reason="ok"`, "")
		return false
	case "detach":
		f.status = "stopping"
		f.resp(c, ` status="stopping" reason="ok"`, "")
		return false
	default:
		f.errResp(c, 4, "unimplemented command")
	}
	return true
}

// recorder collects server events.
type recorder struct {
	started chan *Session
	ended   chan error

	mu       sync.Mutex
	statuses []StatusEvent
	resolved []Breakpoint
	notes    []Notification
	outputs  []Stream
	order    []string
}

func newRecorder() *recorder {
	return &recorder{started: make(chan *Session, 16), ended: make(chan error, 16)}
}

func (r *recorder) add(kind string) {
	r.order = append(r.order, kind)
}

func (r *recorder) events() Events {
	return Events{
		SessionStarted: func(s *Session) {
			r.mu.Lock()
			r.add("started")
			r.mu.Unlock()
			r.started <- s
		},
		SessionEnded: func(s *Session, err error) {
			r.mu.Lock()
			r.add("ended")
			r.mu.Unlock()
			r.ended <- err
		},
		StatusChanged: func(s *Session, ev StatusEvent) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.add("status:" + string(ev.Info.Status))
			r.statuses = append(r.statuses, ev)
		},
		BreakpointResolved: func(s *Session, bp Breakpoint) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.add("resolved")
			r.resolved = append(r.resolved, bp)
		},
		Notification: func(s *Session, n Notification) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.add("notify")
			r.notes = append(r.notes, n)
		},
		Output: func(s *Session, st Stream) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.add("output")
			r.outputs = append(r.outputs, st)
		},
	}
}

func (r *recorder) waitStarted(t testing.TB) *Session {
	t.Helper()
	select {
	case s := <-r.started:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SessionStarted")
		return nil
	}
}

func (r *recorder) waitEnded(t testing.TB) error {
	t.Helper()
	select {
	case err := <-r.ended:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SessionEnded")
		return nil
	}
}

// waitFor polls cond until it is true or the timeout expires.
func waitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
