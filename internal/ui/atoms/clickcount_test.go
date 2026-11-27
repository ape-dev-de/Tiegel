package atoms

import (
	"testing"
	"time"
)

func TestClickCounter(t *testing.T) {
	var c ClickCounter
	t0 := 100 * time.Second
	c.Press(t0)
	c.Press(t0 + 350*time.Millisecond)
	if c.n != 2 {
		t.Fatal("a 350 ms pause is a double click")
	}
	c.Press(t0 + 700*time.Millisecond)
	if c.n != 3 {
		t.Fatal("triple")
	}
	c.Press(t0 + 2*time.Second)
	if c.n != 1 {
		t.Fatal("a long pause starts again")
	}
	c.Press(t0)
	if c.n != 1 {
		t.Fatal("time going backwards starts again")
	}
}
