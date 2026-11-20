package headless

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestLayoutCommand(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	cfg := t.TempDir()
	runL := func(args ...string) (int, string, string) {
		return run(t, "", append(args, "--project", dir, "--config-dir", cfg)...)
	}
	if code, out, errs := runL("layout"); code != ExitOK || !strings.Contains(out, "defaults (nothing stored)") || !strings.Contains(out, "named layouts: none") {
		t.Fatalf("get: %d %s %s", code, out, errs)
	}
	wide := `{"tools":{"terminal":"bottom"},"corners":{"left":"bottom","right":"bottom"}}`
	if code, out, errs := runL("layout", "save", "Wide", wide); code != ExitOK || !strings.Contains(out, `saved layout "Wide"`) {
		t.Fatalf("save: %d %s %s", code, out, errs)
	}
	if code, _, errs := runL("layout", "save", "Bad", "[1]"); code == ExitOK || !strings.Contains(errs, "JSON object") {
		t.Fatalf("save invalid: %d %s", code, errs)
	}
	if code, out, errs := runL("layout", "apply", "Wide"); code != ExitOK || !strings.Contains(out, "applied") {
		t.Fatalf("apply: %d %s %s", code, out, errs)
	}
	code, out, _ := runL("layout", "get", "--json")
	var st api.LayoutState
	if code != ExitOK || json.Unmarshal([]byte(out), &st) != nil || !st.Found || !strings.Contains(string(st.Layout), "corners") {
		t.Fatalf("get json: %d %s", code, out)
	}
	if code, out, _ := runL("layout", "get", "Wide"); code != ExitOK || !strings.Contains(out, `layout "Wide"`) || !strings.Contains(out, `"terminal": "bottom"`) {
		t.Fatalf("get named: %d %s", code, out)
	}
	if code, out, _ := runL("layout", "list"); code != ExitOK || !strings.Contains(out, "named layouts: Wide") {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, out, _ := runL("layout", "reset"); code != ExitOK || !strings.Contains(out, "reset") {
		t.Fatalf("reset: %d %s", code, out)
	}
	if code, out, _ := runL("layout"); code != ExitOK || !strings.Contains(out, "defaults") {
		t.Fatalf("after reset: %d %s", code, out)
	}
	if code, out, _ := runL("layout", "delete", "Wide"); code != ExitOK || !strings.Contains(out, "deleted") {
		t.Fatalf("delete: %d %s", code, out)
	}
	if code, _, errs := runL("layout", "apply", "Wide"); code == ExitOK || !strings.Contains(errs, "no layout named") {
		t.Fatalf("apply missing: %d %s", code, errs)
	}
	if code, _, _ := runL("layout", "frobnicate"); code == ExitOK {
		t.Fatal("unknown verb accepted")
	}
}
