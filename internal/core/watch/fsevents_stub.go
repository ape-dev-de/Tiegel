//go:build !(darwin && cgo)

package watch

import "errors"

const fseventsAvailable = false

func newFSEvents(*Watcher) (backend, error) {
	return nil, errors.New("FSEvents needs macOS and cgo")
}
