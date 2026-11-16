//go:build windows

package watch

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rdcwBackend watches the whole tree with one recursive
// ReadDirectoryChangesW on the root (decision D180). It is what the
// "fsnotify" backend means on Windows.
//
// fsnotify opens one handle per directory there. Windows refuses to rename
// or delete a directory while a handle below it is open ("Access is
// denied"), so per-directory watches made it impossible for the user (and
// for git checkouts) to rename folders inside an open project, and the
// 64 KiB buffer per watch cost ~65 MiB for 1,000 directories. A subtree
// watch holds a single handle on the root and one buffer.
type rdcwBackend struct {
	w    *Watcher
	h    windows.Handle // root directory
	ev   windows.Handle // completion event of the pending read
	stop windows.Handle // set by close
	ov   windows.Overlapped
	buf  []byte
	done chan struct{}
	once sync.Once
}

const rdcwMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_ATTRIBUTES | windows.FILE_NOTIFY_CHANGE_SIZE |
	windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_CREATION

// rdcwBufSize is the notification buffer (64 KiB is the limit for network
// shares); an overflow triggers a full rescan.
const rdcwBufSize = 64 << 10

func newFSNotify(w *Watcher) (backend, error) {
	p, err := windows.UTF16PtrFromString(w.root)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", w.root, err)
	}
	b := &rdcwBackend{w: w, h: h, buf: make([]byte, rdcwBufSize), done: make(chan struct{})}
	if b.ev, err = windows.CreateEvent(nil, 1, 0, nil); err == nil {
		b.stop, err = windows.CreateEvent(nil, 1, 0, nil)
	}
	if err == nil {
		// The system records changes from the first read on, so it is
		// issued before the initial scan starts.
		err = b.read()
	}
	if err != nil {
		b.release()
		return nil, err
	}
	go b.loop()
	return b, nil
}

// read issues the next asynchronous ReadDirectoryChangesW.
func (b *rdcwBackend) read() error {
	if err := windows.ResetEvent(b.ev); err != nil {
		return err
	}
	b.ov = windows.Overlapped{HEvent: b.ev}
	if err := windows.ReadDirectoryChanges(b.h, &b.buf[0], uint32(len(b.buf)), true, rdcwMask, nil, &b.ov, 0); err != nil {
		return fmt.Errorf("ReadDirectoryChangesW %s: %w", b.w.root, err)
	}
	return nil
}

func (b *rdcwBackend) loop() {
	defer close(b.done)
	for {
		ev, err := windows.WaitForMultipleObjects([]windows.Handle{b.ev, b.stop}, false, windows.INFINITE)
		if err != nil || ev != windows.WAIT_OBJECT_0 {
			// Stopped (or the wait failed): cancel the read and wait for
			// it, so the kernel no longer writes into buf.
			_ = windows.CancelIoEx(b.h, &b.ov)
			var n uint32
			_ = windows.GetOverlappedResult(b.h, &b.ov, &n, true)
			return
		}
		var n uint32
		err = windows.GetOverlappedResult(b.h, &b.ov, &n, false)
		switch {
		case errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) || err == nil && n == 0:
			b.w.notifyFull() // buffer overflow: changes were dropped
		case err != nil:
			b.w.opts.OnError(fmt.Errorf("watch: ReadDirectoryChangesW %s: %w", b.w.root, err))
			b.w.notifyFull()
			return
		default:
			b.dispatch(n)
		}
		if err := b.read(); err != nil {
			b.w.opts.OnError(fmt.Errorf("watch: %w", err))
			b.w.notifyFull()
			return
		}
	}
}

// dispatch reports the paths of the FILE_NOTIFY_INFORMATION records in
// buf[:n] (names are relative to the root).
func (b *rdcwBackend) dispatch(n uint32) {
	var off uint32
	for off+uint32(unsafe.Sizeof(windows.FileNotifyInformation{})) <= n {
		fni := (*windows.FileNotifyInformation)(unsafe.Pointer(&b.buf[off]))
		name := windows.UTF16ToString(unsafe.Slice(&fni.FileName, fni.FileNameLength/2))
		b.w.notify(filepath.Join(b.w.root, name), false)
		if fni.NextEntryOffset == 0 {
			return
		}
		off += fni.NextEntryOffset
	}
}

// addDir is a no-op: the root watch covers the whole subtree.
func (b *rdcwBackend) addDir(string) error { return nil }

func (b *rdcwBackend) close() {
	b.once.Do(func() {
		_ = windows.SetEvent(b.stop)
		<-b.done
		b.release()
	})
}

func (b *rdcwBackend) release() {
	for _, h := range []windows.Handle{b.h, b.ev, b.stop} {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}
}
