package gui

import (
	"sync"
	"time"
)

// appearanceTTL is how long a queried OS appearance is used before it is
// asked again.
const appearanceTTL = 5 * time.Second

// appearanceCache holds the OS appearance for the "system" theme. The
// query runs an external program (macOS `defaults`), so it never runs on
// the UI goroutine (D513): Dark returns the last known value and starts a
// background query when it is stale. Safe for concurrent use.
type appearanceCache struct {
	query func() bool

	mu      sync.Mutex
	dark    bool
	known   bool
	pending bool
	at      time.Time
}

// systemAppearance is the process-wide cache of all windows.
var systemAppearance = newAppearanceCache(systemDarkMode)

func newAppearanceCache(query func() bool) *appearanceCache {
	return &appearanceCache{query: query, dark: true}
}

// Dark reports the last known appearance (dark until the first answer)
// and refreshes it in the background when it is older than
// appearanceTTL; changed (may be nil) is called from that goroutine when
// the answer differs, so the window draws again.
func (a *appearanceCache) Dark(now time.Time, changed func()) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pending && (!a.known || now.Sub(a.at) > appearanceTTL) {
		a.pending, a.at = true, now
		go a.refresh(changed)
	}
	return a.dark
}

// refresh runs the query and stores the answer.
func (a *appearanceCache) refresh(changed func()) {
	d := a.query()
	a.mu.Lock()
	diff := d != a.dark
	a.dark, a.known, a.pending = d, true, false
	a.mu.Unlock()
	if diff && changed != nil {
		changed()
	}
}
