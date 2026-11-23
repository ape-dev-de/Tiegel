package gui

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/freeze"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// A deliberately blocking action is reported by the watchdog: a
// goroutine dump that shows the action, and a notice once the UI runs
// again (D510).
func TestFreezeWatchdogReportsBlockingAction(t *testing.T) {
	e := newEnv(t, false)
	mon := freeze.NewMonitor()
	mon.OnRecover = e.v.freezeRecovered
	e.v.monitor = mon
	dir := filepath.Join(t.TempDir(), "logs")
	dumped := make(chan freeze.Report, 1)
	wd := &freeze.Watchdog{Monitor: mon, Dir: dir, Log: io.Discard, Interval: 5 * time.Millisecond,
		Threshold: func() time.Duration { return 50 * time.Millisecond },
		OnDump:    func(r freeze.Report) { dumped <- r },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wd.Run(ctx)

	var report freeze.Report
	e.v.Registry.AddAction(Action{ID: "test.blockUI", Title: "Block the UI…", Run: func(*View) {
		// Blocks until the watchdog has written its dump, so the stack of
		// this goroutine is in it.
		select {
		case report = <-dumped:
		case <-time.After(20 * time.Second):
			t.Error("watchdog did not report the blocked UI goroutine")
		}
	}})
	e.v.RunAction("test.blockUI")
	if report.Path == "" || report.Label != "action Block the UI (test.blockUI)" {
		t.Fatalf("report %+v", report)
	}
	data, err := os.ReadFile(report.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "TestFreezeWatchdogReportsBlockingAction") || !strings.Contains(string(data), "safeRun") {
		t.Fatalf("dump lacks the blocked goroutine:\n%.2000s", data)
	}
	n := e.ctl.Status.Notice
	if !strings.HasPrefix(n, "UI was blocked for ") || !strings.Contains(n, "by action Block the UI (test.blockUI) — report: "+report.Path) {
		t.Fatalf("notice %q", n)
	}

	// A quick action is not a freeze.
	e.ctl.Status.Notice = ""
	var ran atomic.Bool
	e.v.Registry.AddAction(Action{ID: "test.quick", Title: "Quick", Run: func(*View) { ran.Store(true) }})
	e.v.RunAction("test.quick")
	time.Sleep(20 * time.Millisecond) // a few watchdog checks after the action
	if !ran.Load() || e.ctl.Status.Notice != "" {
		t.Fatalf("quick action reported: %q", e.ctl.Status.Notice)
	}
}

// The window's watchdog writes to <config dir>/logs and follows the
// setting ui.freezeThresholdMs.
func TestWindowWatchdogSettings(t *testing.T) {
	e := newEnv(t, false)
	wd := newWatchdog(freeze.NewMonitor(), e.app)
	if wd.Dir != filepath.Join(e.app.ConfigDir(), "logs") {
		t.Fatalf("dir %s", wd.Dir)
	}
	if got := wd.Threshold(); got != freeze.DefaultThreshold {
		t.Fatalf("default threshold %v", got)
	}
	var sv api.SettingValue
	if err := e.ctl.Backend().Call(context.Background(), "settings.set", api.SettingsSetParams{Key: settings.KeyFreezeThreshold, Scope: "global", Value: json.RawMessage("750")}, &sv); err != nil {
		t.Fatal(err)
	}
	if got := wd.Threshold(); got != 750*time.Millisecond {
		t.Fatalf("threshold %v", got)
	}
	if err := e.ctl.Backend().Call(context.Background(), "settings.set", api.SettingsSetParams{Key: settings.KeyFreezeThreshold, Scope: "global", Value: json.RawMessage("5")}, &sv); err == nil {
		t.Fatal("threshold below the minimum accepted")
	}
}

// The OS appearance is queried off the UI goroutine and cached.
func TestAppearanceCacheQueriesInBackground(t *testing.T) {
	release := make(chan struct{})
	var queries atomic.Int32
	a := newAppearanceCache(func() bool {
		queries.Add(1)
		<-release
		return false
	})
	changed := make(chan struct{}, 1)
	now := time.Now()
	// The first call returns at once (dark until known) although the
	// query blocks.
	if !a.Dark(now, func() { changed <- struct{}{} }) {
		t.Fatal("unknown appearance should default to dark")
	}
	if !a.Dark(now, nil) || queries.Load() > 1 {
		t.Fatal("second query while one is pending")
	}
	close(release)
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change callback")
	}
	if a.Dark(now.Add(time.Second), nil) {
		t.Fatal("answer not cached")
	}
	if queries.Load() != 1 {
		t.Fatalf("queries %d within the TTL", queries.Load())
	}
	a.Dark(now.Add(appearanceTTL+time.Second), nil)
	deadline := time.Now().Add(5 * time.Second)
	for queries.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if queries.Load() != 2 {
		t.Fatal("stale appearance not refreshed")
	}
}
