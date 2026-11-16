package debugxdebug

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
)

const xmlNS = `xmlns="urn:debugger_protocol_v1" xmlns:xdebug="https://xdebug.org/dbgp/xdebug"`

type fakeCmd struct {
	Name string
	Args map[string]string
	Data string
}

// parseCmd parses an IDE command like Xdebug: quoted values with backslash
// escapes, base64 data after "--".
func parseCmd(line string) fakeCmd {
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
			b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line[i:], "--")))
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
			i++
		} else {
			for i < len(line) && line[i] != ' ' {
				tok.WriteByte(line[i])
				i++
			}
		}
		toks = append(toks, tok.String())
	}
	c := fakeCmd{Name: toks[0], Args: map[string]string{}, Data: data}
	for j := 1; j+1 < len(toks); j += 2 {
		c.Args[toks[j]] = toks[j+1]
	}
	return c
}

type fakeBP struct {
	id, file, state, expr string
	line                  int
}

// fakeEngine is a small DBGp engine running a script at remote URI script.
type fakeEngine struct {
	t      testing.TB
	conn   net.Conn
	script string

	mu     sync.Mutex
	cmds   []fakeCmd
	bps    map[string]*fakeBP
	nextBP int
	line   int
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func dialEngine(t testing.TB, addr, script string) *fakeEngine {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngine{t: t, conn: conn, script: script, bps: map[string]*fakeBP{}}
	t.Cleanup(func() { _ = conn.Close() })
	f.write(fmt.Sprintf(`<init %s fileuri="%s" language="PHP" xdebug:language_version="8.3.4" protocol_version="1.0" appid="1" idekey="TIEGEL"><engine version="3.3.1"><![CDATA[Xdebug]]></engine></init>`, xmlNS, script))
	go f.serve()
	return f
}

func (f *fakeEngine) write(x string) {
	msg := `<?xml version="1.0" encoding="iso-8859-1"?>` + "\n" + x
	_, _ = fmt.Fprintf(f.conn, "%d\x00%s\x00", len(msg), msg)
}

func (f *fakeEngine) commands(name string) []fakeCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCmd
	for _, c := range f.cmds {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// breakpoints returns "file:line:state:expr" of the engine's breakpoints.
func (f *fakeEngine) breakpoints() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, bp := range f.bps {
		out = append(out, fmt.Sprintf("%s:%d:%s:%s", bp.file, bp.line, bp.state, bp.expr))
	}
	sort.Strings(out)
	return out
}

func (f *fakeEngine) serve() {
	r := bufio.NewReader(f.conn)
	for {
		line, err := r.ReadString(0)
		if err != nil {
			return
		}
		c := parseCmd(strings.TrimSuffix(line, "\x00"))
		f.mu.Lock()
		f.cmds = append(f.cmds, c)
		f.mu.Unlock()
		if !f.respond(c) {
			_ = f.conn.Close()
			return
		}
	}
}

func (f *fakeEngine) resp(c fakeCmd, attrs, body string) {
	f.write(fmt.Sprintf(`<response %s command="%s" transaction_id="%s"%s>%s</response>`, xmlNS, c.Name, c.Args["-i"], attrs, body))
}

func (f *fakeEngine) errResp(c fakeCmd, code int, msg string) {
	f.resp(c, "", fmt.Sprintf(`<error code="%d"><message><![CDATA[%s]]></message></error>`, code, msg))
}

func (f *fakeEngine) breakAt(c fakeCmd, line int) {
	f.line = line
	f.resp(c, ` status="break" reason="ok"`, fmt.Sprintf(`<xdebug:message filename="%s" lineno="%d"></xdebug:message>`, f.script, line))
}

func (f *fakeEngine) respond(c fakeCmd) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch c.Name {
	case "feature_set":
		f.resp(c, ` success="1"`, "")
	case "breakpoint_set":
		f.nextBP++
		id := strconv.Itoa(100 + f.nextBP)
		bp := &fakeBP{id: id, file: c.Args["-f"], state: c.Args["-s"], expr: c.Data}
		bp.line, _ = strconv.Atoi(c.Args["-n"])
		f.bps[id] = bp
		f.resp(c, fmt.Sprintf(` id="%s" resolved="resolved" state="%s"`, id, bp.state), "")
	case "breakpoint_update":
		bp, ok := f.bps[c.Args["-d"]]
		if !ok {
			f.errResp(c, 205, "no such breakpoint")
			break
		}
		bp.state = c.Args["-s"]
		f.resp(c, "", "")
	case "breakpoint_remove":
		if _, ok := f.bps[c.Args["-d"]]; !ok {
			f.errResp(c, 205, "no such breakpoint")
			break
		}
		delete(f.bps, c.Args["-d"])
		f.resp(c, "", "")
	case "run":
		f.write(fmt.Sprintf(`<stream %s type="stdout" encoding="base64"><![CDATA[%s]]></stream>`, xmlNS, b64("Hi")))
		next := 0
		for _, bp := range f.bps {
			if bp.state == "enabled" && bp.file == f.script && bp.line > f.line && (next == 0 || bp.line < next) {
				next = bp.line
			}
		}
		if next == 0 {
			f.resp(c, ` status="stopping" reason="ok"`, "")
			break
		}
		f.breakAt(c, next)
	case "step_into":
		f.breakAt(c, 20)
	case "step_over":
		f.breakAt(c, f.line+1)
	case "step_out":
		f.breakAt(c, 9)
	case "stack_get":
		f.resp(c, "", fmt.Sprintf(`<stack where="{main}" level="0" type="file" filename="%s" lineno="%d"></stack>`, f.script, f.line))
	case "context_names":
		f.resp(c, "", `<context name="Locals" id="0"></context><context name="Superglobals" id="1"></context>`)
	case "context_get":
		f.resp(c, ` context="0"`, `<property name="$count" fullname="$count" type="int"><![CDATA[3]]></property>`+
			`<property name="$list" fullname="$list" type="array" children="1" numchildren="3" page="0" pagesize="2"><property name="0" fullname="$list[0]" type="int"><![CDATA[1]]></property><property name="1" fullname="$list[1]" type="int"><![CDATA[2]]></property></property>`)
	case "property_get":
		switch c.Args["-n"] {
		case "$list":
			if c.Args["-p"] == "1" {
				f.resp(c, "", `<property name="$list" fullname="$list" type="array" children="1" numchildren="3" page="1" pagesize="2"><property name="2" fullname="$list[2]" type="int"><![CDATA[3]]></property></property>`)
			} else {
				f.resp(c, "", `<property name="$list" fullname="$list" type="array" children="1" numchildren="3" page="0" pagesize="2"><property name="0" fullname="$list[0]" type="int"><![CDATA[1]]></property><property name="1" fullname="$list[1]" type="int"><![CDATA[2]]></property></property>`)
			}
		default:
			f.errResp(c, 300, "can not get property")
		}
	case "eval":
		if c.Data == "$count + 1" {
			f.resp(c, "", `<property type="int"><![CDATA[4]]></property>`)
		} else {
			f.errResp(c, 206, "error evaluating code")
		}
	case "stop":
		f.resp(c, ` status="stopped" reason="ok"`, "")
		return false
	case "detach":
		f.resp(c, ` status="stopping" reason="ok"`, "")
		return false
	default:
		f.resp(c, "", "")
	}
	return true
}
