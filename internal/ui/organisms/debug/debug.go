// Package debug contains the debugger organisms: toolbar, call stack,
// variables tree, watches, breakpoints and session list, and the DebugPanel
// template that combines them.
//
// The organisms know nothing about DBGp connections. Data flows in through
// exported fields (plain view-model types such as Frame or Variable; see
// dbgp.go for converters from internal/dbgp), and user intentions flow out
// as Events returned by Update:
//
//	for _, ev := range panel.Update(gtx) {
//		switch ev := ev.(type) {
//		case debug.ActionEvent:  // resume, step over, …
//		case debug.ExpandEvent:  // fetch children of ev.FullName (page ev.Page)
//		…
//		}
//	}
//	panel.Layout(gtx, th)
//
// Layout calls Update itself, so events are dropped when the host does not
// call Update first. Purely visual state (selection, expanded nodes, split
// ratios, inline editors) lives in the organisms.
package debug

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// Event is a user intention reported by Update.
type Event interface{ isDebugEvent() }

// Action names a debugger command; the names double as keymap actions.
type Action = keymap.Action

// Debugger actions.
const (
	ActionToggleListen    Action = "debug.toggleListen"
	ActionResume          Action = "debug.resume"
	ActionStepOver        Action = "debug.stepOver"
	ActionStepInto        Action = "debug.stepInto"
	ActionStepOut         Action = "debug.stepOut"
	ActionStop            Action = "debug.stop"
	ActionMuteBreakpoints Action = "debug.muteBreakpoints"
)

// Shortcuts returns the default key bindings of the debugger actions for
// goos (runtime.GOOS). They are data: hosts merge them into their keymap
// and the toolbar shows them in tooltips.
func Shortcuts(goos string) []keymap.Binding {
	stop := "Ctrl+F2"
	if goos == "darwin" {
		stop = "Cmd+F2"
	}
	return []keymap.Binding{
		{Chord: keymap.MustParse("F9"), Action: ActionResume},
		{Chord: keymap.MustParse("F8"), Action: ActionStepOver},
		{Chord: keymap.MustParse("F7"), Action: ActionStepInto},
		{Chord: keymap.MustParse("Shift+F8"), Action: ActionStepOut},
		{Chord: keymap.MustParse(stop), Action: ActionStop},
	}
}

// State is the state of a debug session as shown by the UI.
type State int

// Session states.
const (
	StateNone     State = iota // no session
	StateStarting              // connected, not yet running
	StateRunning
	StateBreak // paused at a breakpoint or step
	StateStopping
	StateStopped
)

// String returns the state name.
func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateBreak:
		return "paused"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	}
	return "idle"
}

// Active reports whether a session exists that can be stopped.
func (s State) Active() bool { return s >= StateStarting && s <= StateStopping }

// ActionEvent asks the host to run a debugger action.
type ActionEvent struct{ Action Action }

// FrameSelectEvent reports that a stack frame was selected; the host
// loads its variables and shows File:Line.
type FrameSelectEvent struct {
	Level int
	File  string
	Line  int
}

// ExpandEvent asks the host to fetch the children of a variable: page 0
// when it is expanded for the first time, further pages for "load more".
type ExpandEvent struct {
	FullName string
	Page     int
}

// CopyValueEvent asks the host to copy a value to the clipboard.
type CopyValueEvent struct {
	FullName string
	Value    string
}

// WatchAddEvent asks to add a watch expression.
type WatchAddEvent struct{ Expression string }

// WatchEditEvent asks to replace the watch at Index.
type WatchEditEvent struct {
	Index      int
	Expression string
}

// WatchRemoveEvent asks to remove the watch at Index.
type WatchRemoveEvent struct{ Index int }

// BreakpointToggleEvent asks to enable or disable a breakpoint.
type BreakpointToggleEvent struct {
	ID      string
	Enabled bool
}

// BreakpointConditionEvent asks to change a breakpoint's condition (empty:
// unconditional).
type BreakpointConditionEvent struct {
	ID        string
	Condition string
}

// BreakpointRemoveEvent asks to remove a breakpoint.
type BreakpointRemoveEvent struct{ ID string }

// BreakpointJumpEvent asks to show the breakpoint's location.
type BreakpointJumpEvent struct {
	ID   string
	File string
	Line int
}

// SessionSelectEvent switches the panel to another session.
type SessionSelectEvent struct{ ID string }

// SessionStopEvent asks to stop (detach) a session.
type SessionStopEvent struct{ ID string }

func (ActionEvent) isDebugEvent()              {}
func (FrameSelectEvent) isDebugEvent()         {}
func (ExpandEvent) isDebugEvent()              {}
func (CopyValueEvent) isDebugEvent()           {}
func (WatchAddEvent) isDebugEvent()            {}
func (WatchEditEvent) isDebugEvent()           {}
func (WatchRemoveEvent) isDebugEvent()         {}
func (BreakpointToggleEvent) isDebugEvent()    {}
func (BreakpointConditionEvent) isDebugEvent() {}
func (BreakpointRemoveEvent) isDebugEvent()    {}
func (BreakpointJumpEvent) isDebugEvent()      {}
func (SessionSelectEvent) isDebugEvent()       {}
func (SessionStopEvent) isDebugEvent()         {}

// location formats "File.php:42" from a path (base name only).
func location(path string, line int) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(strings.ReplaceAll(path, "\\", "/"))
	if line <= 0 {
		return base
	}
	return fmt.Sprintf("%s:%d", base, line)
}
