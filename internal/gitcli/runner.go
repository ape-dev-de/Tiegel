package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrGitNotFound is returned when no git executable can be found.
var ErrGitNotFound = errors.New("gitcli: git executable not found")

// Error describes a failed git invocation.
type Error struct {
	Args     []string // git arguments (without the global options)
	ExitCode int      // process exit code, -1 if the process did not run
	Stderr   string   // trimmed standard error output
	Err      error    // underlying error from os/exec or the context
}

// Error implements the error interface.
func (e *Error) Error() string {
	cmd := "git"
	if len(e.Args) > 0 {
		cmd += " " + e.Args[0]
	}
	if e.Stderr != "" {
		return fmt.Sprintf("%s: %s", cmd, firstLines(e.Stderr, 3))
	}
	return fmt.Sprintf("%s: %v", cmd, e.Err)
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

// runOpts configures a single git invocation.
type runOpts struct {
	stdin   []byte
	env     []string // additional KEY=VALUE pairs
	okCodes []int    // additional exit codes treated as success
	dir     string   // working directory override
}

// globalArgs are passed before every git sub command.
var globalArgs = []string{
	"--no-pager",
	"-c", "core.quotepath=off",
	"-c", "color.ui=false",
	"-c", "advice.detachedHead=false",
}

// baseEnv returns the process environment for git with the package's
// fixed overrides applied.
func baseEnv(extra []string) []string {
	env := os.Environ()
	set := map[string]string{
		"LC_ALL":                "C",
		"LANG":                  "C",
		"GIT_TERMINAL_PROMPT":   "0",
		"GIT_PAGER":             "cat",
		"PAGER":                 "cat",
		"GIT_EDITOR":            ":",
		"GIT_LITERAL_PATHSPECS": "1",
		"GIT_OPTIONAL_LOCKS":    "0",
	}
	if os.Getenv("GIT_SSH_COMMAND") == "" && os.Getenv("GIT_SSH") == "" {
		set["GIT_SSH_COMMAND"] = "ssh -o BatchMode=yes"
	}
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, override := set[k]; override {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return append(out, extra...)
}

// gitExe resolves the git binary once per call; exec.LookPath caches nothing
// but is cheap compared to running git.
func gitExe() (string, error) {
	p, err := exec.LookPath("git")
	if err != nil {
		return "", ErrGitNotFound
	}
	return p, nil
}

// runGit executes git in dir and returns stdout.
func runGit(ctx context.Context, dir string, o runOpts, args ...string) ([]byte, error) {
	exe, err := gitExe()
	if err != nil {
		return nil, err
	}
	full := make([]string, 0, len(globalArgs)+len(args))
	full = append(full, globalArgs...)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, exe, full...)
	cmd.Dir = dir
	if o.dir != "" {
		cmd.Dir = o.dir
	}
	cmd.Env = baseEnv(o.env)
	cmd.WaitDelay = 2 * time.Second
	if o.stdin != nil {
		cmd.Stdin = bytes.NewReader(o.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	gerr := &Error{Args: args, ExitCode: -1, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	if ctxErr := ctx.Err(); ctxErr != nil {
		gerr.Err = ctxErr
		return stdout.Bytes(), gerr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		gerr.ExitCode = exitErr.ExitCode()
		for _, c := range o.okCodes {
			if c == gerr.ExitCode {
				return stdout.Bytes(), nil
			}
		}
	}
	return stdout.Bytes(), gerr
}

// exitCode returns the git exit code of err or -1.
func exitCode(err error) int {
	var gerr *Error
	if errors.As(err, &gerr) {
		return gerr.ExitCode
	}
	return -1
}

// stderrOf returns the stderr of a git error or "".
func stderrOf(err error) string {
	var gerr *Error
	if errors.As(err, &gerr) {
		return gerr.Stderr
	}
	return ""
}
