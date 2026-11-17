// Package runtime contains the PHP runtime organisms: InterpreterPicker
// (local binaries, Docker Compose candidates and Docker images with their
// context and service state), InterpreterInfoPanel (probe result with
// "re-read"), PathMappingEditor, XdebugHintsList and the StatusbarVersion
// widget.
//
// Like all organisms they only show data and report intentions: hosts set
// the exported fields (see convert.go for converters from internal/interp
// and internal/compose) and handle the Events returned by Update. Layout
// calls Update itself, so events are dropped when the host skips Update.
package runtime

// Event is a user intention reported by Update.
type Event interface{ isRuntimeEvent() }

// SelectInterpreterEvent asks to use the interpreter with ID.
type SelectInterpreterEvent struct{ ID string }

// MappingAddEvent asks to append an (empty) path mapping.
type MappingAddEvent struct{}

// MappingChangeEvent reports an edited mapping row.
type MappingChangeEvent struct {
	Index        int
	Host, Remote string
}

// MappingRemoveEvent asks to remove the mapping at Index.
type MappingRemoveEvent struct{ Index int }

// HintSelectEvent reports a click on a hint (e.g. to show its docs).
type HintSelectEvent struct{ Code string }

// ProbeRefreshEvent asks to re-read the interpreter info (interp.probe
// with refresh) of the interpreter with ID.
type ProbeRefreshEvent struct{ ID string }

// OverrideEvent asks to open the PHP version/interpreter override (click
// on the status bar widget).
type OverrideEvent struct{}

func (SelectInterpreterEvent) isRuntimeEvent() {}
func (MappingAddEvent) isRuntimeEvent()        {}
func (MappingChangeEvent) isRuntimeEvent()     {}
func (MappingRemoveEvent) isRuntimeEvent()     {}
func (HintSelectEvent) isRuntimeEvent()        {}
func (OverrideEvent) isRuntimeEvent()          {}
func (ProbeRefreshEvent) isRuntimeEvent()      {}

// unbounded is the threshold above which a constraint counts as unbounded.
const unbounded = 1 << 20
