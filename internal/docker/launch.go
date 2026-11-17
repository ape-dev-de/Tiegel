package docker

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// External programs for the Services window: a terminal running a shell
// inside a container (until the embedded terminal of M15.5 replaces it)
// and the browser for published ports. Both are started only on a user
// action.

// TerminalOptions describe how to open an external terminal.
type TerminalOptions struct {
	// GOOS selects the platform ("darwin", "linux", "windows", …).
	GOOS string
	// Terminal is the user's choice (setting docker.terminal): on macOS
	// the application for `open -a` (default "Terminal", e.g. "iTerm");
	// elsewhere a command prefix the docker command is appended to (e.g.
	// "kitty", "alacritty -e", "wezterm start --", "wt"). Empty picks a
	// default.
	Terminal string
	// Getenv reads $TERMINAL (Linux); nil means os.Getenv.
	Getenv func(string) string
	// LookPath finds programs; nil means "found" for every name.
	LookPath func(string) (string, error)
	// TempDir receives the macOS launcher script; empty means os.TempDir.
	TempDir string
	// Env are KEY=VALUE entries exported in the macOS launcher script
	// (typically PATH, so the compose plugin is found).
	Env []string
}

// ErrNoTerminal reports that no terminal emulator was found.
var ErrNoTerminal = errors.New("docker: no terminal emulator found (set docker.terminal)")

// TerminalCommand returns the command that opens a terminal window
// running argv. On macOS it writes a self-deleting launcher script
// (*.command) into TempDir and opens it with the chosen application.
func TerminalCommand(o TerminalOptions, argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("docker: empty terminal command")
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	look := func(name string) bool {
		if o.LookPath == nil {
			return true
		}
		_, err := o.LookPath(name)
		return err == nil
	}
	choice := strings.TrimSpace(o.Terminal)
	switch o.GOOS {
	case "darwin":
		app := choice
		if app == "" {
			app = "Terminal"
		}
		script, err := writeLauncher(o.TempDir, o.Env, argv)
		if err != nil {
			return nil, err
		}
		return []string{"open", "-a", app, script}, nil
	case "windows":
		if choice != "" {
			prefix := strings.Fields(choice)
			if isWT(prefix[0]) {
				argv = wtEscape(argv)
			}
			return append(prefix, argv...), nil
		}
		// cmd.exe only (D307): `wt` re-splits its command line at ";" and
		// `cmd /c start "" cmd /k` parses it twice. The launcher starts
		// this argv verbatim in a new console (WindowsRawCmd), which the
		// default terminal application of Windows hosts.
		line, err := CmdLine(argv)
		if err != nil {
			return nil, err
		}
		return append(append([]string(nil), cmdPrefix...), `"`+line+`"`), nil
	}
	if choice != "" {
		return append(strings.Fields(choice), argv...), nil
	}
	if t := strings.TrimSpace(getenv("TERMINAL")); t != "" {
		return append(append(strings.Fields(t), "-e"), argv...), nil
	}
	for _, c := range []struct {
		name string
		flag string
	}{{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"konsole", "-e"}, {"xfce4-terminal", "-x"}, {"kitty", ""}, {"alacritty", "-e"}, {"xterm", "-e"}} {
		if look(c.name) {
			out := []string{c.name}
			if c.flag != "" {
				out = append(out, c.flag)
			}
			return append(out, argv...), nil
		}
	}
	return nil, ErrNoTerminal
}

// cmdPrefix starts cmd.exe without AutoRun (/d), without delayed
// expansion (/v:off) and with /s: it strips exactly the outer quotes of
// the line after /k and runs the rest as typed.
var cmdPrefix = []string{"cmd", "/d", "/v:off", "/s", "/k"}

// WindowsRawCmd reports whether argv is a cmd.exe command returned by
// TerminalCommand for Windows: its elements are already quoted for
// cmd.exe, so a launcher must join them with spaces verbatim (as the
// command line, without per-argument quoting) and open a new console.
func WindowsRawCmd(argv []string) bool {
	if len(argv) != len(cmdPrefix)+1 {
		return false
	}
	for i, a := range cmdPrefix {
		if argv[i] != a {
			return false
		}
	}
	return true
}

// cmdMeta are the characters cmd.exe interprets outside of quotes.
const cmdMeta = `()%!^"<>&|`

// CmdLine returns argv as a line for `cmd /s /k "<line>"` that runs
// argv[0] with exactly argv[1:] as its arguments. The program is quoted
// (cmd needs real quotes to find a path with spaces); every argument is
// quoted for CommandLineToArgvW and each cmd.exe metacharacter in it,
// quotes included, is escaped with ^. Escaped quotes do not switch cmd
// into quote mode, so &, |, < and > stay literal, and %VAR% becomes
// ^%VAR^%: cmd looks up the variable "VAR^", finds none and leaves the
// text, then removes the carets.
func CmdLine(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("docker: empty command")
	}
	prog := argv[0]
	if prog == "" || strings.ContainsAny(prog, "\"%\r\n\x00") {
		return "", fmt.Errorf("docker: program path %q cannot be started by cmd.exe", prog)
	}
	var b strings.Builder
	b.WriteString(`"` + prog + `"`)
	for _, a := range argv[1:] {
		if strings.ContainsAny(a, "\r\n\x00") {
			return "", fmt.Errorf("docker: argument %q cannot be passed through cmd.exe", a)
		}
		b.WriteByte(' ')
		for _, r := range WindowsQuote(a) {
			if strings.ContainsRune(cmdMeta, r) {
				b.WriteByte('^')
			}
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}

// WindowsQuote quotes s as one argument for CommandLineToArgvW (and the
// Go and MSVC runtimes), like syscall.EscapeArg but on every platform.
func WindowsQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			// Backslashes before a quote are doubled, the quote escaped.
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	// Backslashes before the closing quote are doubled.
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// isWT reports whether program is Windows Terminal.
func isWT(program string) bool {
	base := strings.ToLower(program)
	if i := strings.LastIndexAny(base, `\/`); i >= 0 {
		base = base[i+1:]
	}
	return base == "wt" || base == "wt.exe"
}

// wtEscape escapes ";" (Windows Terminal's command separator) as `\;`.
func wtEscape(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = strings.ReplaceAll(a, ";", `\;`)
	}
	return out
}

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,+@%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeLauncher writes a shell script that removes itself and runs argv.
func writeLauncher(dir string, env, argv []string) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "tiegel-shell-*.command")
	if err != nil {
		return "", fmt.Errorf("docker: launcher script: %w", err)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\nrm -f \"$0\"\n")
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t\n'\"$;=") {
			continue
		}
		fmt.Fprintf(&b, "export %s=%s\n", k, ShellQuote(v))
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = ShellQuote(a)
	}
	b.WriteString("exec " + strings.Join(quoted, " ") + "\n")
	_, werr := f.WriteString(b.String())
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o700)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("docker: launcher script: %w", werr)
	}
	return filepath.Clean(f.Name()), nil
}

// CheckURL accepts only absolute http:// and https:// URLs.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("docker: url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("docker: only http:// and https:// URLs can be opened, got %q", raw)
	}
	return u.String(), nil
}

// BrowserCommand returns the command that opens rawURL (http/https only)
// in the default browser of goos.
func BrowserCommand(goos, rawURL string) ([]string, error) {
	u, err := CheckURL(rawURL)
	if err != nil {
		return nil, err
	}
	switch goos {
	case "darwin":
		return []string{"open", u}, nil
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", u}, nil
	}
	return []string{"xdg-open", u}, nil
}
