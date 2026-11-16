package watch

import (
	"errors"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// fsnotifyBackend watches every directory with fsnotify (inotify,
// ReadDirectoryChangesW, kqueue). New directories are added by the
// watcher when its rescan finds them.
type fsnotifyBackend struct {
	w    *Watcher
	fw   *fsnotify.Watcher
	wg   sync.WaitGroup
	once sync.Once
}

func newFSNotify(w *Watcher) (backend, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	b := &fsnotifyBackend{w: w, fw: fw}
	b.wg.Add(1)
	go b.loop()
	return b, nil
}

func (b *fsnotifyBackend) loop() {
	defer b.wg.Done()
	for {
		select {
		case ev, ok := <-b.fw.Events:
			if !ok {
				return
			}
			b.w.notify(ev.Name, false)
		case err, ok := <-b.fw.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				b.w.notifyFull()
				continue
			}
			b.w.opts.OnError(err)
		}
	}
}

func (b *fsnotifyBackend) addDir(abs string) error { return b.fw.Add(abs) }

func (b *fsnotifyBackend) close() {
	b.once.Do(func() { _ = b.fw.Close() })
	b.wg.Wait()
}
