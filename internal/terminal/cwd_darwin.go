package terminal

import (
	"bytes"
	"unsafe"

	"golang.org/x/sys/unix"
)

// proc_pidinfo(PROC_PIDVNODEPATHINFO) through the proc_info system call
// (libproc's wrapper, without cgo): struct proc_vnodepathinfo starts with
// the current directory, a struct vnode_info (152 bytes) followed by its
// path (MAXPATHLEN bytes).
const (
	procInfoCallPIDInfo  = 2
	procPIDVnodePathInfo = 9
	vnodeInfoSize        = 152
	maxPathLen           = 1024
)

// procCwd returns the working directory of process pid ("" if unknown).
func procCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	var buf [2 * (vnodeInfoSize + maxPathLen)]byte
	n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, procInfoCallPIDInfo, uintptr(pid), procPIDVnodePathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 || int(n) < vnodeInfoSize+1 {
		return ""
	}
	p := buf[vnodeInfoSize : vnodeInfoSize+maxPathLen]
	if i := bytes.IndexByte(p, 0); i >= 0 {
		p = p[:i]
	}
	return string(p)
}
