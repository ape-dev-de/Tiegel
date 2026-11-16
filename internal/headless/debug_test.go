package headless

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBreakpointsCommand(t *testing.T) {
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	var env Env
	runIn := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb strings.Builder
		e := testEnv(t, nil, &out, &errb)
		if env.Options.ConfigDir == "" {
			env = e
		}
		e.Options.ConfigDir = env.Options.ConfigDir // share settings across runs
		code := Run(t.Context(), append(args, "--project", dir), e)
		return code, out.String(), errb.String()
	}
	if code, out, _ := runIn("breakpoints"); code != ExitOK || !strings.Contains(out, "no breakpoints") {
		t.Fatalf("empty: %d %q", code, out)
	}
	if code, out, _ := runIn("breakpoints", "set", "a.php:3", "--condition", "$x > 1"); code != ExitOK || out != "set a.php:3\n" {
		t.Fatalf("set: %d %q", code, out)
	}
	runIn("breakpoints", "set", "a.php:5")
	runIn("breakpoints", "disable", "a.php:5")
	if _, out, _ := runIn("breakpoints"); out != "a.php:3 if $x > 1\na.php:5 [disabled]\n" {
		t.Fatalf("list %q", out)
	}
	runIn("breakpoints", "enable", "a.php:5")
	runIn("breakpoints", "remove", "a.php:3")
	_, out, _ := runIn("breakpoints", "--json")
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 || list[0]["enabled"] != true {
		t.Fatalf("json %q %v", out, err)
	}
	for _, args := range [][]string{
		{"breakpoints", "set"}, {"breakpoints", "set", "a.php"}, {"breakpoints", "set", "a.php:x"},
		{"breakpoints", "jump", "a.php:1"},
	} {
		if code, _, _ := runIn(args...); code != ExitUsage {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	if code, _, errs := runIn("breakpoints", "remove", "a.php:99"); code != ExitError || errs == "" {
		t.Fatalf("remove unknown: %d %q", code, errs)
	}
}

func TestBreakpointsE2E(t *testing.T) {
	bin := buildBinary(t)
	cfg := t.TempDir()
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	if code, _, errs := e2e(t, bin, cfg, "", "--headless", "breakpoints", "set", "a.php:2", "--project", dir); code != 0 {
		t.Fatalf("set: %d %s", code, errs)
	}
	code, out, errs := e2e(t, bin, cfg, "", "--headless", "breakpoints", "--project", dir)
	if code != 0 || out != "a.php:2\n" {
		t.Fatalf("list in a new process: %d %q %s", code, out, errs)
	}
	code, out, _ = e2e(t, bin, cfg, "", "--headless", "call", "debug.breakpoints.list", "--project", dir)
	if code != 0 || !strings.Contains(out, `"id": "a.php:2"`) {
		t.Fatalf("call: %d %q", code, out)
	}
}
