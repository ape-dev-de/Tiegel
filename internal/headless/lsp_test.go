package headless

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// buildFakeLSP builds the fake language server binary (once per run).
func buildFakeLSP(t *testing.T) string {
	t.Helper()
	return buildOnce(t, &fakeLSPBin, "fakelsp", "github.com/ape-dev-de/tiegel/internal/lsp/fakeserver/fakelsp")
}

func TestLSPCheckCommand(t *testing.T) {
	t.Parallel()
	fake := buildFakeLSP(t)
	dir := project(t, map[string]string{"bad.php": "<?php\necho 'ERR';\n", "ok.php": "<?php\necho 1;\n", "x.txt": "x"})

	code, out, errs := run(t, "", "lsp-check", "bad.php", "ok.php", "--command", fake, "--project", dir)
	if code != ExitError || !strings.Contains(out, "bad.php:2:7: error: error marker [fake E1]") || strings.Contains(out, "ok.php") {
		t.Fatalf("lsp-check %d %q %q", code, out, errs)
	}
	code, out, errs = run(t, "", "lsp-check", "ok.php", "--json", "--command", fake, "--project", dir)
	var res []lspCheckResult
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || len(res) != 1 || res[0].Path != "ok.php" || len(res[0].Diagnostics) != 0 {
		t.Fatalf("json %d %q %q", code, out, errs)
	}
	if code, _, _ := run(t, "", "lsp-check", "x.txt", "--command", fake, "--project", dir); code != ExitError {
		t.Fatal("non-PHP file accepted")
	}
	if code, _, _ := run(t, "", "lsp-check", "ok.php", "--timeout", "nope", "--project", dir); code != ExitUsage {
		t.Fatal("bad timeout accepted")
	}
	if code, _, errs := run(t, "", "lsp-check", "ok.php", "--project", dir); code != ExitError || !strings.Contains(errs, "no PHP language server") {
		t.Fatalf("no server: %d %q", code, errs)
	}
	// A server that never publishes diagnostics: timeout.
	if runtime.GOOS != "windows" { // no /bin/cat there
		if code, _, errs := run(t, "", "lsp-check", "ok.php", "--command", "/bin/cat", "--timeout", "300ms", "--project", dir); code != ExitError || errs == "" {
			t.Fatalf("timeout: %d %q", code, errs)
		}
	}
}

func TestLSPCheckE2E(t *testing.T) {
	t.Parallel()
	fake := buildFakeLSP(t)
	bin := buildBinary(t)
	dir := project(t, map[string]string{"bad.php": "<?php\n// ERR here\n"})
	code, out, errs := e2e(t, bin, t.TempDir(), "", "--headless", "lsp-check", "bad.php", "--command", fake, "--project", dir)
	if code != 1 || !strings.Contains(out, "bad.php:2:4: error: error marker") {
		t.Fatalf("e2e %d %q %q", code, out, errs)
	}
	code, out, errs = e2e(t, bin, t.TempDir(), "", "--headless", "call", "lsp.status")
	if code != 0 || !strings.Contains(out, `"running": false`) {
		t.Fatalf("status %d %q %q", code, out, errs)
	}
}
