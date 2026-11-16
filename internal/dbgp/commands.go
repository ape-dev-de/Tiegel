package dbgp

import (
	"context"
	"errors"
	"fmt"
)

// Status queries the engine's execution status.
func (s *Session) Status(ctx context.Context) (Status, error) {
	r, err := s.send(ctx, "status", nil, nil)
	if err != nil {
		return "", err
	}
	return Status(r.Status), nil
}

// FeatureSet sets an engine feature such as max_depth or
// resolved_breakpoints.
func (s *Session) FeatureSet(ctx context.Context, name, value string) error {
	r, err := s.send(ctx, "feature_set", []string{"-n", name, "-v", value}, nil)
	if err != nil {
		return err
	}
	if r.Success != "1" {
		return fmt.Errorf("dbgp: feature %s=%s not accepted", name, value)
	}
	return nil
}

func (s *Session) continuation(ctx context.Context, cmd string) (BreakInfo, error) {
	r, err := s.send(ctx, cmd, nil, nil)
	if err != nil {
		return BreakInfo{}, err
	}
	return s.breakInfo(r), nil
}

// Run continues execution until the next breakpoint or the end of the
// script. The returned status is StatusBreak at a breakpoint and
// StatusStopping when the script finished.
func (s *Session) Run(ctx context.Context) (BreakInfo, error) {
	return s.continuation(ctx, "run")
}

// StepInto steps to the next statement, entering function calls.
func (s *Session) StepInto(ctx context.Context) (BreakInfo, error) {
	return s.continuation(ctx, "step_into")
}

// StepOver steps to the next statement in the current scope.
func (s *Session) StepOver(ctx context.Context) (BreakInfo, error) {
	return s.continuation(ctx, "step_over")
}

// StepOut runs until the current function returns.
func (s *Session) StepOut(ctx context.Context) (BreakInfo, error) {
	return s.continuation(ctx, "step_out")
}

// Stop ends the script immediately. The engine closes the connection
// afterwards.
func (s *Session) Stop(ctx context.Context) (BreakInfo, error) {
	return s.continuation(ctx, "stop")
}

// Detach stops debugging and lets the script continue without debugger.
func (s *Session) Detach(ctx context.Context) error {
	_, err := s.continuation(ctx, "detach")
	return err
}

// Break asks a running script to pause. Xdebug answers only if it is able to
// process commands while running.
func (s *Session) Break(ctx context.Context) error {
	r, err := s.send(ctx, "break", nil, nil)
	if err != nil {
		return err
	}
	if r.Success != "" && r.Success != "1" {
		return errors.New("dbgp: break not accepted")
	}
	return nil
}

// SetBreakpoint sets a breakpoint. bp.File is a host path and is translated
// with the session's path mapper. The returned breakpoint carries the
// engine-assigned ID and resolved state.
func (s *Session) SetBreakpoint(ctx context.Context, bp Breakpoint) (Breakpoint, error) {
	bp.Type = bp.effectiveType()
	state := "enabled"
	if bp.Disabled {
		state = "disabled"
	}
	args := []string{"-t", string(bp.Type), "-s", state}
	switch bp.Type {
	case BreakpointLine, BreakpointConditional:
		if bp.File == "" || bp.Line <= 0 {
			return bp, fmt.Errorf("dbgp: %s breakpoint needs file and line", bp.Type)
		}
	case BreakpointCall, BreakpointReturn:
		if bp.Function == "" {
			return bp, fmt.Errorf("dbgp: %s breakpoint needs a function", bp.Type)
		}
		args = append(args, "-m", bp.Function)
		if bp.Class != "" {
			args = append(args, "-a", bp.Class)
		}
	case BreakpointException:
		if bp.Exception == "" {
			bp.Exception = "*"
		}
		args = append(args, "-x", bp.Exception)
	}
	if bp.File != "" {
		bp.FileURI = s.toRemote(bp.File)
		args = append(args, "-f", bp.FileURI)
	}
	if bp.Line > 0 {
		args = append(args, "-n", itoa(bp.Line))
	}
	if bp.Temporary {
		args = append(args, "-r", "1")
	}
	if bp.HitValue > 0 {
		args = append(args, "-h", itoa(bp.HitValue))
		if bp.HitCondition != "" {
			args = append(args, "-o", bp.HitCondition)
		}
	}
	var data []byte
	if bp.Condition != "" {
		data = []byte(bp.Condition)
	}
	r, err := s.send(ctx, "breakpoint_set", args, data)
	if err != nil {
		return bp, err
	}
	bp.ID = r.ID
	bp.Resolved = r.Resolved == "resolved"
	if r.State != "" {
		bp.Disabled = r.State == "disabled"
	}
	return bp, nil
}

// RemoveBreakpoint removes the breakpoint with the given ID.
func (s *Session) RemoveBreakpoint(ctx context.Context, id string) error {
	_, err := s.send(ctx, "breakpoint_remove", []string{"-d", id}, nil)
	return err
}

// SetBreakpointEnabled enables or disables a breakpoint.
func (s *Session) SetBreakpointEnabled(ctx context.Context, id string, enabled bool) error {
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	_, err := s.send(ctx, "breakpoint_update", []string{"-d", id, "-s", state}, nil)
	return err
}

// Breakpoints lists all breakpoints known to the engine.
func (s *Session) Breakpoints(ctx context.Context) ([]Breakpoint, error) {
	r, err := s.send(ctx, "breakpoint_list", nil, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Breakpoint, 0, len(r.Breakpoints))
	for i := range r.Breakpoints {
		out = append(out, s.convertBreakpoint(&r.Breakpoints[i]))
	}
	return out, nil
}

// Stack returns the call stack, innermost frame first. File names are
// mapped to host paths.
func (s *Session) Stack(ctx context.Context) ([]StackFrame, error) {
	r, err := s.send(ctx, "stack_get", nil, nil)
	if err != nil {
		return nil, err
	}
	out := make([]StackFrame, 0, len(r.Stack))
	for _, f := range r.Stack {
		fr := StackFrame{
			Level:   atoi(f.Level),
			Where:   f.Where,
			Type:    f.Type,
			FileURI: f.Filename,
			Line:    atoi(f.Lineno),
		}
		if f.Filename != "" {
			fr.File = s.toHost(f.Filename)
		}
		out = append(out, fr)
	}
	return out, nil
}

// ContextNames returns the variable contexts available at the given stack
// depth.
func (s *Session) ContextNames(ctx context.Context, depth int) ([]Context, error) {
	r, err := s.send(ctx, "context_names", []string{"-d", itoa(depth)}, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Context, 0, len(r.Contexts))
	for _, c := range r.Contexts {
		out = append(out, Context{ID: atoi(c.ID), Name: c.Name})
	}
	return out, nil
}

// Variables returns the variables of a context (see ContextLocals etc.) at
// the given stack depth.
func (s *Session) Variables(ctx context.Context, depth, contextID int) ([]*Property, error) {
	r, err := s.send(ctx, "context_get", []string{"-d", itoa(depth), "-c", itoa(contextID)}, nil)
	if err != nil {
		return nil, err
	}
	return convertProperties(r.Properties), nil
}

// Property fetches a property including one level of children (or the
// requested child page).
func (s *Session) Property(ctx context.Context, req PropertyRequest) (*Property, error) {
	r, err := s.send(ctx, "property_get", req.args(), nil)
	if err != nil {
		return nil, err
	}
	if len(r.Properties) == 0 {
		return nil, fmt.Errorf("dbgp: property_get %s: empty response", req.FullName)
	}
	return convertProperty(&r.Properties[0]), nil
}

// PropertyValue fetches the complete value of a property (not truncated by
// max_data). It returns the decoded value and its type.
func (s *Session) PropertyValue(ctx context.Context, req PropertyRequest) (value, typ string, err error) {
	req.Page = 0
	r, err := s.send(ctx, "property_value", req.args(), nil)
	if err != nil {
		return "", "", err
	}
	if len(r.Properties) > 0 { // some engines wrap the value in a property
		p := convertProperty(&r.Properties[0])
		return p.Value, p.Type, nil
	}
	return decodeText(r.Text, r.Encoding), r.Type, nil
}

// Eval evaluates a PHP expression in the current frame (used for watches).
func (s *Session) Eval(ctx context.Context, expr string) (*Property, error) {
	r, err := s.send(ctx, "eval", nil, []byte(expr))
	if err != nil {
		return nil, err
	}
	if len(r.Properties) == 0 {
		return &Property{Type: "uninitialized"}, nil
	}
	p := convertProperty(&r.Properties[0])
	if p.FullName == "" {
		p.FullName = expr
	}
	if p.Name == "" {
		p.Name = expr
	}
	return p, nil
}

func convertProperties(xs []xmlProperty) []*Property {
	out := make([]*Property, 0, len(xs))
	for i := range xs {
		out = append(out, convertProperty(&xs[i]))
	}
	return out
}
