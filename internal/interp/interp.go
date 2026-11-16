// Package interp abstracts PHP interpreters: a local binary, a service in a
// Docker Compose project or a PHP inside a WSL distribution. All of them are driven
// through the same Interpreter interface, so callers such as the version
// detection or the debugger do not care where PHP actually runs.
package interp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Kind tells where an interpreter runs.
type Kind string

// Known interpreter kinds.
const (
	KindLocal   Kind = "local"
	KindCompose Kind = "compose"
	KindWSL     Kind = "wsl"
)

// ErrNotImplemented is returned by interpreter operations that are not
// supported by an interpreter type.
var ErrNotImplemented = errors.New("interp: not implemented")

// Interpreter is a PHP runtime that can execute commands.
type Interpreter interface {
	// ID is a stable identifier, suitable as a settings key.
	ID() string
	// Label is a short human readable name for pickers.
	Label() string
	// Kind tells where the interpreter runs.
	Kind() Kind
	// Version returns PHP_VERSION, e.g. "8.3.4".
	Version(ctx context.Context) (string, error)
	// Modules returns the loaded extensions (`php -m`), sorted and deduplicated.
	Modules(ctx context.Context) ([]string, error)
	// Ini returns the values of IniKeys that are known to the interpreter.
	Ini(ctx context.Context) (IniValues, error)
	// Command builds a command that runs php with the given arguments.
	// The command is not started.
	Command(ctx context.Context, args ...string) (*exec.Cmd, error)
}

// IniValues maps ini directive names to their current values. Directives the
// interpreter does not know (e.g. xdebug.* without Xdebug) are absent.
type IniValues map[string]string

// IniKeys are the directives reported by Interpreter.Ini.
var IniKeys = []string{
	"xdebug.mode",
	"xdebug.client_host",
	"xdebug.client_port",
	"xdebug.start_with_request",
}

// Run executes php with args on i and returns its standard output. A non-zero
// exit status is returned as an error that includes standard error.
func Run(ctx context.Context, i Interpreter, args ...string) ([]byte, error) {
	cmd, err := i.Command(ctx, args...)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return stdout.Bytes(), fmt.Errorf("interp: run php %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("interp: run php %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// QueryVersion runs `php -r 'echo PHP_VERSION;'` on i.
func QueryVersion(ctx context.Context, i Interpreter) (string, error) {
	out, err := Run(ctx, i, "-r", "echo PHP_VERSION;")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", errors.New("interp: empty PHP_VERSION")
	}
	return v, nil
}

// QueryModules runs `php -m` on i and parses the result.
func QueryModules(ctx context.Context, i Interpreter) ([]string, error) {
	out, err := Run(ctx, i, "-m")
	if err != nil {
		return nil, err
	}
	return ParseModules(out), nil
}

// ParseModules parses the output of `php -m`. Section headers such as
// "[PHP Modules]" are skipped; modules listed twice (Xdebug, OPcache) are
// reported once. The result is sorted case-insensitively.
func ParseModules(out []byte) []string {
	seen := map[string]bool{}
	var mods []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		key := strings.ToLower(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		mods = append(mods, line)
	}
	sort.Slice(mods, func(a, b int) bool { return strings.ToLower(mods[a]) < strings.ToLower(mods[b]) })
	return mods
}

// iniScript prints "key=value" for known directives and "key" alone for
// unknown ones (ini_get returns false).
const iniScript = `foreach ([%s] as $k) { $v = ini_get($k); echo $k, ($v === false ? "" : "=" . $v), "\n"; }`

// QueryIni reads IniKeys from i.
func QueryIni(ctx context.Context, i Interpreter) (IniValues, error) {
	quoted := make([]string, len(IniKeys))
	for n, k := range IniKeys {
		quoted[n] = "'" + k + "'"
	}
	out, err := Run(ctx, i, "-r", fmt.Sprintf(iniScript, strings.Join(quoted, ",")))
	if err != nil {
		return nil, err
	}
	return ParseIni(out), nil
}

// ParseIni parses "key=value" lines. Lines without "=" mark unknown
// directives and are skipped.
func ParseIni(out []byte) IniValues {
	vals := IniValues{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		vals[strings.TrimSpace(k)] = v
	}
	return vals
}
