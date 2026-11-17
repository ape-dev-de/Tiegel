package native

import "testing"

func TestGestureNames(t *testing.T) {
	want := map[int]string{1: GestureMouseBack, 2: GestureMouseForward, 3: GestureSwipeBack, 4: GestureSwipeForward, 0: "", 9: ""}
	for code, name := range want {
		if got := gestureName(code); got != name {
			t.Errorf("gestureName(%d) = %q, want %q", code, got, name)
		}
	}
	// Without a handler nothing is reported; installing one must not block.
	SetGestureHandler(nil)
	_ = GesturesSupported()
}
