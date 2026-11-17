// Package clipring keeps the clipboard history of the editors (Paste
// from History): the texts copied or cut last, newest first, without
// duplicates. It is UI independent and safe for concurrent use.
package clipring

import (
	"slices"
	"sync"
)

// DefaultSize is the number of entries a Ring keeps by default.
const DefaultSize = 30

// Ring is a bounded, de-duplicated clipboard history.
type Ring struct {
	mu    sync.Mutex
	max   int
	items []string
}

// New returns a ring keeping up to size entries (DefaultSize if size <= 0).
func New(size int) *Ring {
	if size <= 0 {
		size = DefaultSize
	}
	return &Ring{max: size}
}

// Push records text as the newest entry; an equal older entry is moved
// to the front. Empty text is ignored.
func (r *Ring) Push(text string) {
	if text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := slices.Index(r.items, text); i >= 0 {
		r.items = slices.Delete(r.items, i, i+1)
	}
	r.items = slices.Insert(r.items, 0, text)
	if len(r.items) > r.max {
		r.items = r.items[:r.max]
	}
}

// Items returns the entries, newest first (a copy).
func (r *Ring) Items() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.items)
}

// Len returns the number of entries.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// Take returns entry i and makes it the newest (as pasting it does).
func (r *Ring) Take(i int) (string, bool) {
	r.mu.Lock()
	if i < 0 || i >= len(r.items) {
		r.mu.Unlock()
		return "", false
	}
	text := r.items[i]
	r.mu.Unlock()
	r.Push(text)
	return text, true
}
