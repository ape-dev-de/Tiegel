package interp

import "sync"

// VersionCache remembers the PHP version an interpreter reported. It is
// safe for concurrent use: interpreters are cached and shared between API
// calls, so Version (the writer) and Label (a reader) may run at the same
// time. Interpreters hold it by pointer, so copies (ExecOnly, WithoutPull)
// share it. All methods accept a nil receiver: nothing is cached then.
type VersionCache struct {
	mu sync.RWMutex
	v  string
}

// NewVersionCache returns an empty cache.
func NewVersionCache() *VersionCache { return &VersionCache{} }

// Get returns the cached version ("" if none or c is nil).
func (c *VersionCache) Get() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.v
}

// Set stores v; a nil cache ignores it.
func (c *VersionCache) Set(v string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.v = v
	c.mu.Unlock()
}
