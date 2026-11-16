package settings

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

func TestDefaultBaseDir(t *testing.T) {
	dir, err := DefaultBaseDir()
	if err != nil {
		t.Skipf("no user config dir: %v", err)
	}
	if filepath.Base(dir) != AppDirName {
		t.Fatalf("dir = %s", dir)
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", "")
	if _, err := DefaultBaseDir(); err == nil && runtime.GOOS != "windows" {
		t.Fatal("expected error without HOME")
	}
}

func TestProjectDirIsStableHash(t *testing.T) {
	base := t.TempDir()
	a, err := ProjectDir(base, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ProjectDir(base, "/tmp/proj/../proj/")
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
	c, _ := ProjectDir(base, "/tmp/other")
	if a == c {
		t.Fatal("different projects share a directory")
	}
	if filepath.Dir(a) != filepath.Join(base, "projects") || len(filepath.Base(a)) != 64 {
		t.Fatalf("unexpected layout: %s", a)
	}
}

func TestGlobalRoundTrip(t *testing.T) {
	base := t.TempDir()
	s, err := OpenGlobal(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Scope() != ScopeGlobal || s.Project() != "" || s.Dir() != base || s.Recovered() != "" {
		t.Fatal("metadata")
	}
	if got := Get(s, "missing", 42); got != 42 {
		t.Fatalf("default: %d", got)
	}
	type win struct{ W, H int }
	if err := Set(s, "ui.window", win{800, 600}); err != nil {
		t.Fatal(err)
	}
	if err := Set(s, "editor.fontSize", 13.5); err != nil {
		t.Fatal(err)
	}
	if err := Set(s, "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if !s.Has("theme") || s.Has("nope") {
		t.Fatal("Has")
	}
	if got := strings.Join(s.Keys(), ","); got != "editor.fontSize,theme,ui.window" {
		t.Fatalf("keys = %s", got)
	}

	s2, err := OpenGlobal(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := Get(s2, "ui.window", win{}); w != (win{800, 600}) {
		t.Fatalf("window = %+v", w)
	}
	if f := Get(s2, "editor.fontSize", 0.0); f != 13.5 {
		t.Fatalf("font = %v", f)
	}
	// wrong type -> default, Lookup reports error
	if n := Get(s2, "theme", 7); n != 7 {
		t.Fatalf("type mismatch should yield default, got %d", n)
	}
	if _, ok, err := Lookup[int](s2, "theme"); !ok || err == nil {
		t.Fatal("Lookup should report decode error")
	}
	if err := s2.Delete("theme"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete("theme"); err != nil {
		t.Fatal(err)
	}
	s3, _ := OpenGlobal(base, nil)
	if s3.Has("theme") {
		t.Fatal("delete not persisted")
	}
	if err := Set(s3, "bad", func() {}); err == nil {
		t.Fatal("expected encode error")
	}
}

func TestTypedKey(t *testing.T) {
	s, _ := OpenGlobal(t.TempDir(), nil)
	k := Key[time.Duration]{Name: "autosave.idle", Default: 15 * time.Second}
	if k.Get(s) != 15*time.Second {
		t.Fatal("default")
	}
	if err := k.Set(s, time.Minute); err != nil {
		t.Fatal(err)
	}
	if k.Get(s) != time.Minute {
		t.Fatal("set")
	}
}

func TestChangeEvents(t *testing.T) {
	b := bus.New()
	var mu sync.Mutex
	var changes []Change
	Changed.Subscribe(b, func(c Change) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	})
	base := t.TempDir()
	s, err := OpenProject(base, "/work/app", b)
	if err != nil {
		t.Fatal(err)
	}
	_ = Set(s, "a", 1)
	_ = Set(s, "a", 1) // unchanged: no event
	// reopen: unchanged value after reload must not produce an event either
	s2, _ := OpenProject(base, "/work/app", b)
	_ = Set(s2, "a", 1)
	_ = s2.Delete("a")
	_ = b.Drain(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 2 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].Key != "a" || changes[0].Scope != ScopeProject || changes[0].Deleted {
		t.Fatalf("first = %+v", changes[0])
	}
	if !changes[1].Deleted || !strings.HasSuffix(changes[1].Project, "app") {
		t.Fatalf("second = %+v", changes[1])
	}
}

// A store shared by several App instances publishes on every attached bus.
func TestAttachBus(t *testing.T) {
	b1, b2 := bus.New(), bus.New()
	var mu sync.Mutex
	got := map[*bus.Bus]int{}
	for _, b := range []*bus.Bus{b1, b2} {
		Changed.Subscribe(b, func(Change) {
			mu.Lock()
			got[b]++
			mu.Unlock()
		})
	}
	s, err := OpenGlobal(t.TempDir(), b1)
	if err != nil {
		t.Fatal(err)
	}
	detach := s.AttachBus(b2)
	_ = Set(s, "x", 1)
	detach()
	_ = Set(s, "x", 2)
	_ = b1.Drain(context.Background())
	_ = b2.Drain(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if got[b1] != 2 || got[b2] != 1 {
		t.Fatalf("events %v", got)
	}
}

func TestProjectStore(t *testing.T) {
	base := t.TempDir()
	proj := t.TempDir()
	s, err := OpenProject(base, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Scope() != ScopeProject || s.Project() != filepath.Clean(proj) {
		t.Fatalf("scope %s project %s", s.Scope(), s.Project())
	}
	want, _ := ProjectDir(base, proj)
	if s.Dir() != want {
		t.Fatalf("dir %s want %s", s.Dir(), want)
	}
	marker, err := os.ReadFile(filepath.Join(want, projectPathFile))
	if err != nil || string(marker) != s.Project() {
		t.Fatalf("marker %q %v", marker, err)
	}
	// nothing written into the project itself
	entries, _ := os.ReadDir(proj)
	if len(entries) != 0 {
		t.Fatalf("project dir polluted: %v", entries)
	}
	g, _ := OpenGlobal(base, nil)
	_ = Set(g, "k", "global")
	_ = Set(s, "k", "project")
	if Get(g, "k", "") != "global" || Get(s, "k", "") != "project" {
		t.Fatal("scopes not separated")
	}
}

func TestModuleState(t *testing.T) {
	s, _ := OpenGlobal(t.TempDir(), nil)
	if _, known := s.ModuleEnabled("git"); known {
		t.Fatal("unknown module reported as known")
	}
	if err := s.SetModuleEnabled("git", false); err != nil {
		t.Fatal(err)
	}
	if on, known := s.ModuleEnabled("git"); on || !known {
		t.Fatal("state")
	}
	_ = s.SetModuleEnabled("git", true)
	if on, _ := s.ModuleEnabled("git"); !on {
		t.Fatal("state not updated")
	}
	_ = Set(s, moduleKey("broken"), "yes")
	if _, known := s.ModuleEnabled("broken"); known {
		t.Fatal("undecodable state should be unknown")
	}
}

func TestCorruptFileIsRecovered(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenGlobal(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Recovered() == "" {
		t.Fatal("no recovery reported")
	}
	if _, err := os.Stat(s.Recovered()); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if len(s.Keys()) != 0 {
		t.Fatal("expected empty store")
	}
}

func TestOpenErrors(t *testing.T) {
	base := t.TempDir()
	// settings.json is a directory -> read error
	if err := os.MkdirAll(filepath.Join(base, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenGlobal(base, nil); err == nil {
		t.Fatal("expected read error")
	}
	// base is a file -> project marker cannot be written
	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := OpenProject(file, "/x", nil); err == nil {
		t.Fatal("expected marker error")
	}
}

func TestWriteFailureRollsBack(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	s, err := OpenGlobal(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Block directory creation by placing a file where base should be.
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(s, "a", 1); err == nil {
		t.Fatal("expected write error")
	}
	if s.Has("a") {
		t.Fatal("failed set not rolled back")
	}
	_ = os.Remove(base)
	_ = Set(s, "a", 1)
	_ = os.RemoveAll(base)
	_ = os.WriteFile(base, nil, 0o600)
	if err := s.Delete("a"); err == nil {
		t.Fatal("expected delete write error")
	}
	if !s.Has("a") {
		t.Fatal("failed delete not rolled back")
	}
	if err := Set(s, "a", 2); err == nil {
		t.Fatal("expected update write error")
	}
	if Get(s, "a", 0) != 1 {
		t.Fatal("failed update not rolled back")
	}
}

func TestConcurrentSet(t *testing.T) {
	s, _ := OpenGlobal(t.TempDir(), bus.New())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = Set(s, "k", i*100+j)
				_ = Get(s, "k", 0)
				_ = s.Keys()
			}
		}(i)
	}
	wg.Wait()
	s2, err := OpenGlobal(s.Dir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if Get(s2, "k", -1) != Get(s, "k", -2) {
		t.Fatal("disk and memory differ")
	}
}
