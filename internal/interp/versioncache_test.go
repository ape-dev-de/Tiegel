package interp

import (
	"context"
	"sync"
	"testing"
)

func TestVersionCache(t *testing.T) {
	var nilCache *VersionCache
	nilCache.Set("8.3.0")
	if nilCache.Get() != "" {
		t.Fatal("nil cache stored a value")
	}
	c := NewVersionCache()
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.Set("8.3.1") }()
		go func() { defer wg.Done(); _ = c.Get() }()
	}
	wg.Wait()
	if c.Get() != "8.3.1" {
		t.Fatalf("got %q", c.Get())
	}
	seeded := &Local{Path: "/x/php", PHPVersion: "8.1.0"}
	if seeded.CachedVersion() != "8.1.0" || seeded.Label() != "PHP 8.1.0 (/x/php)" {
		t.Fatalf("seed %q", seeded.Label())
	}
}

// TestLocalVersionLabelRace: Version and Label of a shared Local run
// concurrently; run with -race.
func TestLocalVersionLabelRace(t *testing.T) {
	l := NewLocal(fakePHP(t, t.TempDir(), "php", "8.3.4"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for n := 0; n < 3; n++ {
			if _, err := l.Version(context.Background()); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for {
		_ = l.Label()
		select {
		case <-done:
			if l.CachedVersion() != "8.3.4" {
				t.Fatalf("version %q", l.CachedVersion())
			}
			return
		default:
		}
	}
}
