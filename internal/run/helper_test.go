package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	case "marker":
		fmt.Fprintln(os.Stderr, pidMarker+"4711")
		fmt.Println("ready")
		time.Sleep(60 * time.Second)
		return 0
	}
	return 99
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
