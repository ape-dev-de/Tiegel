package terminal

import (
	"os/exec"
	"runtime"
	"strings"
)

// DefaultShell returns the shell for a new session when the setting is
// empty: $SHELL, else /bin/zsh, /bin/bash or /bin/sh (the first that
// exists); on Windows PowerShell if found, else %ComSpec% (cmd.exe).
func DefaultShell(goos string, getenv func(string) string, lookPath func(string) (string, error)) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if goos == "windows" {
		for _, p := range []string{"pwsh.exe", "powershell.exe"} {
			if full, err := lookPath(p); err == nil {
				return full
			}
		}
		if cs := getenv("ComSpec"); cs != "" {
			return cs
		}
		return "cmd.exe"
	}
	if sh := getenv("SHELL"); sh != "" {
		return sh
	}
	for _, p := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if full, err := lookPath(p); err == nil {
			return full
		}
	}
	return "/bin/sh"
}

// DefaultArgs returns the arguments of shell when the setting gives
// none: bash, zsh and fish start as login shells (-l) so that the
// profile sets up PATH like in a terminal application.
func DefaultArgs(shell string) []string {
	if i := strings.LastIndexAny(shell, `/\`); i >= 0 {
		shell = shell[i+1:]
	}
	base := strings.TrimSuffix(strings.ToLower(shell), ".exe")
	switch base {
	case "bash", "zsh", "fish":
		return []string{"-l"}
	case "pwsh", "powershell":
		return []string{"-NoLogo"}
	}
	return nil
}

// Env returns the environment of a session: base (usually os.Environ)
// with PATH replaced by path (when not empty), TERM=xterm-256color,
// COLORTERM=truecolor, TERM_PROGRAM=Tiegel, a UTF-8 LANG when no locale
// is set (GUI apps on macOS start without one), then extra ("K=V",
// overriding).
func Env(base []string, path string, extra []string) []string {
	keyOf := func(kv string) string {
		k, _, _ := strings.Cut(kv, "=")
		if runtime.GOOS == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	vals := map[string]int{}
	var out []string
	set := func(kv string) {
		k := keyOf(kv)
		if i, ok := vals[k]; ok {
			out[i] = kv
			return
		}
		vals[k] = len(out)
		out = append(out, kv)
	}
	for _, kv := range base {
		if strings.Contains(kv, "=") {
			set(kv)
		}
	}
	if path != "" {
		name := "PATH"
		if runtime.GOOS == "windows" {
			name = "Path"
		}
		set(name + "=" + path)
	}
	set("TERM=xterm-256color")
	set("COLORTERM=truecolor")
	set("TERM_PROGRAM=Tiegel")
	if runtime.GOOS != "windows" {
		hasLocale := false
		for _, k := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
			if _, ok := vals[k]; ok {
				hasLocale = true
			}
		}
		if !hasLocale {
			set("LANG=en_US.UTF-8")
		}
	}
	for _, kv := range extra {
		if strings.Contains(kv, "=") {
			set(kv)
		}
	}
	return out
}
