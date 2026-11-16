package dbgp

import (
	"errors"
	"fmt"
)

// ErrSessionClosed is returned by session commands after the connection to
// the debugger engine has been closed.
var ErrSessionClosed = errors.New("dbgp: session closed")

// Error is an error response of the debugger engine.
type Error struct {
	Command string // command that failed
	Code    int    // DBGp error code, e.g. 5 (command not available) or 206 (eval error)
	Message string // engine-provided message, may be empty
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("dbgp: %s failed with error %d", e.Command, e.Code)
	}
	return fmt.Sprintf("dbgp: %s failed with error %d: %s", e.Command, e.Code, e.Message)
}

// Status is the execution state of the debugged script.
type Status string

// Engine states as defined by DBGp. StatusRunning is set locally while a
// continuation command is in flight.
const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusBreak    Status = "break"
	StatusStopping Status = "stopping"
	StatusStopped  Status = "stopped"
)

// InitInfo is the content of the init packet sent by the engine on connect.
type InitInfo struct {
	FileURI         string // script URI as sent by the engine
	File            string // FileURI mapped to a host path
	IDEKey          string
	AppID           string
	Session         string // value of XDEBUG_SESSION / DBGP_COOKIE, if any
	Thread          string
	Parent          string // parent appid, if any
	Language        string // e.g. "PHP"
	LanguageVersion string
	ProtocolVersion string
	Engine          string // e.g. "Xdebug"
	EngineVersion   string
}

// BreakInfo describes the result of a continuation command.
type BreakInfo struct {
	Status        Status
	Reason        string // ok, error, aborted, exception
	FileURI       string // remote file URI, empty if unknown
	File          string // host path, empty if unknown
	Line          int
	Exception     string // exception class if the break was caused by one
	ExceptionCode string
	Message       string // exception message
}

// StatusEvent is reported via [Events.StatusChanged].
type StatusEvent struct {
	Previous Status
	Command  string // command whose response caused the change ("" if local)
	Info     BreakInfo
}

// Property is a variable or expression value, possibly with children.
type Property struct {
	Name        string
	FullName    string // expression to address this property in property_get
	Type        string // e.g. int, string, array, object, null, uninitialized
	ClassName   string
	Facet       string // e.g. "public static"
	Key         string
	Value       string // decoded value, possibly truncated (see Size)
	Size        int    // full size of the value in bytes, if reported
	HasChildren bool
	NumChildren int
	Page        int
	PageSize    int
	Children    []*Property
}

// HasMorePages reports whether further child pages can be fetched with
// [Session.Property].
func (p *Property) HasMorePages() bool {
	if p == nil || !p.HasChildren || p.PageSize <= 0 {
		return false
	}
	return (p.Page+1)*p.PageSize < p.NumChildren
}

func convertProperty(xp *xmlProperty) *Property {
	p := &Property{
		Name:        xp.Name,
		FullName:    xp.FullName,
		Type:        xp.Type,
		ClassName:   xp.ClassName,
		Facet:       xp.Facet,
		Key:         xp.Key,
		Size:        atoi(xp.Size),
		HasChildren: xp.Children == "1" || xp.Children == "true",
		NumChildren: atoi(xp.NumChildren),
		Page:        atoi(xp.Page),
		PageSize:    atoi(xp.PageSize),
	}
	if xp.NameEl != nil {
		p.Name = xp.NameEl.decode()
	}
	if xp.FullNameEl != nil {
		p.FullName = xp.FullNameEl.decode()
	}
	if xp.ClassNameEl != nil {
		p.ClassName = xp.ClassNameEl.decode()
	}
	switch {
	case xp.ValueEl != nil:
		p.Value = xp.ValueEl.decode()
	case len(xp.Properties) == 0 && !p.HasChildren:
		p.Value = decodeText(xp.Text, xp.Encoding)
	}
	if len(xp.Properties) > 0 {
		p.Children = make([]*Property, 0, len(xp.Properties))
		for i := range xp.Properties {
			p.Children = append(p.Children, convertProperty(&xp.Properties[i]))
		}
	}
	return p
}

// StackFrame is one entry of the call stack.
type StackFrame struct {
	Level   int    // 0 is the current frame
	Where   string // function name, e.g. "{main}" or "Foo->bar"
	Type    string // "file" or "eval"
	FileURI string // remote file URI
	File    string // host path
	Line    int
}

// Context is a variable context such as Locals or Superglobals.
type Context struct {
	ID   int
	Name string
}

// Common context IDs used by Xdebug.
const (
	ContextLocals       = 0
	ContextSuperglobals = 1
	ContextConstants    = 2
)

// BreakpointType is the DBGp breakpoint type.
type BreakpointType string

// Supported breakpoint types.
const (
	BreakpointLine        BreakpointType = "line"
	BreakpointConditional BreakpointType = "conditional"
	BreakpointCall        BreakpointType = "call"
	BreakpointReturn      BreakpointType = "return"
	BreakpointException   BreakpointType = "exception"
)

// Breakpoint describes a breakpoint. When setting a breakpoint, ID, HitCount
// and Resolved are ignored; File is a host path.
type Breakpoint struct {
	ID   string
	Type BreakpointType // derived from the other fields if empty
	// File is the host path; FileURI is the engine's URI (filled on output).
	File    string
	FileURI string
	Line    int
	// Condition is a PHP expression; for line breakpoints it turns the
	// breakpoint into a conditional one.
	Condition string
	Function  string // call/return breakpoints
	Class     string // optional class for call/return breakpoints
	Exception string // exception class, "*" for all
	Disabled  bool
	Temporary bool
	// HitValue and HitCondition (">=", "==", "%") restrict when the
	// breakpoint triggers.
	HitValue     int
	HitCondition string
	HitCount     int
	Resolved     bool
}

func (bp Breakpoint) effectiveType() BreakpointType {
	switch {
	case bp.Type != "":
		return bp.Type
	case bp.Exception != "":
		return BreakpointException
	case bp.Function != "":
		return BreakpointCall
	case bp.Condition != "":
		return BreakpointConditional
	default:
		return BreakpointLine
	}
}

func (s *Session) convertBreakpoint(xb *xmlBreakpoint) Breakpoint {
	bp := Breakpoint{
		ID:           xb.ID,
		Type:         BreakpointType(xb.Type),
		FileURI:      xb.Filename,
		Line:         atoi(xb.Lineno),
		Function:     xb.Function,
		Class:        xb.Class,
		Exception:    xb.Exception,
		Disabled:     xb.State == "disabled",
		Temporary:    xb.Temporary == "1",
		HitValue:     atoi(xb.HitValue),
		HitCondition: xb.HitCondition,
		HitCount:     atoi(xb.HitCount),
		Resolved:     xb.Resolved == "resolved",
		Condition:    xb.Expression.decode(),
	}
	if bp.FileURI != "" {
		bp.File = s.toHost(bp.FileURI)
	}
	return bp
}

// Stream is program output forwarded by the engine (see stdout/stderr
// commands).
type Stream struct {
	Type string // "stdout" or "stderr"
	Data string
}

// Notification is an asynchronous engine notification other than
// breakpoint_resolved, e.g. "error" with the notify_ok feature.
type Notification struct {
	Name    string
	FileURI string
	File    string
	Line    int
	Type    string // e.g. "Warning"
	Code    string
	Message string
}

// PropertyRequest selects a property for [Session.Property] and
// [Session.PropertyValue].
type PropertyRequest struct {
	FullName string // e.g. "$foo->bar[2]"
	Depth    int    // stack depth, 0 = current frame
	Context  int    // context ID, see ContextLocals etc.
	Page     int    // child page to return
	MaxData  int    // maximum data size, 0 = engine default
	Key      string // property key as returned by the engine (optional)
}

func (r PropertyRequest) args() []string {
	a := []string{"-n", r.FullName, "-d", itoa(r.Depth), "-c", itoa(r.Context)}
	if r.Page > 0 {
		a = append(a, "-p", itoa(r.Page))
	}
	if r.MaxData > 0 {
		a = append(a, "-m", itoa(r.MaxData))
	}
	if r.Key != "" {
		a = append(a, "-k", r.Key)
	}
	return a
}

// Response is a raw engine response returned by [Session.Send].
type Response struct {
	Command       string
	TransactionID string
	Status        Status
	Reason        string
	Success       bool
	Raw           []byte // complete XML of the response
}

// Feature is a feature name/value pair for feature_set.
type Feature struct {
	Name  string
	Value string
}

// DefaultFeatures are negotiated with every new session if
// [Config.Features] is nil.
var DefaultFeatures = []Feature{
	{"max_depth", "1"},
	{"max_children", "100"},
	{"max_data", "8192"},
	{"show_hidden", "1"},
	{"resolved_breakpoints", "1"},
	{"notify_ok", "1"},
	{"extended_properties", "1"},
}

func isContinuation(cmd string) bool {
	switch cmd {
	case "run", "step_into", "step_over", "step_out", "stop", "detach":
		return true
	}
	return false
}
