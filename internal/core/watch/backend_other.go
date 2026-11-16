//go:build !darwin

package watch

// autoBackend: fsnotify (inotify on Linux, kqueue on the BSDs; on Windows
// one recursive ReadDirectoryChangesW, see rdcw_windows.go).
func autoBackend() Backend { return FSNotify }
