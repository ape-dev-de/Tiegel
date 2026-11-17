//go:build windows

package proctree

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sysTree holds the Job Object of the tree (0: none, use taskkill).
type sysTree struct {
	job windows.Handle
}

func prepare(*exec.Cmd) {}

// attach puts the process into a new Job Object that kills all members
// when it is closed or terminated. Processes the child starts afterwards
// belong to the job automatically.
func attach(p *os.Process) sysTree {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return sysTree{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return sysTree{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return sysTree{}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return sysTree{}
	}
	return sysTree{job: job}
}

func kill(p *os.Process, s sysTree) error {
	if s.job != 0 {
		if err := windows.TerminateJobObject(s.job, 1); err == nil {
			return nil
		}
	}
	// Fallback: taskkill walks the tree by parent process ID.
	tk := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	tk.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
	terr := tk.Run()
	if err := killProcess(p); err != nil && terr != nil {
		return fmt.Errorf("proctree: kill %d: %w", p.Pid, err)
	}
	return nil
}

func release(s sysTree) {
	if s.job != 0 {
		_ = windows.CloseHandle(s.job)
	}
}
