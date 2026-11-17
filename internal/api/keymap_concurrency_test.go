package api

import (
	"fmt"
	"sync"
	"testing"
)

// TestKeymapConcurrentSets runs keymap.set for different actions from
// many goroutines (as the GUI does): no override may be lost to an
// unserialised read-modify-write of the settings.
func TestKeymapConcurrentSets(t *testing.T) {
	a := newTestApp(t)
	var st KeymapState
	call(t, a, "keymap.list", nil, &st)
	const n = 26
	if len(st.Bindings) < n {
		t.Fatalf("only %d actions", len(st.Bindings))
	}
	var wg sync.WaitGroup
	errs := make(chan error, n+1)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chord := fmt.Sprintf("Ctrl+Alt+Shift+%c", 'A'+i)
			p := KeymapSetParams{Action: st.Bindings[i].Action, Chords: []string{chord}}
			if err := tryCall(a, "keymap.set", p, nil); err != nil {
				errs <- err
			}
		}(i)
	}
	// A concurrent reset of an action that is not set must not undo the
	// others either.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := tryCall(a, "keymap.reset", KeymapResetParams{Action: st.Bindings[n].Action}, nil); err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	call(t, a, "keymap.list", KeymapListParams{User: true}, &st)
	if len(st.Bindings) != n {
		t.Fatalf("%d user bindings after %d concurrent sets", len(st.Bindings), n)
	}
}
