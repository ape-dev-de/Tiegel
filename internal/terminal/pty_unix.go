//go:build unix

package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/ape-dev-de/tiegel/internal/core/proctree"
)

// unixProcess is a program on the slave side of a pty pair.
type unixProcess struct {
	cmd  *exec.Cmd
	pty  *os.File
	tree *proctree.Tree

	mu sync.Mutex
	// fg is the foreground job seen at the last Hangup (the terminal may
	// be closed when Kill runs).
	fg int
}

// StartPTY starts c in a new pseudo terminal. The program leads a new
// session (and process group) with the terminal as controlling terminal.
func StartPTY(c Command) (Process, error) {
	cmd := exec.Command(c.Program, c.Args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	tree := proctree.New(cmd)
	// pty.Start makes the child a session leader (Setsid), which is also
	// the leader of its process group; Setpgid on a session leader fails
	// (EPERM), so the group of proctree comes from the session instead.
	cmd.SysProcAttr.Setpgid = false
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(clampSize(c.Cols)), Rows: uint16(clampSize(c.Rows))})
	if err != nil {
		return nil, fmt.Errorf("terminal: start %s: %w", c.Program, err)
	}
	tree.Attach()
	return &unixProcess{cmd: cmd, pty: f, tree: tree}, nil
}

func clampSize(n int) int { return max(1, min(n, 9999)) }

// Read returns the output; once every holder of the slave side closed
// it, Linux reports EIO and macOS EOF (the session ends on any error).
func (p *unixProcess) Read(b []byte) (int, error) { return p.pty.Read(b) }

func (p *unixProcess) Write(b []byte) (int, error) { return p.pty.Write(b) }

func (p *unixProcess) Resize(cols, rows int) error {
	return pty.Setsize(p.pty, &pty.Winsize{Cols: uint16(clampSize(cols)), Rows: uint16(clampSize(rows))})
}

func (p *unixProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	p.tree.Release()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, err
}

// foreground returns the process group in the foreground of the
// terminal (a job the shell started under job control has its own
// group), 0 if unknown or the shell's own.
func (p *unixProcess) foreground() int {
	if p.cmd.Process == nil {
		return 0
	}
	rc, err := p.pty.SyscallConn()
	if err != nil {
		return 0
	}
	pgrp := 0
	// Control keeps the descriptor non-blocking (unlike Fd), so a
	// blocked Read still ends when the terminal closes.
	_ = rc.Control(func(fd uintptr) {
		if v, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil {
			pgrp = v
		}
	})
	if pgrp <= 1 || pgrp == p.cmd.Process.Pid {
		return 0
	}
	return pgrp
}

// Hangup sends SIGHUP to the shell's process group and to the
// foreground job, like closing a terminal window.
func (p *unixProcess) Hangup() error {
	if p.cmd.Process == nil {
		return nil
	}
	if fg := p.foreground(); fg > 0 {
		p.mu.Lock()
		p.fg = fg
		p.mu.Unlock()
		_ = syscall.Kill(-fg, syscall.SIGHUP)
	}
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminal: hangup: %w", err)
	}
	return nil
}

// Kill ends the foreground job and the shell's process group, also
// after the shell itself exited (members of the group may be left).
func (p *unixProcess) Kill() error {
	p.mu.Lock()
	last := p.fg
	p.mu.Unlock()
	for _, fg := range []int{p.foreground(), last} {
		if fg > 0 {
			_ = syscall.Kill(-fg, syscall.SIGKILL)
		}
	}
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.tree.Kill(); err != nil {
		return err
	}
	// After Wait the tree is released; the group may still have members.
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("terminal: kill: %w", err)
	}
	return nil
}

func (p *unixProcess) Close() error { return p.pty.Close() }

func (p *unixProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Foreground returns the name of the job in the foreground of the
// terminal when it is not the shell ("" otherwise).
func (p *unixProcess) Foreground() string {
	fg := p.foreground()
	if fg <= 0 {
		return ""
	}
	return procName(fg)
}
