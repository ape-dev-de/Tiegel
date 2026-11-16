//go:build darwin && cgo

package watch

/*
#cgo LDFLAGS: -framework CoreServices
#include <stdlib.h>
#include "fsevents_darwin.h"
*/
import "C"

import (
	"errors"
	"runtime/cgo"
	"unsafe"
)

const fseventsAvailable = true

// FSEvents flags (FSEvents.h).
const (
	fsMustScanSubDirs = 0x00000001
	fsUserDropped     = 0x00000002
	fsKernelDropped   = 0x00000004
	fsHistoryDone     = 0x00000010
	fsRootChanged     = 0x00000020
	fsMount           = 0x00000040
	fsUnmount         = 0x00000080
	fsItemCreated     = 0x00000100
	fsItemRemoved     = 0x00000200
	fsItemRenamed     = 0x00000800
	fsItemIsDir       = 0x00020000
)

// fseventsLatency is the coalescing delay of the stream in seconds; the
// watcher debounces on top of it.
const fseventsLatency = 0.05

// fseventsBackend is one FSEvents stream for the whole tree (no file
// descriptors per file or directory).
type fseventsBackend struct {
	w      *Watcher
	handle cgo.Handle
	stream *C.tiegel_stream
}

func newFSEvents(w *Watcher) (backend, error) {
	b := &fseventsBackend{w: w}
	b.handle = cgo.NewHandle(b)
	path := C.CString(w.root)
	defer C.free(unsafe.Pointer(path))
	b.stream = C.tiegel_fsevents_start(C.uintptr_t(b.handle), path, C.double(fseventsLatency))
	if b.stream == nil {
		b.handle.Delete()
		return nil, errors.New("FSEventStreamCreate/Start failed")
	}
	return b, nil
}

func (b *fseventsBackend) addDir(string) error { return nil }

func (b *fseventsBackend) close() {
	C.tiegel_fsevents_stop(b.stream)
	b.handle.Delete()
}

func (b *fseventsBackend) event(path string, flags uint32) {
	switch {
	case flags&fsHistoryDone != 0:
	case flags&(fsUserDropped|fsKernelDropped|fsRootChanged|fsMount|fsUnmount) != 0:
		b.w.notifyFull()
	case flags&fsMustScanSubDirs != 0:
		b.w.notify(path, true)
	case flags&fsItemIsDir != 0:
		// Directory events matter when entries appear, vanish or move;
		// attribute changes of a directory are not reported.
		if flags&(fsItemCreated|fsItemRemoved|fsItemRenamed) != 0 {
			b.w.notify(path, true)
		}
	default:
		b.w.notify(path, false)
	}
}

//export tiegelFSEvents
func tiegelFSEvents(handle C.uintptr_t, n C.size_t, paths **C.char, flags *C.uint32_t) {
	b, ok := cgo.Handle(handle).Value().(*fseventsBackend)
	if !ok || n == 0 {
		return
	}
	ps := unsafe.Slice(paths, int(n))
	fl := unsafe.Slice(flags, int(n))
	for i := range ps {
		b.event(C.GoString(ps[i]), uint32(fl[i]))
	}
}
