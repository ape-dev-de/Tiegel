package gui

import (
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
)

// Every backend call of the package's tests must come from background
// work (D512): a blocking call made by an action, a frame or a posted
// function on the UI goroutine fails the run (TestMain).

// syncViolations collects the unmarked backend calls (method -> stack of
// the first call).
var syncViolations = struct {
	sync.Mutex
	byMethod map[string]string
}{byMethod: map[string]string{}}

func init() {
	controller.SetDefaultSyncCallHandler(func(method string) {
		syncViolations.Lock()
		defer syncViolations.Unlock()
		if _, ok := syncViolations.byMethod[method]; !ok {
			syncViolations.byMethod[method] = string(debug.Stack())
		}
	})
}

// checkSyncCalls reports the collected violations and turns a passing
// run (code 0) into a failure when there are any.
func checkSyncCalls(code int) int {
	syncViolations.Lock()
	defer syncViolations.Unlock()
	methods := make([]string, 0, len(syncViolations.byMethod))
	for k := range syncViolations.byMethod {
		methods = append(methods, k)
	}
	sort.Strings(methods)
	for _, k := range methods {
		fmt.Fprintf(os.Stderr, "blocking backend call %s on the UI goroutine (use Controller.Go):\n%s\n", k, syncViolations.byMethod[k])
	}
	if len(methods) > 0 && code == 0 {
		return 1
	}
	return code
}

// actionsNotRunBySyncTest are actions the all-actions test leaves out:
// they leave the process (new window, Finder), start external programs
// or listeners, or end the project the remaining actions need.
var actionsNotRunBySyncTest = map[string]bool{
	"window.new": true, "file.reveal": true, "project.close": true, "project.hibernate": true,
	"debug.toggleListen": true, "run.debug": true, "run.debugFile": true, "run.run": true, "run.runFile": true,
	"run.rerun": true, "test.rerunFailed": true, "terminal.new": true,
}

// Every registered action, run from the menu path with an open PHP file,
// makes its backend calls in background work only (D512).
func TestActionsMakeNoBlockingBackendCalls(t *testing.T) {
	e := newEnv(t, true)
	e.v.noNativeDialog = true
	var mu sync.Mutex
	current := ""
	found := map[string][]string{}
	e.ctl.SetSyncCallHandler(func(method string) {
		mu.Lock()
		found[current] = append(found[current], method)
		mu.Unlock()
	})
	ran := 0
	for _, a := range e.v.Registry.Actions() {
		id := string(a.ID)
		if actionsNotRunBySyncTest[id] || a.Run == nil {
			continue
		}
		if e.ctl.ActiveTab() == nil {
			e.ctl.OpenFile("src/Shop.php", nil)
			e.settle()
		}
		mu.Lock()
		current = id
		mu.Unlock()
		e.v.runAction(a.ID)
		e.settle()
		e.v.closeOverlay()
		ran++
	}
	e.settle()
	mu.Lock()
	defer mu.Unlock()
	if ran < 50 {
		t.Fatalf("only %d actions ran", ran)
	}
	for id, methods := range found {
		t.Errorf("action %s made blocking backend calls on the UI goroutine: %v", id, methods)
	}
}
