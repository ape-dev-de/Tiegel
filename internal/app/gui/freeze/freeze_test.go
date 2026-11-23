package freeze

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a manual clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)} }

func TestNilMonitor(t *testing.T) {
	var m *Monitor
	m.Enter("x")() // must not panic
	w := &Watchdog{}
	if w.Check() {
		t.Fatal("check without monitor")
	}
}

func TestFreezeReportedOnceAndRecovered(t *testing.T) {
	ck := newClock()
	m := NewMonitor()
	m.Now = ck.Now
	var recovered []Report
	m.OnRecover = func(r Report) { recovered = append(recovered, r) }
	var log bytes.Buffer
	var dumped []Report
	dir := filepath.Join(t.TempDir(), "logs")
	w := &Watchdog{Monitor: m, Dir: dir, Now: ck.Now, Log: &log,
		Threshold: func() time.Duration { return 2 * time.Second },
		Dump:      func(w io.Writer) error { _, err := io.WriteString(w, "goroutine 1 [running]:\n"); return err },
		OnDump:    func(r Report) { dumped = append(dumped, r) },
	}

	// Idle: nothing to report.
	if w.Check() {
		t.Fatal("idle reported")
	}
	leaveFrame := m.Enter("frame")
	ck.Add(time.Second)
	if w.Check() {
		t.Fatal("reported below the threshold")
	}
	leaveAction := m.Enter("action Slow (slow.run)")
	ck.Add(1500 * time.Millisecond)
	if !w.Check() {
		t.Fatal("freeze not reported")
	}
	ck.Add(time.Second)
	if w.Check() {
		t.Fatal("freeze reported twice")
	}
	if len(dumped) != 1 || dumped[0].Label != "action Slow (slow.run)" || dumped[0].Duration != 2500*time.Millisecond {
		t.Fatalf("dump %+v", dumped)
	}
	data, err := os.ReadFile(dumped[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"never sent anywhere", "busy with: action Slow (slow.run)", "goroutine 1 [running]", "blocked:   2.5s"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("report lacks %q:\n%s", want, data)
		}
	}
	if !strings.HasPrefix(filepath.Base(dumped[0].Path), "freeze-20261123-1000") {
		t.Fatalf("name %s", dumped[0].Path)
	}
	if !strings.Contains(log.String(), "UI blocked for 2.5s by action Slow (slow.run); goroutine dump: "+dumped[0].Path) {
		t.Fatalf("log %q", log.String())
	}

	leaveAction()
	if len(recovered) != 0 {
		t.Fatal("recovered before the busy period ended")
	}
	ck.Add(300 * time.Millisecond)
	leaveFrame()
	if len(recovered) != 1 || recovered[0].Duration != 3800*time.Millisecond || recovered[0].Path != dumped[0].Path {
		t.Fatalf("recovered %+v", recovered)
	}
	if msg := recovered[0].Message(); msg != "UI was blocked for 3.8 s by action Slow (slow.run) — report: "+dumped[0].Path {
		t.Fatalf("message %q", msg)
	}

	// A new busy period is a new freeze.
	leave := m.Enter("frame")
	ck.Add(3 * time.Second)
	if !w.Check() {
		t.Fatal("second freeze not reported")
	}
	leave()
	if len(recovered) != 2 || len(dumped) != 2 || dumped[1].Path == dumped[0].Path {
		t.Fatalf("second freeze %+v %+v", recovered, dumped)
	}
	// Two reports in the same second get distinct names.
	p1, err1 := w.write(Report{Label: "x"}, ck.Now())
	p2, err2 := w.write(Report{Label: "y"}, ck.Now())
	if err1 != nil || err2 != nil || p1 == p2 || !strings.HasSuffix(p1, "-2.txt") || !strings.HasSuffix(p2, "-3.txt") {
		t.Fatalf("same second names %s %s (%v %v)", p1, p2, err1, err2)
	}

	// A short busy period stays silent.
	m.Enter("frame")()
	if len(recovered) != 2 {
		t.Fatal("short period reported")
	}
}

func TestMarkReportedIgnoresEndedPeriod(t *testing.T) {
	m := NewMonitor()
	m.Enter("frame")()
	st := m.snapshot()
	m.markReported(st.seq, Report{Label: "frame"})
	if m.snapshot().reported {
		t.Fatal("ended period marked")
	}
}

func TestLeaveAfterPanicUnwinds(t *testing.T) {
	m := NewMonitor()
	outer := m.Enter("frame")
	m.Enter("action") // never left (panic recovered further up)
	outer()           // the outer leave ends the period
	if m.snapshot().busy {
		t.Fatal("still busy")
	}
}

func TestDumpErrorAndNoDir(t *testing.T) {
	ck := newClock()
	m := NewMonitor()
	m.Now = ck.Now
	var recovered Report
	m.OnRecover = func(r Report) { recovered = r }
	var log bytes.Buffer
	w := &Watchdog{Monitor: m, Now: ck.Now, Log: &log}
	leave := m.Enter("frame")
	ck.Add(DefaultThreshold)
	if !w.Check() {
		t.Fatal("not reported")
	}
	leave()
	if recovered.Err == nil || recovered.Path != "" || !strings.Contains(recovered.Message(), "no report") {
		t.Fatalf("recovered %+v", recovered)
	}
	if !strings.Contains(log.String(), "dump failed") {
		t.Fatalf("log %q", log.String())
	}

	w = &Watchdog{Monitor: m, Now: ck.Now, Log: io.Discard, Dir: t.TempDir(),
		Dump: func(io.Writer) error { return errors.New("boom") }}
	leave = m.Enter("frame")
	ck.Add(DefaultThreshold)
	w.Check()
	leave()
	if recovered.Err == nil || !strings.Contains(recovered.Err.Error(), "boom") {
		t.Fatalf("dump error %+v", recovered)
	}
}

func TestDefaultDumpHasAllGoroutines(t *testing.T) {
	var b bytes.Buffer
	if err := dumpGoroutines(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "goroutine ") || !strings.Contains(b.String(), "TestDefaultDumpHasAllGoroutines") {
		t.Fatalf("dump %q", b.String()[:min(200, b.Len())])
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for i := range 5 {
		names = append(names, fmt.Sprintf("freeze-20261123-10000%d.txt", i))
	}
	names = append(names, "freeze-20261123-100004-2.txt", "freeze-20261123-100004-10.txt", "other.txt")
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w := &Watchdog{Dir: dir, Keep: 3}
	w.prune()
	entries, _ := os.ReadDir(dir)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	want := []string{"freeze-20261123-100004-10.txt", "freeze-20261123-100004-2.txt", "freeze-20261123-100004.txt", "other.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kept %v", got)
	}
}

func TestRunChecksUntilCancelled(t *testing.T) {
	m := NewMonitor()
	reported := make(chan Report, 1)
	w := &Watchdog{Monitor: m, Dir: t.TempDir(), Log: io.Discard,
		Threshold: func() time.Duration { return 20 * time.Millisecond }, Interval: 5 * time.Millisecond,
		Dump:   func(io.Writer) error { return nil },
		OnDump: func(r Report) { reported <- r },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	leave := m.Enter("frame")
	select {
	case r := <-reported:
		if r.Label != "frame" {
			t.Fatalf("label %q", r.Label)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not report")
	}
	leave()
	cancel()
	<-done
	// The default interval derives from the threshold.
	w2 := &Watchdog{}
	if w2.threshold() != DefaultThreshold {
		t.Fatal("default threshold")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	w2.Run(ctx2)
}
