package supervisor

import (
	"sync"
	"time"
)

// Log entry kinds.
const (
	KindStderr = "stderr" // a line the server wrote to stderr
	KindSend   = "send"   // JSON-RPC message to the server (trace)
	KindRecv   = "recv"   // JSON-RPC message from the server (trace)
	KindEvent  = "event"  // supervisor event (start, exit, restart, …)
)

// Entry is one line of the server log.
type Entry struct {
	// Seq increases by one per entry (starting at 1).
	Seq  int64     `json:"seq"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	// Server is the ID of the server the entry belongs to.
	Server string `json:"server,omitempty"`
	Text   string `json:"text"`
}

// maxTraceText bounds a traced message in the log.
const maxTraceText = 8 << 10

// Log is a bounded in-memory server log (stderr, supervisor events and the
// optional JSON-RPC trace). It is safe for concurrent use.
type Log struct {
	mu       sync.Mutex
	entries  []Entry // ring buffer
	start    int     // index of the oldest entry
	n        int
	seq      int64
	onAppend func(Entry)
}

// NewLog returns a log keeping the last capacity entries (minimum 16).
// onAppend, if set, is called for every entry outside the lock; it must
// not block.
func NewLog(capacity int, onAppend func(Entry)) *Log {
	if capacity < 16 {
		capacity = 16
	}
	return &Log{entries: make([]Entry, capacity), onAppend: onAppend}
}

// Append adds an entry.
func (l *Log) Append(kind, server, text string) {
	if kind != KindStderr && len(text) > maxTraceText {
		text = text[:maxTraceText] + "…"
	}
	l.mu.Lock()
	l.seq++
	e := Entry{Seq: l.seq, Time: time.Now(), Kind: kind, Server: server, Text: text}
	idx := (l.start + l.n) % len(l.entries)
	if l.n == len(l.entries) {
		l.start = (l.start + 1) % len(l.entries)
	} else {
		l.n++
	}
	l.entries[idx] = e
	fn := l.onAppend
	l.mu.Unlock()
	if fn != nil {
		fn(e)
	}
}

// Since returns the entries with Seq > after (oldest first), at most limit
// (0: all kept).
func (l *Log) Since(after int64, limit int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Entry{}
	for i := 0; i < l.n; i++ {
		e := l.entries[(l.start+i)%len(l.entries)]
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Clear drops all entries (sequence numbers keep increasing).
func (l *Log) Clear() {
	l.mu.Lock()
	l.start, l.n = 0, 0
	l.mu.Unlock()
}
