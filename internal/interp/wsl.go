package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf16"
)

// DefaultWSLProgram is the Windows launcher for WSL distributions.
const DefaultWSLProgram = "wsl.exe"

// CommandRunner runs an external program and returns its standard output.
// It is injectable so that WSL detection can be tested on any OS.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner is the default CommandRunner: it runs the program with os/exec
// and returns its standard output; standard error is part of the error.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(DecodeWSLOutput(stderr.Bytes())); msg != "" {
			return out, fmt.Errorf("interp: %s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return out, fmt.Errorf("interp: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// WSL is a PHP interpreter inside a WSL distribution. Commands run as
// `wsl.exe -d <Distro> -- <Path> args...`; paths passed as arguments must
// already be WSL paths (see WindowsToWSL).
type WSL struct {
	// Distro is the WSL distribution name, e.g. "Ubuntu-24.04".
	Distro string
	// Path is the php binary inside the distribution, e.g. "/usr/bin/php".
	Path string
	// Program is the WSL launcher; empty means DefaultWSLProgram. Tests
	// point it to a fake script.
	Program string
}

var _ Interpreter = (*WSL)(nil)

// NewWSL returns a WSL interpreter for php at path (empty: "php") in distro.
func NewWSL(distro, path string) *WSL {
	if path == "" {
		path = "php"
	}
	return &WSL{Distro: distro, Path: path}
}

// ID implements Interpreter.
func (w *WSL) ID() string { return "wsl:" + w.Distro + ":" + w.Path }

// Label implements Interpreter.
func (w *WSL) Label() string { return "WSL " + w.Distro + " (" + w.Path + ")" }

// Kind implements Interpreter.
func (w *WSL) Kind() Kind { return KindWSL }

// Version implements Interpreter.
func (w *WSL) Version(ctx context.Context) (string, error) { return QueryVersion(ctx, w) }

// Modules implements Interpreter.
func (w *WSL) Modules(ctx context.Context) ([]string, error) { return QueryModules(ctx, w) }

// Ini implements Interpreter.
func (w *WSL) Ini(ctx context.Context) (IniValues, error) { return QueryIni(ctx, w) }

// Args returns the launcher arguments for running php with args:
// ["-d", distro, "--", php, args...].
func (w *WSL) Args(args ...string) []string {
	php := w.Path
	if php == "" {
		php = "php"
	}
	out := make([]string, 0, len(args)+4)
	out = append(out, "-d", w.Distro, "--", php)
	return append(out, args...)
}

// Command implements Interpreter.
func (w *WSL) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if w.Distro == "" {
		return nil, errors.New("interp: WSL interpreter without distribution")
	}
	return exec.CommandContext(ctx, w.program(), w.Args(args...)...), nil
}

func (w *WSL) program() string {
	if w.Program != "" {
		return w.Program
	}
	return DefaultWSLProgram
}

// DecodeWSLOutput decodes output of wsl.exe. wsl.exe writes its own messages
// (e.g. `wsl.exe -l -q`) as UTF-16LE, with or without byte order mark, while
// commands running inside a distribution write UTF-8. UTF-16 is detected by
// a BOM or by NUL bytes in odd positions; NUL padding and "\r" are removed.
func DecodeWSLOutput(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return clean(decodeUTF16LE(b[2:]))
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return clean(string(b[3:]))
	}
	if looksUTF16LE(b) {
		return clean(decodeUTF16LE(b))
	}
	return clean(string(b))
}

func looksUTF16LE(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	zeros, pairs := 0, len(b)/2
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			zeros++
		}
	}
	return zeros*2 >= pairs
}

func decodeUTF16LE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.ReplaceAll(s, "\r", "")
}

// ParseDistros parses the decoded output of `wsl.exe -l -q`: one name per
// line, empty lines skipped. Docker Desktop's internal distributions
// ("docker-desktop", "docker-desktop-data") are skipped unless withDocker.
func ParseDistros(out string, withDocker bool) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if name == "" {
			continue
		}
		if !withDocker && strings.HasPrefix(strings.ToLower(name), "docker-desktop") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// WSLOptions configure ListDistros and DiscoverWSL.
type WSLOptions struct {
	// Run executes commands; nil means ExecRunner.
	Run CommandRunner
	// Program is the WSL launcher; empty means DefaultWSLProgram.
	Program string
	// GOOS is the host OS; empty means runtime.GOOS. Discovery only runs
	// on Windows unless Run is injected.
	GOOS string
	// WithDocker includes Docker Desktop's internal distributions.
	WithDocker bool
}

func (o WSLOptions) runner() CommandRunner {
	if o.Run != nil {
		return o.Run
	}
	return ExecRunner
}

func (o WSLOptions) program() string {
	if o.Program != "" {
		return o.Program
	}
	return DefaultWSLProgram
}

// enabled reports whether WSL discovery makes sense: on Windows, or with an
// injected runner (tests, other hosts that proxy wsl.exe).
func (o WSLOptions) enabled() bool {
	goos := o.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	return goos == "windows" || o.Run != nil
}

// ListDistros returns the installed WSL distributions (`wsl.exe -l -q`).
func ListDistros(ctx context.Context, opts WSLOptions) ([]string, error) {
	out, err := opts.runner()(ctx, opts.program(), "-l", "-q")
	if err != nil {
		return nil, fmt.Errorf("interp: list WSL distributions: %w", err)
	}
	return ParseDistros(DecodeWSLOutput(out), opts.WithDocker), nil
}

// findPHPScript prints the absolute path of php inside a distribution.
const findPHPScript = "command -v php"

// DiscoverWSL finds php in every WSL distribution. Distributions without php
// are skipped. Off Windows it returns nil unless opts.Run is injected; a
// failing distribution list returns the error.
func DiscoverWSL(ctx context.Context, opts WSLOptions) ([]*WSL, error) {
	if !opts.enabled() {
		return nil, nil
	}
	distros, err := ListDistros(ctx, opts)
	if err != nil {
		return nil, err
	}
	run := opts.runner()
	var found []*WSL
	for _, d := range distros {
		out, err := run(ctx, opts.program(), "-d", d, "--", "sh", "-c", findPHPScript)
		if err != nil {
			continue
		}
		path := firstLine(DecodeWSLOutput(out))
		if !strings.HasPrefix(path, "/") {
			continue
		}
		w := NewWSL(d, path)
		if opts.Program != "" {
			w.Program = opts.Program
		}
		found = append(found, w)
	}
	return found, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// WSL path translation. Windows paths are recognised with drive letters
// ("C:\x", "c:/x"), the long-path prefix ("\\?\C:\x") and the UNC shares of
// a distribution ("\\wsl.localhost\<Distro>\..." and the older
// "\\wsl$\<Distro>\..."). WSLToWindows produces "\\wsl.localhost\" paths,
// which Windows 11 and current Windows 10 builds understand; use
// WSLToWindowsShare for "\\wsl$\".

// WindowsToWSL converts an absolute Windows path to the path seen inside the
// WSL distribution distro: "C:\x\y" → "/mnt/c/x/y", "C:\" → "/mnt/c",
// "\\wsl.localhost\Ubuntu\home\u" → "/home/u". A UNC path of another
// distribution (compared case-insensitively; distro "" accepts any), other
// UNC shares and relative paths are not translatable (false).
func WindowsToWSL(path, distro string) (string, bool) {
	p := strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(p, `\\?\UNC\`) {
		p = `\\` + p[len(`\\?\UNC\`):]
	} else {
		p = strings.TrimPrefix(p, `\\?\`)
	}
	if len(p) >= 2 && p[1] == ':' && isLetter(p[0]) {
		rest := p[2:]
		if rest != "" && rest[0] != '\\' {
			return "", false // drive-relative ("C:foo")
		}
		out := "/mnt/" + strings.ToLower(p[:1])
		if r := joinParts(rest); r != "" {
			out += "/" + r
		}
		return out, true
	}
	if !strings.HasPrefix(p, `\\`) {
		return "", false
	}
	parts := strings.SplitN(p[2:], `\`, 3)
	if len(parts) < 2 {
		return "", false
	}
	host := strings.ToLower(parts[0])
	if host != "wsl$" && host != "wsl.localhost" {
		return "", false
	}
	if parts[1] == "" || (distro != "" && !strings.EqualFold(parts[1], distro)) {
		return "", false
	}
	if len(parts) < 3 {
		return "/", true
	}
	return "/" + joinParts(parts[2]), true
}

// WSLToWindows converts an absolute path inside the WSL distribution distro
// to a Windows path: "/mnt/c/x" → `C:\x`, "/mnt/c" → `C:\`, "/home/u" →
// `\\wsl.localhost\<distro>\home\u`. Relative paths are returned unchanged
// (with "\" separators).
func WSLToWindows(path, distro string) string {
	return wslToWindows(path, distro, "wsl.localhost")
}

// WSLToWindowsShare is WSLToWindows with the older `\\wsl$\` share name.
func WSLToWindowsShare(path, distro string) string {
	return wslToWindows(path, distro, "wsl$")
}

func wslToWindows(path, distro, share string) string {
	if !strings.HasPrefix(path, "/") {
		return strings.ReplaceAll(path, "/", `\`)
	}
	if strings.HasPrefix(path, "/mnt/") {
		rest := path[len("/mnt/"):]
		drive, tail, _ := strings.Cut(rest, "/")
		if len(drive) == 1 && isLetter(drive[0]) {
			out := strings.ToUpper(drive) + `:\`
			return out + strings.Join(slashParts(tail), `\`)
		}
	}
	out := `\\` + share + `\` + distro
	if parts := slashParts(path); len(parts) > 0 {
		out += `\` + strings.Join(parts, `\`)
	}
	return out
}

// joinParts drops empty segments (leading/trailing/double separators) and
// joins the rest with "/". Both separators are accepted.
func joinParts(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' })
	return strings.Join(fields, "/")
}

// slashParts splits a Linux path at "/" and drops empty segments.
func slashParts(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '/' })
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
