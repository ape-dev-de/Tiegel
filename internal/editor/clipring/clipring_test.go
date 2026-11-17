package clipring

import (
	"fmt"
	"slices"
	"testing"
)

func TestRing(t *testing.T) {
	r := New(3)
	for _, s := range []string{"a", "b", "", "c", "a"} {
		r.Push(s)
	}
	if got := r.Items(); !slices.Equal(got, []string{"a", "c", "b"}) {
		t.Fatalf("items %v", got)
	}
	r.Push("d")
	if got := r.Items(); !slices.Equal(got, []string{"d", "a", "c"}) || r.Len() != 3 {
		t.Fatalf("bounded %v", got)
	}
	if s, ok := r.Take(2); !ok || s != "c" || r.Items()[0] != "c" {
		t.Fatalf("take %q %v %v", s, ok, r.Items())
	}
	if _, ok := r.Take(9); ok {
		t.Fatal("take out of range")
	}
	if New(0).max != DefaultSize {
		t.Fatal("default size")
	}
}

func TestRingConcurrent(t *testing.T) {
	r := New(10)
	done := make(chan bool)
	for g := 0; g < 4; g++ {
		go func(g int) {
			for i := 0; i < 100; i++ {
				r.Push(fmt.Sprint(g, i))
				_ = r.Items()
			}
			done <- true
		}(g)
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	if r.Len() != 10 {
		t.Fatalf("len %d", r.Len())
	}
}
