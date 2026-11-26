// Package mcp is a minimal Model Context Protocol server over stdio
// (D562): newline-delimited JSON-RPC 2.0 with initialize, ping,
// tools/list and tools/call. Tiegel uses it to give coding agents tools
// such as the project knowledge search. It has no dependencies and
// imports no Gio.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// LatestProtocol is the newest protocol version the server speaks;
// Protocols lists all accepted versions.
const LatestProtocol = "2025-11-25"

// Protocols are the protocol versions the server accepts.
var Protocols = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// Tool is a tool the server offers.
type Tool struct {
	Name        string
	Description string
	// InputSchema is the JSON schema of the arguments (an object).
	InputSchema json.RawMessage
	// Call runs the tool; an error is returned to the client as tool
	// result with isError (the model sees it), not as protocol error.
	Call func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server serves tools over a stream.
type Server struct {
	Name    string
	Version string
	// Instructions are sent with initialize (how to use the tools).
	Instructions string
	Tools        []Tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Content is a content item of a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallResult is the result of tools/call.
type CallResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// ToolInfo is a tool in tools/list.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Serve reads requests from r and writes responses to w until r ends or
// ctx is cancelled. Requests are answered in order.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	write := func(v any) error {
		mu.Lock()
		defer mu.Unlock()
		return enc.Encode(v)
	}
	lines := make(chan []byte)
	errc := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 32<<20)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-ctx.Done():
				return
			}
		}
		errc <- sc.Err()
		close(lines)
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return <-errc
			}
			if len(line) == 0 {
				continue
			}
			if resp := s.handle(ctx, line); resp != nil {
				if err := write(resp); err != nil {
					return fmt.Errorf("mcp: write: %w", err)
				}
			}
		}
	}
}

func (s *Server) handle(ctx context.Context, line []byte) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{CodeParse, "parse error: " + err.Error()}}
	}
	notification := len(req.ID) == 0
	res, rerr := s.dispatch(ctx, req)
	if notification {
		return nil
	}
	resp := &response{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = res
	}
	return resp
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := LatestProtocol
		if slices.Contains(Protocols, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		res := map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
		if s.Instructions != "" {
			res["instructions"] = s.Instructions
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "tools/list":
		tools := make([]ToolInfo, 0, len(s.Tools))
		for _, t := range s.Tools {
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, ToolInfo{Name: t.Name, Description: t.Description, InputSchema: schema})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{CodeInvalidParams, err.Error()}
		}
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			args := p.Arguments
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			text, err := safeCall(ctx, t, args)
			if err != nil {
				return CallResult{Content: []Content{{Type: "text", Text: err.Error()}}, IsError: true}, nil
			}
			return CallResult{Content: []Content{{Type: "text", Text: text}}}, nil
		}
		return nil, &rpcError{CodeInvalidParams, "unknown tool " + p.Name}
	}
	return nil, &rpcError{CodeMethodNotFound, "method not found: " + req.Method}
}

// safeCall runs a tool and turns a panic into an error (a broken tool
// must not end the server).
func safeCall(ctx context.Context, t Tool, args json.RawMessage) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool %s failed: %v", t.Name, r)
		}
	}()
	if t.Call == nil {
		return "", errors.New("tool has no implementation")
	}
	return t.Call(ctx, args)
}
