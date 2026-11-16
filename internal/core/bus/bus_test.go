package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func drain(t *testing.T, b *Bus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

func TestPublishAsyncOrderPerSubscriber(t *testing.T) {
	b := New()
	var mu sync.Mutex
	var got []int
	b.Subscribe("n", func(e Event) {
		mu.Lock()
		got = append(got, e.Payload.(int))
		mu.Unlock()
	})
	for i := 0; i < 1000; i++ {
		b.Publish("n", i)
	}
	drain(t, b)
	if len(got) != 1000 {
		t.Fatalf("got %d events, want 1000", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("event %d = %d: order not preserved", i, v)
		}
	}
}

func TestPublishSyncRunsInOrderAndReturnsPanics(t *testing.T) {
	var panics atomic.Int32
	b := New(WithPanicHandler(func(*PanicError) { panics.Add(1) }))
	var order []string
	b.Subscribe("x", func(Event) { order = append(order, "a") })
	b.Subscribe("x", func(Event) { panic("boom") })
	b.Subscribe("x", func(Event) { order = append(order, "c") })
	err := b.PublishSync("x", nil)
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Topic != "x" || pe.Value != "boom" {
		t.Fatalf("want PanicError, got %v", err)
	}
	if pe.Error() == "" || len(pe.Stack) == 0 {
		t.Fatal("panic error lacks message or stack")
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "c" {
		t.Fatalf("order = %v", order)
	}
	if panics.Load() != 1 {
		t.Fatalf("panic handler called %d times", panics.Load())
	}
}

func TestAsyncPanicDoesNotStopDelivery(t *testing.T) {
	var panics atomic.Int32
	b := New(WithPanicHandler(func(*PanicError) { panics.Add(1) }))
	var n atomic.Int32
	b.Subscribe("x", func(e Event) {
		if e.Payload.(int) == 1 {
			panic("first")
		}
		n.Add(1)
	})
	b.Publish("x", 1)
	b.Publish("x", 2)
	drain(t, b)
	if n.Load() != 1 || panics.Load() != 1 {
		t.Fatalf("n=%d panics=%d", n.Load(), panics.Load())
	}
}

func TestPatterns(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"file.saved", "file.saved", true},
		{"file.saved", "file.opened", false},
		{"*", "anything", true},
		{"file.*", "file.saved", true},
		{"file.*", "file.a.b", true},
		{"file.*", "filex.saved", false},
		{"file.*", "file", false},
	}
	for _, c := range cases {
		if got := match(c.pattern, c.topic); got != c.want {
			t.Errorf("match(%q,%q)=%v want %v", c.pattern, c.topic, got, c.want)
		}
	}
	b := New()
	var all, files atomic.Int32
	b.Subscribe("*", func(Event) { all.Add(1) })
	b.Subscribe("file.*", func(Event) { files.Add(1) })
	_ = b.PublishSync("file.saved", nil)
	_ = b.PublishSync("project.opened", nil)
	if all.Load() != 2 || files.Load() != 1 {
		t.Fatalf("all=%d files=%d", all.Load(), files.Load())
	}
}

func TestUnsubscribe(t *testing.T) {
	b := New()
	var n atomic.Int32
	unsub := b.Subscribe("x", func(Event) { n.Add(1) })
	_ = b.PublishSync("x", nil)
	unsub()
	unsub() // idempotent
	_ = b.PublishSync("x", nil)
	b.Publish("x", nil)
	drain(t, b)
	if n.Load() != 1 {
		t.Fatalf("n = %d, want 1", n.Load())
	}
}

func TestUnsubscribeDropsQueued(t *testing.T) {
	b := New()
	release := make(chan struct{})
	started := make(chan struct{})
	var n atomic.Int32
	var once sync.Once
	unsub := b.Subscribe("x", func(Event) {
		once.Do(func() { close(started) })
		<-release
		n.Add(1)
	})
	for i := 0; i < 10; i++ {
		b.Publish("x", i)
	}
	<-started
	unsub()
	close(release)
	drain(t, b)
	if n.Load() != 1 {
		t.Fatalf("handled %d events after unsubscribe, want 1 (the running one)", n.Load())
	}
}

func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	b := New()
	block := make(chan struct{})
	b.Subscribe("x", func(Event) { <-block })
	fast := make(chan struct{}, 1)
	b.Subscribe("x", func(Event) { fast <- struct{}{} })
	b.Publish("x", nil)
	select {
	case <-fast:
	case <-time.After(5 * time.Second):
		t.Fatal("fast subscriber blocked by slow one")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain with blocked handler: %v", err)
	}
	close(block)
	drain(t, b)
}

func TestClose(t *testing.T) {
	b := New()
	var n atomic.Int32
	b.Subscribe("x", func(Event) { n.Add(1) })
	b.Publish("x", nil)
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("queued event not delivered before close: %d", n.Load())
	}
	b.Publish("x", nil)
	_ = b.PublishSync("x", nil)
	unsub := b.Subscribe("x", func(Event) { n.Add(1) })
	unsub()
	if n.Load() != 1 {
		t.Fatalf("delivery after close: %d", n.Load())
	}
}

func TestNilHandler(t *testing.T) {
	b := New()
	b.Subscribe("x", nil)()
	if err := b.PublishSync("x", nil); err != nil {
		t.Fatal(err)
	}
}

func TestTypedTopic(t *testing.T) {
	type saved struct{ Path string }
	topic := Topic[saved]("file.saved")
	if topic.Name() != "file.saved" {
		t.Fatal(topic.Name())
	}
	b := New()
	var got []string
	var mu sync.Mutex
	topic.Subscribe(b, func(s saved) {
		mu.Lock()
		got = append(got, s.Path)
		mu.Unlock()
	})
	topic.Publish(b, saved{"a"})
	drain(t, b)
	if err := topic.PublishSync(b, saved{"b"}); err != nil {
		t.Fatal(err)
	}
	b.Publish("file.saved", "wrong type") // ignored by typed handler
	drain(t, b)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	b := New()
	var n atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				unsub := b.Subscribe("c", func(Event) { n.Add(1) })
				b.Publish("c", j)
				_ = b.PublishSync("c", j)
				if j%4 != 0 {
					unsub()
				}
			}
		}()
	}
	wg.Wait()
	drain(t, b)
	if n.Load() == 0 {
		t.Fatal("no events delivered")
	}
}

// Review 2026-11-16 #14: a subscriber that stops consuming made its queue
// grow without bound. A bounded subscription is cut off at the limit.
func TestSubscribeBoundedOverflow(t *testing.T) {
	b := New()
	block := make(chan struct{})
	overflow := make(chan struct{}, 2)
	var got atomic.Int32
	b.SubscribeBounded("t", func(Event) { got.Add(1); <-block }, 3, func() { overflow <- struct{}{} })
	for i := 0; i < 100; i++ {
		b.Publish("t", i)
	}
	select {
	case <-overflow:
	case <-time.After(5 * time.Second):
		t.Fatal("overflow not reported")
	}
	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Drain(ctx); err != nil {
		t.Fatal(err) // queued events were released
	}
	if n := got.Load(); n > 4 {
		t.Fatalf("delivered %d events after the limit", n)
	}
	b.Publish("t", "after")
	if err := b.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if len(overflow) != 0 {
		t.Fatal("overflow reported twice")
	}
	// Unbounded and roomy subscriptions are unaffected.
	var all atomic.Int32
	b.SubscribeBounded("u", func(Event) { all.Add(1) }, 0, nil)
	for i := 0; i < 50; i++ {
		b.Publish("u", i)
	}
	if err := b.Drain(ctx); err != nil || all.Load() != 50 {
		t.Fatalf("unbounded: %d %v", all.Load(), err)
	}
}
