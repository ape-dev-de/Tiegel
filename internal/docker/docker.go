// Package docker drives the docker CLI for Tiegel: it finds the docker
// binary (GUI apps on macOS do not see the Homebrew/OrbStack PATH), lists
// Docker contexts and detects the engine behind them (OrbStack, Docker
// Desktop, Colima, Rancher Desktop, Podman, DOCKER_HOST), checks whether an
// engine is reachable and provides the "Docker image" PHP interpreter
// (`docker run --rm -v <project>:<remote> …`).
//
// The package never talks to an engine on its own: every function that
// runs docker is called for a user action or an execution (privacy, see
// docs/DECISIONS.md D230). `docker context ls` only reads the local CLI
// configuration.
//
// It does not import Gio.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Client runs the docker CLI with a fixed binary, context and environment.
// The zero value runs "docker" from PATH with docker's current context.
type Client struct {
	// Binary is the docker executable; empty means "docker" from PATH.
	Binary string
	// Context is passed as --context; empty means docker's own choice
	// (DOCKER_CONTEXT, DOCKER_HOST or the CLI's current context).
	Context string
	// Env holds extra environment entries (KEY=VALUE) that override the
	// process environment, typically PATH (see EnvPath) so that compose
	// plugins and credential helpers next to the binary are found.
	Env []string
}

// Bin returns the docker executable.
func (c Client) Bin() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "docker"
}

// Args returns args prefixed with the global options of c (--context).
func (c Client) Args(args ...string) []string {
	out := make([]string, 0, len(args)+2)
	if c.Context != "" {
		out = append(out, "--context", c.Context)
	}
	return append(out, args...)
}

// Command builds (but does not start) `docker <global options> args…`.
func (c Client) Command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Bin(), c.Args(args...)...)
	if len(c.Env) > 0 {
		cmd.Env = MergeEnv(os.Environ(), c.Env)
	}
	return cmd
}

// Output runs docker with args and returns standard output. A failure is
// returned with the first line of standard error appended; ctx bounds the
// run.
func (c Client) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := c.Command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%w)", ctx.Err(), err)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, firstLine(msg))
		}
		return stdout.Bytes(), fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// WithContext returns a copy of c using the context name.
func (c Client) WithContext(name string) Client {
	c.Context = name
	c.Env = append([]string(nil), c.Env...)
	return c
}

// MergeEnv returns base with the entries of override replacing entries of
// the same key.
func MergeEnv(base, override []string) []string {
	keys := map[string]bool{}
	for _, kv := range override {
		k, _, _ := strings.Cut(kv, "=")
		keys[envKey(k)] = true
	}
	out := make([]string, 0, len(base)+len(override))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !keys[envKey(k)] {
			out = append(out, kv)
		}
	}
	return append(out, override...)
}

// ErrNotFound reports that no docker binary was found.
var ErrNotFound = errors.New("docker: binary not found")

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
