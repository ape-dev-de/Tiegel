package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	runsvc "github.com/ape-dev-de/tiegel/internal/run"
)

// runHelperEnv makes the test binary act as the program of a run test.
const runHelperEnv = "TIEGEL_HEADLESS_RUN_HELPER"

// runHelperMain runs the helper program and exits when runHelperEnv is
// set (called from TestMain).
func runHelperMain() {
	switch os.Getenv(runHelperEnv) {
	case "":
		return
	case "echo":
		for _, a := range os.Args[1:] {
			fmt.Println(a)
		}
		if os.Getenv("FAIL") != "" {
			fmt.Fprintln(os.Stderr, "failing")
			os.Exit(4)
		}
		os.Exit(0)
	case "sleep":
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	os.Exit(99)
}

func TestRunCommand(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	echo := []string{"run", "--project", dir, "--env", runHelperEnv + "=echo", "--cmd", "--", self}

	code, out, errOut := run(t, "", append(echo, "a", "b c")...)
	if code != 0 || !strings.HasPrefix(out, "a\nb c\n") || !strings.Contains(errOut, "exit code 0") {
		t.Fatalf("run: %d %q %q", code, out, errOut)
	}
	code, out, errOut = run(t, "", "run", "--project", dir, "--env", "FAIL=1", "--env", runHelperEnv+"=echo", "--cmd", "--", self, "x")
	if code != 4 || out != "x\n" || !strings.Contains(errOut, "failing") {
		t.Fatalf("exit code: %d %q %q", code, out, errOut)
	}
	code, out, _ = run(t, "", append(append([]string{"--json"}, echo...), "j")...)
	var res runOutput
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Run.State != runsvc.StateExited || len(res.Output) < 1 || res.Output[0].Text != "j" {
		t.Fatalf("json: %d %q", code, out)
	}

	// Interrupted runs are stopped (exit 130).
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	code, out, _ = runCtx(t, ctx, "", "run", "--project", dir, "--env", runHelperEnv+"=sleep", "--cmd", "--", self)
	if code != 130 || !strings.Contains(out, "ready") {
		t.Fatalf("interrupt: %d %q", code, out)
	}

	// No PHP interpreter: the error explains it.
	code, _, errOut = run(t, "", "run", "--project", dir, "--file", "a.php")
	if code != ExitError || !strings.Contains(errOut, "interpreter") {
		t.Fatalf("no php: %d %q", code, errOut)
	}
	for _, bad := range [][]string{
		{"run", "--project", dir},
		{"run", "--project", dir, "--file", "a.php", "--cmd"},
		{"run", "--project", dir, "--cmd"},
		{"run", "--project", dir, "cfg", "--interpreter", "host"},
		{"run", "--project", dir, "cfg", "--env", "A=1"},
		{"run-configs", "--project", dir, "bogus"},
		{"run-configs", "--project", dir, "delete"},
		{"run-configs", "--project", dir, "save", "{"},
	} {
		if code, _, _ := run(t, "", bad...); code != ExitUsage {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
}

func TestRunConfigsCommand(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := project(t, map[string]string{"composer.json": `{"scripts":{"lint":"x"}}`})
	// One config dir for all invocations: configurations persist.
	cfgDir := t.TempDir()
	run := func(t *testing.T, stdin string, args ...string) (int, string, string) {
		return run(t, stdin, append(args, "--config-dir", cfgDir)...)
	}
	code, out, _ := run(t, "", "run-configs", "--project", dir, "types")
	if code != 0 || !strings.Contains(out, "php-script\tPHP Script (core)") || !strings.Contains(out, "[lint]") {
		t.Fatalf("types: %d %q", code, out)
	}
	cfg, _ := json.Marshal(runsvc.Config{Name: "Echo", Type: runsvc.TypeCommand, Values: map[string]any{
		"command": runsvc.QuoteWords([]string{self, "from config"}), "env": []string{runHelperEnv + "=echo"}}})
	code, out, errOut := run(t, string(cfg), "run-configs", "--project", dir, "save", "-")
	if code != 0 || !strings.HasPrefix(out, "saved Echo (") {
		t.Fatalf("save: %d %q %q", code, out, errOut)
	}
	code, out, _ = run(t, "", "run-configs", "--project", dir)
	if code != 0 || !strings.HasPrefix(out, "* Echo") || !strings.Contains(out, "command") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = run(t, "", "run", "--project", dir, "echo")
	if code != 0 || out != "from config\n" {
		t.Fatalf("run config: %d %q", code, out)
	}
	if code, _, errOut := run(t, "", "run-configs", "--project", dir, "save", `{"name":"x","type":"nope"}`); code != ExitUsage || errOut == "" {
		t.Fatalf("save bad type: %d %q", code, errOut)
	}
	if code, _, _ := run(t, "", "run-configs", "--project", dir, "select", "Echo"); code != 0 {
		t.Fatal("select")
	}
	code, out, _ = run(t, "", "--json", "run-configs", "--project", dir, "delete", "Echo")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("delete: %d %q", code, out)
	}
}
