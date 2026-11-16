package watch

// autoBackend: FSEvents (one stream for the tree) when built with cgo;
// without cgo polling – fsnotify's kqueue backend needs one file
// descriptor per watched file, which large projects exhaust.
func autoBackend() Backend {
	if fseventsAvailable {
		return FSEvents
	}
	return Poll
}
