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
		t.Skip("shell script fake composer")
	}
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	bin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\necho \"Using version ^2 for $3\"\n"
	if err := os.WriteFile(filepath.Join(bin, "composer"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	called := func() string { b, _ := os.ReadFile(logFile); return string(b) }

	code, out, errs := runPath(t, bin, "install", "lsp-phpactor", "--dry-run", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "command: composer global require phpactor/phpactor") || called() != "" {
		t.Fatalf("dry-run %d %q %q", code, out, errs)
	}
	code, out, errs = runPath(t, bin, "install", "lsp-phpactor", "--scope", "project", "--project", dir)
	if code != ExitError || !strings.Contains(errs, "--yes") || !strings.Contains(out, "changes composer.json: true") || called() != "" {
		t.Fatalf("unconfirmed %d %q %q", code, out, errs)
	}
	code, out, errs = runPath(t, bin, "install", "lsp-phpactor", "--scope", "project", "--yes", "--project", dir)
	if code != ExitOK || !strings.Contains(out, "Using version ^2 for phpactor/phpactor") || !strings.Contains(out, "phpactor: succeeded") ||
		called() != "require --dev phpactor/phpactor\n" {
		t.Fatalf("install %d %q %q (calls %q)", code, out, errs, called())
	}
	// Hint-only recipe and missing composer: hint, exit 1, nothing runs.
	code, out, _ = runPath(t, bin, "install", "lsp-phpantom", "--project", dir)
	if code != ExitError || !strings.Contains(out, "hint: PHPantom is a native binary") {
		t.Fatalf("phpantom %d %q", code, out)
	}
	code, out, _ = runPath(t, t.TempDir(), "install", "lsp-phpactor", "--yes", "--json", "--project", dir)
	var res installOutput
	if code != ExitError || json.Unmarshal([]byte(out), &res) != nil || res.Plan.Available || !strings.Contains(res.Plan.Hint, "Composer was not found") {
		t.Fatalf("no composer %d %q", code, out)
	}
	if code, _, _ := runPath(t, bin, "install", "lsp-nope", "--project", dir); code != ExitError {
		t.Fatal("unknown plugin accepted")
	}
}
