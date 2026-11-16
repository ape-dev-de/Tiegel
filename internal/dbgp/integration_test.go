package dbgp

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const integrationScript = `<?php
function greet(string $name): string {
    $greeting = "Hello, " . $name;
    return $greeting;
}
$items = [1, 2, 3];
$count = count($items);
$msg = greet("World");
echo $msg, "\n";
`

// TestRealXdebug runs a script with a real php + Xdebug installation if one
// is available.
func TestRealXdebug(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	out, err := exec.Command(php, "-m").Output()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "xdebug") {
		t.Skip("xdebug extension not loaded")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "script with space.php")
	if err := os.WriteFile(script, []byte(integrationScript), 0o600); err != nil {
		t.Fatal(err)
	}
	// Resolve symlinks (e.g. /var -> /private/var on macOS) so that the
	// paths reported by PHP match.
	if real, err := filepath.EvalSymlinks(script); err == nil {
		script = real
	}

	rec := newRecorder()
	srv := startServer(t, Config{Events: rec.events()})
	port := srv.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, php,
		"-dxdebug.mode=debug",
		"-dxdebug.start_with_request=yes",
		"-dxdebug.client_host=127.0.0.1",
		fmt.Sprintf("-dxdebug.client_port=%d", port),
		script)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()

	s := rec.waitStarted(t)
	if s.Info().Language != "PHP" || s.Info().File != script {
		t.Errorf("init = %+v", s.Info())
	}
	bp, err := s.SetBreakpoint(ctx, Breakpoint{File: script, Line: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBreakpoint(ctx, Breakpoint{File: script, Line: 7, Condition: "count($items) == 3"}); err != nil {
		t.Fatal(err)
	}
	if bp.ID == "" {
		t.Errorf("bp = %+v", bp)
	}

	bi, err := s.Run(ctx)
	if err != nil || bi.Status != StatusBreak || bi.Line != 7 || bi.File != script {
		t.Fatalf("first break = %+v, %v", bi, err)
	}
	vars, err := s.Variables(ctx, 0, ContextLocals)
	if err != nil {
		t.Fatal(err)
	}
	var items *Property
	for _, v := range vars {
		if v.Name == "$items" {
			items = v
		}
	}
	if items == nil || items.Type != "array" || items.NumChildren != 3 {
		t.Errorf("$items = %+v", items)
	}

	bi, err = s.Run(ctx)
	if err != nil || bi.Line != 4 {
		t.Fatalf("second break = %+v, %v", bi, err)
	}
	frames, err := s.Stack(ctx)
	if err != nil || len(frames) < 2 || frames[0].Where != "greet" || frames[1].File != script {
		t.Fatalf("stack = %+v, %v", frames, err)
	}
	p, err := s.Property(ctx, PropertyRequest{FullName: "$greeting"})
	if err != nil || p.Value != "Hello, World" {
		t.Errorf("$greeting = %+v, %v", p, err)
	}
	ev, err := s.Eval(ctx, "strlen($greeting) * 2")
	if err != nil || ev.Value != "24" {
		t.Errorf("eval = %+v, %v", ev, err)
	}
	if _, err := s.StepOut(ctx); err != nil {
		t.Fatal(err)
	}
	bi, err = s.Run(ctx)
	if err != nil || bi.Status != StatusStopping {
		t.Fatalf("final run = %+v, %v", bi, err)
	}
	if _, err := s.Stop(ctx); err != nil {
		t.Logf("stop: %v", err)
	}
	if err := rec.waitEnded(t); err != nil {
		t.Errorf("ended: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("php: %v", err)
	}
	if !strings.Contains(stdout.String(), "Hello, World") {
		t.Errorf("stdout = %q", stdout.String())
	}
}
