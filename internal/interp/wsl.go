package interp

import (
	"context"
	"os/exec"
)

// WSL is a placeholder for a PHP interpreter inside a WSL distribution. It
// satisfies Interpreter so settings and pickers can already list it; every
// operation returns ErrNotImplemented until milestone M9.
type WSL struct {
	// Distro is the WSL distribution name, e.g. "Ubuntu-24.04".
	Distro string
	// Path is the php binary inside the distribution, e.g. "/usr/bin/php".
	Path string
}

var _ Interpreter = (*WSL)(nil)

// ID implements Interpreter.
func (w *WSL) ID() string { return "wsl:" + w.Distro + ":" + w.Path }

// Label implements Interpreter.
func (w *WSL) Label() string { return "WSL " + w.Distro + " (" + w.Path + ")" }

// Kind implements Interpreter.
func (w *WSL) Kind() Kind { return KindWSL }

// Version implements Interpreter.
func (w *WSL) Version(context.Context) (string, error) { return "", ErrNotImplemented }

// Modules implements Interpreter.
func (w *WSL) Modules(context.Context) ([]string, error) { return nil, ErrNotImplemented }

// Ini implements Interpreter.
func (w *WSL) Ini(context.Context) (IniValues, error) { return nil, ErrNotImplemented }

// Command implements Interpreter.
func (w *WSL) Command(context.Context, ...string) (*exec.Cmd, error) {
	return nil, ErrNotImplemented
}
