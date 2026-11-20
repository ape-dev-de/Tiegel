package terminal

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// procName returns the command name of process pid ("" if unknown).
func procName(pid int) string {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	c := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(c, 0); i >= 0 {
		c = c[:i]
	}
	return string(c)
}
