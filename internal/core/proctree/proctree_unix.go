//go:build unix

package proctree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type sysTree struct{}

func prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func attach(*os.Process) sysTree { return sysTree{} }

func kill(p *os.Process, _ sysTree) error {
	// The child leads its own group (Setpgid), so -pid is the group. The
	// group outlives a leader that already exited as long as members are
	// left, so this also reaches orphaned grandchildren.
	gerr := syscall.Kill(-p.Pid, syscall.SIGKILL)
	perr := killProcess(p)
	if gerr != nil && !errors.Is(gerr, syscall.ESRCH) && perr != nil {
		return fmt.Errorf("proctree: kill %d: %w", p.Pid, errors.Join(gerr, perr))
	}
	return nil
}

func release(sysTree) {}
