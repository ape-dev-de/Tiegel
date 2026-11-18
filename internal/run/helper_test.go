package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the program the tests run (portable to
// Windows without shell scripts): with TIEGEL_RUN_HELPER set, TestMain
// acts as that program instead of running the tests.
const helperEnv = "TIEGEL_RUN_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(helper(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func helper(mode string, args []string) int {
	switch mode {
	case "echo":
		for _, a := range args {
			fmt.Println(a)
		}
		fmt.Fprintln(os.Stderr, "err:"+os.Getenv("FOO"))
		code, _ := strconv.Atoi(os.Getenv("TIEGEL_RUN_EXIT"))
		return code
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
		return 0
	case "sleep":
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
		return 0
	case "tree":
		// A child that keeps the inherited stdout open: only a tree kill
		// ends the run quickly.
		self, _ := os.Executable()
		c := exec.Command(self)
		c.Env = append(os.Environ(), helperEnv+"=sleep")
		c.Stdout = os.Stdout
		if err := c.Start(); err != nil {
			fmt.Println("child:", err)
			return 3
		}
		time.Sleep(60 * time.Second)
		return 0
	case "long":
		fmt.Println(strings.Repeat("x", 200000))
		fmt.Print("partial")
		return 0
	case "special":
		fmt.Print("10%\r50%\r100%\n")
		fmt.Print("\x1b[32mgreen\x1b[0m\r\n")
		fmt.Println("")
		fmt.Println("end")
		return 0
	case "pidtree":
		// Reports its PID and a child's (which inherits nothing it
		// could keep the run alive with), then waits.
		self, _ := os.Executable()
		c := exec.Command(self)
		c.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := c.Start(); err != nil {
			fmt.Println("child:", err)
			return 3
		}
		fmt.Printf("pid %d\nchild %d\n", os.Getpid(), c.Process.Pid)
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
		return 0
	case "fakedocker", "fakewsl":
		return fakeRemote(mode, args)
	case "marker":
		fmt.Fprintln(os.Stderr, pidMarker+"4711")
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
		return 0
	}
	return 99
}

// fakeLogEnv names the file a fake docker/wsl.exe appends its argument
// lists to (one JSON array per line).
const fakeLogEnv = "TIEGEL_RUN_FAKE_LOG"

// fakeRemote plays docker (`compose … exec -T [-e K=V]… [-w D] SERVICE
// argv…`, `kill NAME`) or wsl.exe (`-d D [--cd X] -- argv…`): it logs
// its arguments and runs argv on the host, so that the shell scripts
// of the stop helper really run.
func fakeRemote(mode string, args []string) int {
	if f, err := os.OpenFile(os.Getenv(fakeLogEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		data, _ := json.Marshal(args)
		fmt.Fprintln(f, string(data))
		_ = f.Close()
	}
	var env, argv []string
	switch mode {
	case "fakewsl":
		i := slices.Index(args, "--")
		if i < 0 {
			return 98
		}
		argv = args[i+1:]
	default:
		i := slices.Index(args, "exec")
		if i < 0 {
			if slices.Contains(args, "kill") {
				return 0
			}
			return 98
		}
		rest := args[i+1:]
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			switch rest[0] {
			case "-e":
				env = append(env, rest[1])
				rest = rest[2:]
			case "-w":
				rest = rest[2:] // a container path
			default:
				rest = rest[1:]
			}
		}
		if len(rest) < 2 {
			return 98
		}
		argv = rest[1:] // after the service
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = append(os.Environ(), env...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 97
	}
	return 0
}

// fakeCalls returns the argument lists logged by fakeRemote.
func fakeCalls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	var out [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var a []string
		if err := json.Unmarshal([]byte(l), &a); err != nil {
			t.Fatalf("fake log %q: %v", l, err)
		}
		out = append(out, a)
	}
	return out
}

// helperSpec returns a host spec that runs the helper in mode.
func helperSpec(t *testing.T, mode string, args ...string) (Spec, Target) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{Name: mode, Program: self, Args: args, Env: []string{helperEnv + "=" + mode}, Interpreter: InterpreterHost},
		Target{ProjectDir: t.TempDir()}
}

func launcherFor(spec Spec, t Target) Launcher {
	return func(ctx context.Context) (*Launch, error) { return Prepare(ctx, spec, t) }
}
