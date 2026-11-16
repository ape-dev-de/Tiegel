//go:build unix

package watch

import (
	"io/fs"
	"syscall"
)

// fileID returns the inode number (0 if unknown).
func fileID(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino) //nolint:unconvert // int64 on some platforms
	}
	return 0
}
