package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	runsvc "github.com/ape-dev-de/tiegel/internal/run"
)

func TestComposerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake composer is a shell script")
	}
	// One config dir for all invocations: settings and configurations persist.
	cfgDir := t.TempDir()
	run := func(t *testing.T, stdin string, args ...string) (int, string, string) {
		return run(t, stdin, append([]string{"--config-dir", cfgDir}, args...)...)
	}
	dir := project(t, map[string]string{"composer.json": `{"scripts":{"test":"phpunit","lint":["a","b"]},"scripts-descriptions":{"test":"Run the tests"}}`})
	fake := filepath.Join(t.TempDir(), "composer")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"composer $*\"\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, "", "composer", "--project", dir)
	if code != 0 || !strings.Contains(out, "test") || !strings.Contains(out, "Run the tests") || !strings.Contains(out, "a && b") {
		t.Fatalf("scripts: %d %q %q", code, out, errOut)
	}
	if code, _, _ := run(t, "", "settings", "set", "composer.path", fake, "--scope", "project", "--project", dir); code != 0 {
		t.Fatal("settings set")
	}
	code, out, errOut = run(t, "", "composer", "run", "test", "--project", dir, "--interpreter", "host", "--", "--filter", "x")
	if code != 2 || !strings.Contains(out, "composer run-script test -- --filter x") {
		t.Fatalf("run: %d %q %q", code, out, errOut)
	}
	for _, bad := range [][]string{{"composer", "run"}, {"composer", "scripts", "x"}, {"composer", "nope"}} {
		if code, _, _ := run(t, "", append(bad, "--project", dir)...); code != ExitUsage {
			t.Errorf("%v: %d", bad, code)
		}
	}
	empty := project(t, map[string]string{"a.php": "<?php\n"})
	if code, out, _ := run(t, "", "composer", "--project", empty); code != 0 || !strings.Contains(out, "no composer.json") {
		t.Fatalf("no composer.json: %d %q", code, out)
	}
}

func TestExternalToolsCommand(t *testing.T) {
	// One config dir for all invocations: settings and configurations persist.
	cfgDir := t.TempDir()
	run := func(t *testing.T, stdin string, args ...string) (int, string, string) {
		return run(t, stdin, append([]string{"--config-dir", cfgDir}, args...)...)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := project(t, map[string]string{"src/a.php": "<?php\n"})
	if code, out, _ := run(t, "", "external-tools", "--project", dir); code != 0 || !strings.Contains(out, "no external tools") {
		t.Fatalf("empty list: %d %q", code, out)
	}
	cfg, _ := json.Marshal(runsvc.Config{Name: "Show", Type: runsvc.TypeExternalTool, Values: map[string]any{
		"program": self, "args": "$FileRelativePath$ $LineNumber$ $SelectedText$", "env": []string{runHelperEnv + "=echo"}}})
	if code, out, errOut := run(t, "", "run-configs", "save", string(cfg), "--project", dir); code != 0 {
		t.Fatalf("save: %d %q %q", code, out, errOut)
	}
	code, out, _ := run(t, "", "external-tools", "list", "--project", dir)
	if code != 0 || !strings.Contains(out, "Show") || !strings.Contains(out, "externalTool.") || !strings.Contains(out, "$FileRelativePath$") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, errOut := run(t, "", "external-tools", "run", "show", "--project", dir, "--file", "src/a.php", "--line", "4", "--selection", "x y")
	if code != 0 || !strings.HasPrefix(out, "src/a.php\n4\nx y\n") {
		t.Fatalf("run: %d %q %q", code, out, errOut)
	}
	// Without a file the file macros fail.
	if code, _, errOut := run(t, "", "external-tools", "run", "Show", "--project", dir); code != ExitUsage || !strings.Contains(errOut, "$FileRelativePath$") {
		t.Fatalf("no file: %d %q", code, errOut)
	}
	code, out, _ = run(t, "", "external-tools", "macros", "--project", dir, "--file", "src/a.php", "--column", "2")
	if code != 0 || !strings.Contains(out, "$FileName$") || !strings.Contains(out, "a.php") || !regexp.MustCompile(`\$ColumnNumber\$\s+2\s`).MatchString(out) {
		t.Fatalf("macros: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "external-tools", "macros", "--project", dir); code != 0 || !strings.Contains(out, "needs a current file") {
		t.Fatalf("macros without file: %d %q", code, out)
	}
	for _, bad := range [][]string{
		{"external-tools", "run"},
		{"external-tools", "nope"},
		{"external-tools", "macros", "--line", "x"},
	} {
		if code, _, _ := run(t, "", append(bad, "--project", dir)...); code != ExitUsage {
			t.Errorf("%v: %d", bad, code)
		}
	}
	if code, _, errOut := run(t, "", "external-tools", "run", "missing", "--project", dir); code != ExitError || !strings.Contains(errOut, "no external tool") {
		t.Fatalf("missing: %d %q", code, errOut)
	}
}
