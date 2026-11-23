// Package freeze detects a blocked UI goroutine (D510, D511).
//
// The window's UI goroutine marks what it is doing with Monitor.Enter (a
// frame, an action, a window event); a Watchdog goroutine looks at the
// Monitor periodically. When the UI goroutine stays busy with the same
// work longer than the threshold, the Watchdog writes a dump of all
// goroutines to a local file (once per freeze) and logs one line on
// stderr. When the work finally returns, the Monitor reports the freeze
// with its total duration on the UI goroutine (OnRecover), so the window
// can show a status notice.
//
// Reports are plain local files below the config directory; nothing is
// ever sent anywhere. The package has no Gio dependency.
package freeze

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultThreshold is the time the UI goroutine may stay busy before a
// freeze is reported.
const DefaultThreshold = 2 * time.Second

// DefaultKeep is the number of freeze reports kept in the log directory.
const DefaultKeep = 20

// Report describes one freeze.
type Report struct {
	// Label is the innermost work running when the freeze was detected
	// ("action Open File (file.open)", "frame").
	Label string
	// Started is when the busy period began.
	Started time.Time
	// Duration is how long the UI goroutine was blocked: the time until
	// detection in the Watchdog's report, the total once recovered.
	Duration time.Duration
	// Path is the goroutine dump ("" if writing it failed).
	Path string
	// Err is the error writing the dump.
	Err error
}

// Message is the user-facing description of a recovered freeze.
func (r Report) Message() string {
	msg := fmt.Sprintf("UI was blocked for %.1f s by %s", r.Duration.Seconds(), r.Label)
	switch {
	case r.Path != "":
		msg += " — report: " + r.Path
	case r.Err != nil:
		msg += " — no report: " + r.Err.Error()
	}
	return msg
}

// Monitor records what the UI goroutine is busy with. Enter and the
// function it returns belong to the UI goroutine; the Watchdog reads the
// Monitor from its own goroutine. A nil *Monitor ignores everything.
type Monitor struct {
	// OnRecover is called on the UI goroutine when work that was reported
	// as a freeze returns (set before the first Enter).
	OnRecover func(Report)
	// Now is the clock (tests replace it; nil: time.Now).
	Now func() time.Time

	mu     sync.Mutex
	labels []string
	since  time.Time
	seq    uint64
	// reported is the freeze report of the busy period seq (nil: none).
	reported *Report
}

// NewMonitor returns an idle Monitor.
func NewMonitor() *Monitor { return &Monitor{} }

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Enter marks the UI goroutine busy with label until the returned
// function is called. Calls nest: the busy period lasts from the
// outermost Enter to its leave, the innermost label names the work.
func (m *Monitor) Enter(label string) (leave func()) {
	if m == nil {
		return func() {}
	}
	m.mu.Lock()
	if len(m.labels) == 0 {
		m.since = m.now()
		m.seq++
		m.reported = nil
	}
	m.labels = append(m.labels, label)
	depth := len(m.labels)
	m.mu.Unlock()
	return func() { m.leave(depth) }
}

// leave ends the Enter at depth (and any deeper one left open by a
// panic); the end of the busy period delivers a pending freeze report.
func (m *Monitor) leave(depth int) {
	m.mu.Lock()
	if depth <= len(m.labels) {
		m.labels = m.labels[:depth-1]
	}
	var done *Report
	if len(m.labels) == 0 && m.reported != nil {
		r := *m.reported
		r.Duration = m.now().Sub(m.since)
		done = &r
		m.reported = nil
	}
	cb := m.OnRecover
	m.mu.Unlock()
	if done != nil && cb != nil {
		cb(*done)
	}
}

// state is a snapshot of the Monitor for the Watchdog.
type state struct {
	busy     bool
	label    string
	since    time.Time
	seq      uint64
	reported bool
}

func (m *Monitor) snapshot() state {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := state{busy: len(m.labels) > 0, since: m.since, seq: m.seq, reported: m.reported != nil}
	if st.busy {
		st.label = m.labels[len(m.labels)-1]
	}
	return st
}

// markReported attaches r to busy period seq, unless that period ended
// meanwhile.
func (m *Monitor) markReported(seq uint64, r Report) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seq == seq && len(m.labels) > 0 && m.reported == nil {
		m.reported = &r
	}
}

// Watchdog checks a Monitor and writes freeze reports.
type Watchdog struct {
	// Monitor is the watched UI goroutine.
	Monitor *Monitor
	// Dir receives the reports (freeze-YYYYMMDD-HHMMSS.txt).
	Dir string
	// Threshold returns the current threshold (nil or <= 0:
	// DefaultThreshold); it is asked on every check, so a changed
	// setting applies at once.
	Threshold func() time.Duration
	// Interval is the check period (0: a quarter of the threshold, at
	// least 50 ms).
	Interval time.Duration
	// Keep is the number of reports kept (0: DefaultKeep).
	Keep int
	// Dump writes the goroutine dump (nil: all goroutines with full
	// stacks, pprof debug=2).
	Dump func(w io.Writer) error
	// Log receives one line per detected freeze (nil: os.Stderr).
	Log io.Writer
	// OnDump is called on the Watchdog goroutine after a report was
	// written (tests).
	OnDump func(Report)
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

func (w *Watchdog) threshold() time.Duration {
	if w.Threshold != nil {
		if t := w.Threshold(); t > 0 {
			return t
		}
	}
	return DefaultThreshold
}

func (w *Watchdog) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Run checks the Monitor until ctx is cancelled.
func (w *Watchdog) Run(ctx context.Context) {
	for {
		iv := w.Interval
		if iv <= 0 {
			iv = max(w.threshold()/4, 50*time.Millisecond)
		}
		t := time.NewTimer(iv)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			w.Check()
		}
	}
}

// Check looks at the Monitor once and writes a report when the UI
// goroutine has been busy longer than the threshold and this freeze was
// not reported yet. It reports whether it wrote one.
func (w *Watchdog) Check() bool {
	if w.Monitor == nil {
		return false
	}
	st := w.Monitor.snapshot()
	now := w.now()
	if !st.busy || st.reported || now.Sub(st.since) < w.threshold() {
		return false
	}
	r := Report{Label: st.label, Started: st.since, Duration: now.Sub(st.since)}
	r.Path, r.Err = w.write(r, now)
	w.Monitor.markReported(st.seq, r)
	out := w.Log
	if out == nil {
		out = os.Stderr
	}
	if r.Err != nil {
		fmt.Fprintf(out, "tiegel: UI blocked for %v by %s (writing the goroutine dump failed: %v)\n", r.Duration.Round(time.Millisecond), r.Label, r.Err)
	} else {
		fmt.Fprintf(out, "tiegel: UI blocked for %v by %s; goroutine dump: %s\n", r.Duration.Round(time.Millisecond), r.Label, r.Path)
	}
	if w.OnDump != nil {
		w.OnDump(r)
	}
	return true
}

// write stores the report file and prunes old ones.
func (w *Watchdog) write(r Report, now time.Time) (string, error) {
	if w.Dir == "" {
		return "", fmt.Errorf("freeze: no log directory")
	}
	if err := os.MkdirAll(w.Dir, 0o700); err != nil {
		return "", fmt.Errorf("freeze: log directory: %w", err)
	}
	base := "freeze-" + now.Format("20060102-150405")
	var f *os.File
	var path string
	for i := 1; ; i++ {
		name := base + ".txt"
		if i > 1 {
			name = fmt.Sprintf("%s-%d.txt", base, i)
		}
		path = filepath.Join(w.Dir, name)
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) || i >= 100 {
			return "", fmt.Errorf("freeze: create report: %w", err)
		}
	}
	fmt.Fprintf(f, "Tiegel UI freeze report (local file, never sent anywhere)\n\n")
	fmt.Fprintf(f, "time:      %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(f, "blocked:   %v (threshold %v)\n", r.Duration.Round(time.Millisecond), w.threshold())
	fmt.Fprintf(f, "busy with: %s\n", r.Label)
	fmt.Fprintf(f, "go:        %s %s/%s\n\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	dump := w.Dump
	if dump == nil {
		dump = dumpGoroutines
	}
	err := dump(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return path, fmt.Errorf("freeze: write report: %w", err)
	}
	w.prune()
	return path, nil
}

// prune removes the oldest reports beyond Keep.
func (w *Watchdog) prune() {
	keep := w.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasPrefix(n, "freeze-") && strings.HasSuffix(n, ".txt") {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Slice(names, func(i, j int) bool { return sortKey(names[i]) < sortKey(names[j]) })
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(w.Dir, n))
	}
}

// sortKey orders the report names by time and then by their counter
// ("freeze-X.txt" before "freeze-X-2.txt" before "freeze-X-10.txt").
func sortKey(name string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(name, "freeze-"), ".txt")
	n := 1
	if i := strings.LastIndex(s, "-"); i > len("20060102") {
		if v, err := strconv.Atoi(s[i+1:]); err == nil {
			n, s = v, s[:i]
		}
	}
	return fmt.Sprintf("%s#%05d", s, n)
}

// dumpGoroutines writes all goroutines with full stacks.
func dumpGoroutines(w io.Writer) error {
	p := pprof.Lookup("goroutine")
	if p == nil {
		return fmt.Errorf("goroutine profile unavailable")
	}
	return p.WriteTo(w, 2)
}
