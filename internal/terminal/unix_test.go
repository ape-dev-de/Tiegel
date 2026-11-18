//go:build unix

package terminal

import "syscall"

// processAlive reports whether pid exists (a zombie counts until its
// parent, after a kill usually init, reaps it).
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
