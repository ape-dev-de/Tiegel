//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ape-dev-de/tiegel/internal/core/proctree"
)

// winProcess is a program attached to a Windows pseudo console (ConPTY,
// Windows 10 1809 and later).
type winProcess struct {
	hpc     windows.Handle
	in, out *os.File
	proc    windows.Handle
	pid     int
	tree    *proctree.Tree

	closeOnce sync.Once
}

// StartPTY starts c attached to a new pseudo console. Older Windows
// versions without ConPTY get an error (the session cannot open).
func StartPTY(c Command) (Process, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("terminal: pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		closeHandles(inR, inW)
		return nil, fmt.Errorf("terminal: pipe: %w", err)
	}
	var hpc windows.Handle
	size := windows.Coord{X: int16(clampSize(c.Cols)), Y: int16(clampSize(c.Rows))}
	if err := windows.CreatePseudoConsole(size, inR, outW, 0, &hpc); err != nil {
		closeHandles(inR, inW, outR, outW)
		return nil, fmt.Errorf("terminal: pseudo console (needs Windows 10 1809 or later): %w", err)
	}
	// The pseudo console holds its own duplicates.
	closeHandles(inR, outW)
	p := &winProcess{hpc: hpc, in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out")}
	if err := p.start(c); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = p.in.Close()
		_ = p.out.Close()
		return nil, err
	}
	return p, nil
}

func (p *winProcess) start(c Command) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fmt.Errorf("terminal: attribute list: %w", err)
	}
	defer attrs.Delete()
	// The attribute value is the HPCON itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&p.hpc)), unsafe.Sizeof(p.hpc)); err != nil {
		return fmt.Errorf("terminal: attribute: %w", err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{c.Program}, c.Args...)))
	if err != nil {
		return fmt.Errorf("terminal: command line: %w", err)
	}
	var dir *uint16
	if c.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(c.Dir); err != nil {
			return fmt.Errorf("terminal: directory: %w", err)
		}
	}
	env := envBlock(c.Env)
	var envp *uint16
	if len(env) > 0 {
		envp = &env[0]
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(nil, line, nil, nil, false, flags, envp, dir, &si.StartupInfo, &pi); err != nil {
		return fmt.Errorf("terminal: start %s: %w", c.Program, err)
	}
	_ = windows.CloseHandle(pi.Thread)
	p.proc, p.pid = pi.Process, int(pi.ProcessId)
	// proctree works on an exec.Cmd: wrap the started process.
	cmd := &exec.Cmd{}
	p.tree = proctree.New(cmd)
	if op, err := os.FindProcess(p.pid); err == nil {
		cmd.Process = op
		p.tree.Attach()
	}
	return nil
}

// envBlock encodes env as a Windows environment block (NUL separated,
// double NUL terminated); nil means "inherit".
func envBlock(env []string) []uint16 {
	if len(env) == 0 {
		return nil
	}
	var b []uint16
	for _, kv := range env {
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	return append(b, 0)
}

func closeHandles(hs ...windows.Handle) {
	for _, h := range hs {
		_ = windows.CloseHandle(h)
	}
}

func clampSize(n int) int { return max(1, min(n, 9999)) }

func (p *winProcess) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *winProcess) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *winProcess) Resize(cols, rows int) error {
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(clampSize(cols)), Y: int16(clampSize(rows))})
}

func (p *winProcess) Wait() (int, error) {
	if _, err := windows.WaitForSingleObject(p.proc, windows.INFINITE); err != nil {
		return -1, fmt.Errorf("terminal: wait: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.proc, &code); err != nil {
		return -1, fmt.Errorf("terminal: exit code: %w", err)
	}
	if p.tree != nil {
		p.tree.Release()
	}
	return int(code), nil
}

func (p *winProcess) Hangup() error { return p.Kill() }

func (p *winProcess) Kill() error {
	if p.tree == nil {
		return windows.TerminateProcess(p.proc, 1)
	}
	return p.tree.Kill()
}

func (p *winProcess) Close() error {
	var err error
	p.closeOnce.Do(func() {
		// Closing the pseudo console ends the output stream.
		windows.ClosePseudoConsole(p.hpc)
		err = errors.Join(p.in.Close(), p.out.Close(), windows.CloseHandle(p.proc))
	})
	return err
}

func (p *winProcess) Pid() int { return p.pid }
