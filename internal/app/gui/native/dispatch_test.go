package native

import (
	"sync"
	"testing"
	"time"
)

func TestDispatcherOrderAndPanics(t *testing.T) {
	var mu sync.Mutex
	var got []int
	var panics []any
	d := dispatcher{report: func(r any, _ []byte) {
		mu.Lock()
		panics = append(panics, r)
		mu.Unlock()
	}}
	done := make(chan struct{})
	for i := range 5 {
		if i == 2 {
			d.post(func() { panic("boom") })
		}
		d.post(func() {
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
		})
	}
	d.post(func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher stuck")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 5 || got[0] != 0 || got[4] != 4 || len(panics) != 1 {
		t.Fatalf("order %v, panics %v", got, panics)
	}
}

// A stuck receiver must never block the caller (the main thread): posts
// beyond the queue are dropped.
func TestDispatcherNeverBlocks(t *testing.T) {
	var d dispatcher
	release := make(chan struct{})
	defer close(release)
	d.post(func() { <-release })
	finished := make(chan int)
	go func() {
		dropped := 0
		for range dispatchQueue * 3 {
			if !d.post(func() {}) {
				dropped++
			}
		}
		finished <- dropped
	}()
	select {
	case dropped := <-finished:
		if dropped == 0 {
			t.Fatal("nothing dropped although the receiver is stuck")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post blocked")
	}
}
