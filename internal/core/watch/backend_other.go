//go:build !darwin

package watch

// autoBackend: fsnotify (inotify on Linux, ReadDirectoryChangesW on
// Windows, kqueue on the BSDs).
func autoBackend() Backend { return FSNotify }
