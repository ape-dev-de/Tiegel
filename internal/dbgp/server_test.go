package dbgp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const hostRoot = "/Users/me/project"

func startServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	srv := NewServer(cfg)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return srv
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestSessionFullFlow(t *testing.T) {
	rec := newRecorder()
	srv := startServer(t, Config{
		Mapper: NewPrefixMapper(Mapping{Host: hostRoot, Remote: "/var/www/html"}),
		Events: rec.events(),
	})
	fake := dialFake(t, srv.Addr(), "4711")
	s := rec.waitStarted(t)
	ctx := testCtx(t)

	info := s.Info()
	if info.File != hostRoot+"/index.php" || info.FileURI != scriptURI {
		t.Errorf("init file = %q (%q)", info.File, info.FileURI)
	}
	if info.IDEKey != "TIEGEL" || info.AppID != "4711" || info.Language != "PHP" ||
		info.LanguageVersion != "8.3.4" || info.Engine != "Xdebug" || info.EngineVersion != "3.3.1" || info.ProtocolVersion != "1.0" {
		t.Errorf("init info = %+v", info)
	}
	if s.ID() != "1" || s.RemoteAddr() == nil {
		t.Errorf("ID=%q addr=%v", s.ID(), s.RemoteAddr())
	}
	if got := srv.Sessions(); len(got) != 1 || got[0] != s {
		t.Errorf("Sessions() = %v", got)
	}
	if got, ok := srv.Session("1"); !ok || got != s {
		t.Errorf("Session(1) = %v, %v", got, ok)
	}

	// Default features were negotiated before SessionStarted.
	var features []string
	for _, c := range fake.commands() {
		if c.Name == "feature_set" {
			features = append(features, c.Args["-n"]+"="+c.Args["-v"])
		}
	}
	want := []string{"max_depth=1", "max_children=100", "max_data=8192", "show_hidden=1", "resolved_breakpoints=1", "notify_ok=1", "extended_properties=1"}
	if strings.Join(features, ",") != strings.Join(want, ",") {
		t.Errorf("features = %v", features)
	}

	st, err := s.Status(ctx)
	if err != nil || st != StatusStarting || s.CurrentStatus() != StatusStarting {
		t.Fatalf("Status = %v, %v", st, err)
	}

	// Line breakpoint: host path is translated to the remote URI.
	bp, err := s.SetBreakpoint(ctx, Breakpoint{File: hostRoot + "/index.php", Line: 5})
	if err != nil {
		t.Fatal(err)
	}
	if bp.ID != "10001" || !bp.Resolved || bp.Type != BreakpointLine || bp.FileURI != scriptURI {
		t.Errorf("bp = %+v", bp)
	}
	c, _ := fake.lastCommand("breakpoint_set")
	if c.Args["-t"] != "line" || c.Args["-f"] != scriptURI || c.Args["-n"] != "5" || c.Args["-s"] != "enabled" {
		t.Errorf("breakpoint_set args = %v", c.Args)
	}

	// Conditional breakpoint with a condition that needs no quoting (data).
	cond := `$count > 2 && $name == "a b"`
	cbp, err := s.SetBreakpoint(ctx, Breakpoint{File: hostRoot + "/index.php", Line: 7, Condition: cond, HitValue: 2, HitCondition: ">=", Temporary: true})
	if err != nil {
		t.Fatal(err)
	}
	c, _ = fake.lastCommand("breakpoint_set")
	if c.Args["-t"] != "conditional" || c.Data != cond || c.Args["-h"] != "2" || c.Args["-o"] != ">=" || c.Args["-r"] != "1" {
		t.Errorf("conditional args = %v data=%q", c.Args, c.Data)
	}

	// Unresolved breakpoint in a file with a space (URL-encoded).
	ubp, err := s.SetBreakpoint(ctx, Breakpoint{File: hostRoot + "/my dir/a.php", Line: 200, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if ubp.Resolved || !ubp.Disabled {
		t.Errorf("ubp = %+v", ubp)
	}
	c, _ = fake.lastCommand("breakpoint_set")
	if c.Args["-f"] != "file:///var/www/html/my%20dir/a.php" || c.Args["-s"] != "disabled" {
		t.Errorf("encoded file arg = %v", c.Args)
	}

	// Exception and call breakpoints.
	ebp, err := s.SetBreakpoint(ctx, Breakpoint{Exception: "RuntimeException"})
	if err != nil || ebp.Type != BreakpointException {
		t.Fatalf("exception bp = %+v, %v", ebp, err)
	}
	if _, err := s.SetBreakpoint(ctx, Breakpoint{Function: "greet", Class: `App\Greeter`}); err != nil {
		t.Fatal(err)
	}
	c, _ = fake.lastCommand("breakpoint_set")
	if c.Args["-t"] != "call" || c.Args["-m"] != "greet" || c.Args["-a"] != `App\Greeter` {
		t.Errorf("call args = %v", c.Args)
	}

	waitFor(t, "breakpoint_resolved events", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.resolved) == 2
	})
	rec.mu.Lock()
	if r := rec.resolved[0]; r.ID != "10001" || r.File != hostRoot+"/index.php" || r.Line != 5 || !r.Resolved {
		t.Errorf("resolved = %+v", r)
	}
	rec.mu.Unlock()

	// Run to the first breakpoint.
	bi, err := s.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bi.Status != StatusBreak || bi.Line != 5 || bi.File != hostRoot+"/index.php" || bi.FileURI != scriptURI {
		t.Errorf("Run = %+v", bi)
	}
	if s.LastBreak() != bi || s.CurrentStatus() != StatusBreak {
		t.Errorf("LastBreak = %+v status=%v", s.LastBreak(), s.CurrentStatus())
	}

	frames, err := s.Stack(ctx)
	if err != nil || len(frames) != 1 || frames[0].Where != "{main}" || frames[0].Line != 5 || frames[0].File != hostRoot+"/index.php" {
		t.Fatalf("Stack = %+v, %v", frames, err)
	}

	if bi, err = s.StepOver(ctx); err != nil || bi.Line != 6 {
		t.Fatalf("StepOver = %+v, %v", bi, err)
	}
	if bi, err = s.StepInto(ctx); err != nil || bi.File != hostRoot+"/lib/greet.php" || bi.Line != 21 {
		t.Fatalf("StepInto = %+v, %v", bi, err)
	}
	frames, err = s.Stack(ctx)
	if err != nil || len(frames) != 2 || frames[0].Where != "greet" || frames[1].Level != 1 || frames[1].File != hostRoot+"/index.php" {
		t.Fatalf("Stack = %+v, %v", frames, err)
	}

	ctxs, err := s.ContextNames(ctx, 0)
	if err != nil || len(ctxs) != 3 || ctxs[1] != (Context{ID: 1, Name: "Superglobals"}) {
		t.Fatalf("ContextNames = %+v, %v", ctxs, err)
	}
	c, _ = fake.lastCommand("context_names")
	if c.Args["-d"] != "0" {
		t.Errorf("context_names args = %v", c.Args)
	}

	vars, err := s.Variables(ctx, 1, ContextLocals)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = fake.lastCommand("context_get")
	if c.Args["-d"] != "1" || c.Args["-c"] != "0" {
		t.Errorf("context_get args = %v", c.Args)
	}
	if len(vars) != 5 {
		t.Fatalf("vars = %d", len(vars))
	}
	if v := vars[0]; v.Name != "$name" || v.Type != "string" || v.Value != "World" || v.Size != 5 {
		t.Errorf("$name = %+v", v)
	}
	if v := vars[1]; v.Value != "3" || v.Type != "int" {
		t.Errorf("$count = %+v", v)
	}
	list := vars[2]
	if !list.HasChildren || list.NumChildren != 3 || len(list.Children) != 2 || list.Children[1].FullName != "$list[1]" || list.Children[1].Value != "2" || !list.HasMorePages() {
		t.Errorf("$list = %+v", list)
	}
	if v := vars[3]; v.Name != "$obj" || v.FullName != "$obj" || v.ClassName != `App\Ünïcode` || v.Value != "" {
		t.Errorf("$obj (extended) = %+v", v)
	}
	if v := vars[4]; v.Type != "uninitialized" || v.HasMorePages() {
		t.Errorf("$unset = %+v", v)
	}
	if sg, err := s.Variables(ctx, 0, ContextSuperglobals); err != nil || len(sg) != 1 || sg[0].Name != "$_GET" {
		t.Errorf("superglobals = %v, %v", sg, err)
	}

	// Paging.
	page1, err := s.Property(ctx, PropertyRequest{FullName: "$list", Page: 1, MaxData: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if page1.Page != 1 || len(page1.Children) != 1 || page1.Children[0].Value != "3" || page1.HasMorePages() {
		t.Errorf("page1 = %+v", page1)
	}
	c, _ = fake.lastCommand("property_get")
	if c.Args["-p"] != "1" || c.Args["-m"] != "1024" || c.Args["-d"] != "0" || c.Args["-c"] != "0" {
		t.Errorf("property_get args = %v", c.Args)
	}
	obj, err := s.Property(ctx, PropertyRequest{FullName: "$obj"})
	if err != nil || obj.ClassName != `App\Greeter` || obj.Children[0].Facet != "private" || obj.Children[0].Value != "Hi" || obj.Children[0].FullName != "$obj->greeting" {
		t.Errorf("$obj = %+v, %v", obj, err)
	}
	// Names with spaces and quotes must survive quoting.
	if p, err := s.Property(ctx, PropertyRequest{FullName: `$map["a key"]`, Key: "k1"}); err != nil || p.Value != "1" {
		t.Errorf("quoted property = %+v, %v", p, err)
	}
	c, _ = fake.lastCommand("property_get")
	if c.Args["-k"] != "k1" {
		t.Errorf("key arg = %v", c.Args)
	}
	if _, err := s.Property(ctx, PropertyRequest{FullName: "$empty"}); err == nil {
		t.Error("empty property_get: want error")
	}
	var dErr *Error
	if _, err := s.Property(ctx, PropertyRequest{FullName: "$nope"}); !errors.As(err, &dErr) || dErr.Code != 300 || dErr.Command != "property_get" {
		t.Errorf("missing property err = %v", err)
	} else if !strings.Contains(dErr.Error(), "can not get property") {
		t.Errorf("error text = %q", dErr.Error())
	}

	val, typ, err := s.PropertyValue(ctx, PropertyRequest{FullName: "$name", Page: 3})
	if err != nil || val != "World" || typ != "string" {
		t.Errorf("PropertyValue = %q %q %v", val, typ, err)
	}
	c, _ = fake.lastCommand("property_value")
	if _, ok := c.Args["-p"]; ok {
		t.Errorf("property_value must not page: %v", c.Args)
	}

	// Watches.
	p, err := s.Eval(ctx, "$count + 1")
	if err != nil || p.Value != "4" || p.Type != "int" || p.Name != "$count + 1" || p.FullName != "$count + 1" {
		t.Errorf("Eval = %+v, %v", p, err)
	}
	if p, err := s.Eval(ctx, "noop()"); err != nil || p.Type != "uninitialized" {
		t.Errorf("Eval noop = %+v, %v", p, err)
	}
	if _, err := s.Eval(ctx, "bad("); !errors.As(err, &dErr) || dErr.Code != 206 {
		t.Errorf("Eval error = %v", err)
	}

	if bi, err = s.StepOut(ctx); err != nil || bi.Line != 9 || bi.File != hostRoot+"/index.php" {
		t.Fatalf("StepOut = %+v, %v", bi, err)
	}

	// Breakpoint management.
	bps, err := s.Breakpoints(ctx)
	if err != nil || len(bps) != 5 {
		t.Fatalf("Breakpoints = %+v, %v", bps, err)
	}
	if bps[1].Condition != cond || bps[1].Type != BreakpointConditional || bps[1].File != hostRoot+"/index.php" {
		t.Errorf("listed conditional = %+v", bps[1])
	}
	if bps[2].File != hostRoot+"/my dir/a.php" || !bps[2].Disabled {
		t.Errorf("listed encoded = %+v", bps[2])
	}
	if err := s.SetBreakpointEnabled(ctx, cbp.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBreakpointEnabled(ctx, cbp.ID, true); err != nil {
		t.Fatal(err)
	}
	if c, _ := fake.lastCommand("breakpoint_update"); c.Args["-s"] != "enabled" || c.Args["-d"] != cbp.ID {
		t.Errorf("breakpoint_update args = %v", c.Args)
	}
	if err := s.SetBreakpointEnabled(ctx, "999", true); !errors.As(err, &dErr) || dErr.Code != 205 {
		t.Errorf("update unknown = %v", err)
	}
	if err := s.RemoveBreakpoint(ctx, cbp.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveBreakpoint(ctx, cbp.ID); err == nil {
		t.Error("double remove: want error")
	}

	// No line breakpoint ahead -> exception breakpoint hits.
	bi, err = s.Run(ctx)
	if err != nil || bi.Exception != "RuntimeException" || bi.ExceptionCode != "42" || bi.Message != "boom" || bi.Line != 22 {
		t.Fatalf("exception break = %+v, %v", bi, err)
	}
	if err := s.RemoveBreakpoint(ctx, ebp.ID); err != nil {
		t.Fatal(err)
	}
	bi, err = s.Run(ctx)
	if err != nil || bi.Status != StatusStopping || bi.File != "" {
		t.Fatalf("Run to end = %+v, %v", bi, err)
	}

	bi, err = s.Stop(ctx)
	if err != nil || bi.Status != StatusStopped {
		t.Fatalf("Stop = %+v, %v", bi, err)
	}
	if err := rec.waitEnded(t); err != nil {
		t.Errorf("SessionEnded err = %v", err)
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed")
	}
	if s.Err() != nil || s.CurrentStatus() != StatusStopped {
		t.Errorf("Err=%v status=%v", s.Err(), s.CurrentStatus())
	}
	if _, err := s.Status(ctx); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("after close: %v", err)
	}
	waitFor(t, "session removal", func() bool { return len(srv.Sessions()) == 0 })

	// Event order and contents.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.order[0] != "started" || rec.order[len(rec.order)-1] != "ended" {
		t.Errorf("event order = %v", rec.order)
	}
	var seq []string
	for _, ev := range rec.statuses {
		seq = append(seq, string(ev.Info.Status))
	}
	wantSeq := "running,break,running,break,running,break,running,break,running,break,running,stopping,stopped"
	if strings.Join(seq, ",") != wantSeq {
		t.Errorf("status sequence = %v", seq)
	}
	if ev := rec.statuses[1]; ev.Previous != StatusRunning || ev.Command != "run" || ev.Info.Line != 5 {
		t.Errorf("break event = %+v", ev)
	}
	if len(rec.outputs) != 3 || rec.outputs[0] != (Stream{Type: "stdout", Data: "Hello"}) {
		t.Errorf("outputs = %+v", rec.outputs)
	}
	if len(rec.notes) != 3 {
		t.Fatalf("notes = %+v", rec.notes)
	}
	if n := rec.notes[0]; n.Name != "error" || n.Type != "Warning" || n.Code != "2" || n.Line != 4 || n.File != hostRoot+"/index.php" || n.Message != "Undefined variable $x" {
		t.Errorf("note = %+v", n)
	}
}

func TestMultipleSessionsAndServerStop(t *testing.T) {
	rec := newRecorder()
	srv := startServer(t, Config{Events: rec.events(), Features: []Feature{}})
	const n = 4
	for i := 0; i < n; i++ {
		dialFake(t, srv.Addr(), fmt.Sprint(i))
	}
	sessions := make([]*Session, 0, n)
	for i := 0; i < n; i++ {
		sessions = append(sessions, rec.waitStarted(t))
	}
	if got := srv.Sessions(); len(got) != n || got[0].ID() != "1" || got[n-1].ID() != fmt.Sprint(n) {
		t.Fatalf("Sessions = %d", len(got))
	}
	// Concurrent commands on all sessions.
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			ctx := testCtx(t)
			for j := 0; j < 20; j++ {
				if _, err := s.Status(ctx); err != nil {
					t.Error(err)
					return
				}
			}
			if bi, err := s.Run(ctx); err != nil || bi.Status != StatusStopping {
				t.Errorf("Run = %+v, %v", bi, err)
			}
		}(s)
	}
	wg.Wait()
	// Without feature negotiation no feature_set was sent; identity mapping.
	if f := sessions[0].Info().File; f != "/var/www/html/index.php" {
		t.Errorf("identity file = %q", f)
	}

	if err := srv.Stop(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := rec.waitEnded(t); err != nil {
			t.Errorf("ended err = %v", err)
		}
	}
	if srv.Running() || srv.Addr() != nil || len(srv.Sessions()) != 0 {
		t.Error("server still running after Stop")
	}
	if err := srv.Stop(); err != nil {
		t.Errorf("second Stop = %v", err)
	}
}

func TestContextTimeoutAndClose(t *testing.T) {
	rec := newRecorder()
	srv := startServer(t, Config{Events: rec.events()})
	fake := dialFake(t, srv.Addr(), "1")
	s := rec.waitStarted(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Eval(ctx, "sleep"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Eval timeout err = %v", err)
	}
	// Session is still usable.
	if _, err := s.Status(testCtx(t)); err != nil {
		t.Fatalf("Status after timeout: %v", err)
	}

	// A pending command fails when the engine disconnects.
	errc := make(chan error, 1)
	go func() {
		_, err := s.Eval(context.Background(), "sleep")
		errc <- err
	}()
	waitFor(t, "pending eval", func() bool {
		n := 0
		for _, c := range fake.commands() {
			if c.Name == "eval" {
				n++
			}
		}
		return n == 2
	})
	fake.close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrSessionClosed) {
			t.Errorf("pending err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending command not released")
	}
	if err := rec.waitEnded(t); err != nil {
		t.Errorf("ended err = %v", err)
	}
	if _, err := s.Run(testCtx(t)); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Run after close = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Error(err)
	}
}

func TestDetachBreakAndRawSend(t *testing.T) {
	rec := newRecorder()
	srv := startServer(t, Config{Events: rec.events(), Features: []Feature{{"no_such_feature", "1"}, {"max_depth", "2"}}})
	fake := dialFake(t, srv.Addr(), "1")
	s := rec.waitStarted(t)
	ctx := testCtx(t)

	// Rejected feature during negotiation is ignored; later ones are sent.
	if c, ok := fake.lastCommand("feature_set"); !ok || c.Args["-n"] != "max_depth" {
		t.Errorf("features = %v", fake.commands())
	}
	if err := s.FeatureSet(ctx, "no_such_feature", "1"); err == nil {
		t.Error("FeatureSet unsupported: want error")
	}
	if err := s.FeatureSet(ctx, "max_children", "50"); err != nil {
		t.Error(err)
	}
	if err := s.Break(ctx); err != nil {
		t.Error(err)
	}
	r, err := s.Send(ctx, "status", nil, nil)
	if err != nil || r.Command != "status" || r.Status != StatusStarting || !strings.Contains(string(r.Raw), `command="status"`) {
		t.Errorf("Send = %+v, %v", r, err)
	}
	var dErr *Error
	r, err = s.Send(ctx, "typo_cmd", []string{"-x", "1"}, []byte("data"))
	if !errors.As(err, &dErr) || dErr.Code != 4 || r == nil || r.Command != "typo_cmd" {
		t.Errorf("Send unknown = %+v, %v", r, err)
	}
	if _, err := s.Send(ctx, "bad name", nil, nil); err == nil {
		t.Error("invalid name: want error")
	}
	if _, err := s.SetBreakpoint(ctx, Breakpoint{Line: 3}); err == nil {
		t.Error("line bp without file: want error")
	}
	if _, err := s.SetBreakpoint(ctx, Breakpoint{Type: BreakpointCall}); err == nil {
		t.Error("call bp without function: want error")
	}
	bp, err := s.SetBreakpoint(ctx, Breakpoint{Type: BreakpointException})
	if err != nil || bp.Exception != "*" {
		t.Errorf("wildcard exception bp = %+v, %v", bp, err)
	}

	if err := s.Detach(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rec.waitEnded(t); err != nil {
		t.Errorf("ended err = %v", err)
	}
}

func TestBadConnections(t *testing.T) {
	rec := newRecorder()
	srv := startServer(t, Config{Events: rec.events(), InitTimeout: 200 * time.Millisecond})

	cases := []string{
		"garbage\x00",                      // invalid length
		"5\x00<foo>\x00",                   // not NUL-terminated at given length
		"7\x00<foo/>\x00",                  // not an init packet
		"12\x00<response/>\x00",            // response instead of init
		"14\x00<init><bad</i>\x00",         // malformed XML
		"",                                 // silent client: init timeout
		"100\x00<init fileuri=\"x\"/>\x00", // truncated body
	}
	for _, payload := range cases {
		conn, err := net.Dial("tcp", srv.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if payload != "" {
			_, _ = conn.Write([]byte(payload))
		}
		if strings.HasPrefix(payload, "100") {
			_ = conn.(*net.TCPConn).CloseWrite()
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 1)
		if _, err := conn.Read(buf); err == nil {
			t.Errorf("payload %q: connection not closed", payload)
		} else {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Errorf("payload %q: connection not closed (timeout)", payload)
			}
		}
		_ = conn.Close()
	}
	select {
	case s := <-rec.started:
		t.Errorf("unexpected session %v", s.ID())
	default:
	}
	if len(srv.Sessions()) != 0 {
		t.Error("sessions registered for bad connections")
	}
}

func TestServerStartErrors(t *testing.T) {
	srv := startServer(t, Config{})
	if err := srv.Start(context.Background()); err == nil {
		t.Error("second Start: want error")
	}
	other := NewServer(Config{Addr: srv.Addr().String()})
	if err := other.Start(context.Background()); err == nil {
		_ = other.Stop()
		t.Error("Start on used port: want error")
	}
	if NewServer(Config{}).cfg.Addr != DefaultAddr {
		t.Error("default addr")
	}
	// Restart after Stop works.
	if err := srv.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !srv.Running() {
		t.Error("not running after restart")
	}
}

func TestSetMapperAppliesToSessions(t *testing.T) {
	rec := newRecorder()
	srv := startServer(t, Config{Events: rec.events(), Features: []Feature{}})
	dialFake(t, srv.Addr(), "1")
	s := rec.waitStarted(t)
	ctx := testCtx(t)
	if _, err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	srv.SetMapper(NewPrefixMapper(Mapping{Host: `C:\work\app`, Remote: "/var/www/html"}))
	frames, err := s.Stack(ctx)
	if err != nil || frames[0].File != `C:\work\app\index.php` {
		t.Fatalf("frames = %+v, %v", frames, err)
	}
}
