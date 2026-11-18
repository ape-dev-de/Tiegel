package terminal

import (
	"errors"
	"io"
)

// Command is a program to start in a pseudo terminal.
type Command struct {
	Program string
	Args    []string
	Dir     string
	// Env is the complete environment ("K=V").
	Env        []string
	Cols, Rows int
}

// Process is a program running in a pseudo terminal. Read returns its
// output (io.EOF or an error once the terminal closed), Write sends
// input.
type Process interface {
	io.ReadWriter
	// Resize changes the terminal size (the program gets SIGWINCH).
	Resize(cols, rows int) error
	// Wait waits for the program to exit and returns its exit code
	// (-1 when it was killed by a signal).
	Wait() (int, error)
	// Hangup asks the program to end (SIGHUP to its process group on
	// Unix; Windows ends it like Kill).
	Hangup() error
	// Kill ends the program and everything it started.
	Kill() error
	// Close closes the terminal (after Wait); further reads fail.
	Close() error
	// Pid returns the process ID.
	Pid() int
}

// Starter starts a Command in a pseudo terminal; StartPTY is the real
// one, tests use fakes.
type Starter func(c Command) (Process, error)

// ErrUnsupported is returned where no pseudo terminal is available.
var ErrUnsupported = errors.New("terminal: pseudo terminals are not supported on this platform")
