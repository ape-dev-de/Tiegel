package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
)

// runPath runs a command with pathDir as the only PATH for the server
// and package manager search.
func runPath(t *testing.T, pathDir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := testEnv(t, strings.NewReader(""), &out, &errb)
	env.Options.LSP.PathEnv = pathDir
	code := Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func copyExe(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLSPSupervisorCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable names")
	}
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	empty := t.TempDir()
	cfg := t.TempDir()

	code, out, errs := runPath(t, empty, "lsp-doctor", "--project", dir, "--config-dir", cfg)
	if code != ExitOK || !strings.Contains(out, "cannot start: no PHP language server found") ||
		!strings.Contains(out, "install: tiegel --headless install lsp-phpactor") || !strings.Contains(out, filepath.Join(dir, "vendor", "bin")) {
		t.Fatalf("doctor %d %q %q", code, out, errs)
	}
	code, out, _ = runPath(t, empty, "lsp-status", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.HasPrefix(out, "off") {
		t.Fatalf("status %d %q", code, out)
	}
	code, out, _ = runPath(t, empty, "lsp-status", "--start", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.Contains(out, "not-found") || !strings.Contains(out, "searched:") {
		t.Fatalf("status --start %d %q", code, out)
	}

	// The fake server installed as "phpactor": started natively.
	bin := t.TempDir()
	copyExe(t, buildFakeLSP(t), filepath.Join(bin, "phpactor"))
	code, out, errs = runPath(t, bin, "lsp-restart", "--trace", "--project", dir, "--config-dir", cfg)
	if code != ExitOK || !strings.Contains(out, "running: Phpactor") || !strings.Contains(out, `send {"jsonrpc":"2.0","id":1,"method":"initialize"`) {
		t.Fatalf("restart %d %q %q", code, out, errs)
	}
	code, out, _ = runPath(t, bin, "lsp-status", "--start", "--json", "--project", dir, "--config-dir", cfg)
	var st langphp.Status
	if code != ExitOK || json.Unmarshal([]byte(out), &st) != nil || st.State != "running" || st.PID == 0 {
		t.Fatalf("status json %d %q", code, out)
	}

	// Switch off for the project (persisted), then back to auto.
	code, out, _ = runPath(t, bin, "lsp-select", "none", "--project", dir, "--config-dir", cfg)
	if code != ExitOK || !strings.Contains(out, "switched off") {
		t.Fatalf("select none %d %q", code, out)
	}
	code, out, _ = runPath(t, bin, "lsp-status", "--start", "--project", dir, "--config-dir", cfg)
	if code != ExitError || !strings.Contains(out, "selected: none") {
		t.Fatalf("status after none %d %q", code, out)
	}
	code, out, _ = runPath(t, bin, "lsp-select", "auto", "--project", dir, "--config-dir", cfg)
	if code != ExitOK || !strings.Contains(out, "running") {
		t.Fatalf("select auto %d %q", code, out)
	}
	if code, _, _ := runPath(t, bin, "lsp-select", "emacs", "--project", dir, "--config-dir", cfg); code == ExitOK {
		t.Fatal("unknown server accepted")
	}
	if code, out, _ := runPath(t, bin, "lsp-servers", "--all", "--project", dir, "--config-dir", cfg); code != ExitOK || !strings.Contains(out, "phpantom  (not installed)") {
		t.Fatalf("servers %q", out)
	}
}

func TestInstallCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake curl")
	}
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	bin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "calls")
	// Fake curl: logs the URL and writes the --output file.
	script := "#!/bin/sh\necho \"$8\" >> " + logFile + "\nmkdir -p \"$(dirname \"$7\")\"\nprintf '#!/usr/bin/env php\\n' > \"$7\"\necho downloaded\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	called := func() string { b, _ := os.ReadFile(logFile); return string(b) }

	code, out, errs := runPath(t, bin, "install", "lsp-phpactor", "--dry-run", "--json", "--project", dir)
	var dry installOutput
	if code != ExitOK || json.Unmarshal([]byte(out), &dry) != nil || !strings.Contains(dry.Plan.Display, "curl --fail") ||
		!strings.HasSuffix(dry.Plan.Target, filepath.Join(".local", "bin", "phpactor")) || called() != "" {
		t.Fatalf("dry-run %d %q %q", code, out, errs)
	}
	code, out, errs = runPath(t, bin, "install", "lsp-phpactor", "--project", dir)
	if code != ExitError || !strings.Contains(errs, "--yes") || called() != "" {
		t.Fatalf("unconfirmed %d %q %q", code, out, errs)
	}
	code, out, errs = runPath(t, bin, "install", "lsp-phpactor", "--yes", "--json", "--project", dir)
	var done installOutput
	if code != ExitOK || json.Unmarshal([]byte(out), &done) != nil || done.Result == nil || done.Result.State != install.StateSucceeded || !strings.Contains(called(), "phpactor.phar") {
		t.Fatalf("install %d %q %q (calls %q)", code, out, errs, called())
	}
	if st, err := os.Stat(done.Plan.Target); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("installed file %v %v", st, err)
	}
	if _, err := os.Stat(done.Plan.Target + ".download"); err == nil {
		t.Fatal("download file left behind")
	}
	// Hint-only recipe and missing curl: hint, exit 1, nothing runs.
	code, out, _ = runPath(t, bin, "install", "lsp-phpantom", "--project", dir)
	if code != ExitError || !strings.Contains(out, "hint: PHPantom is a native binary") {
		t.Fatalf("phpantom %d %q", code, out)
	}
	code, out, _ = runPath(t, t.TempDir(), "install", "lsp-phpactor", "--yes", "--json", "--project", dir)
	var res installOutput
	if code != ExitError || json.Unmarshal([]byte(out), &res) != nil || res.Plan.Available || !strings.Contains(res.Plan.Hint, "curl was not found") {
		t.Fatalf("no curl %d %q", code, out)
	}
	if code, _, _ := runPath(t, bin, "install", "lsp-nope", "--project", dir); code != ExitError {
		t.Fatal("unknown plugin accepted")
	}
}
