// Package jsonrpc2 implements a JSON-RPC 2.0 connection with the
// Content-Length based framing used by the Language Server Protocol.
//
// A Conn is symmetric: it can issue requests and notifications to the peer
// and dispatch requests and notifications received from the peer to
// registered handlers. It only depends on the standard library.
package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Standard JSON-RPC and LSP error codes.
const (
	CodeParseError       = -32700
	CodeInvalidRequest   = -32600
	CodeMethodNotFound   = -32601
	CodeInvalidParams    = -32602
	CodeInternalError    = -32603
	CodeServerNotInit    = -32002
	CodeRequestFailed    = -32803
	CodeServerCancelled  = -32802
	CodeContentModified  = -32801
	CodeRequestCancelled = -32800
)

// Error is a JSON-RPC error object. It is returned by Conn.Call when the
// peer answers with an error and may be returned by handlers to control the
// error sent to the peer.
type Error struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("jsonrpc2: code %d: %s", e.Code, e.Message)
}

// NewError creates an Error with the given code and message.
func NewError(code int64, message string) *Error {
	return &Error{Code: code, Message: message}
}

// ID is a JSON-RPC request identifier, either a number or a string.
// The zero value is the number 0.
type ID struct {
	num   int64
	str   string
	isStr bool
}

// NumberID returns a numeric ID.
func NumberID(n int64) ID { return ID{num: n} }

// StringID returns a string ID.
func StringID(s string) ID { return ID{str: s, isStr: true} }

// String returns a human readable form of the ID.
func (id ID) String() string {
	if id.isStr {
		return strconv.Quote(id.str)
	}
	return strconv.FormatInt(id.num, 10)
}

// MarshalJSON implements json.Marshaler.
func (id ID) MarshalJSON() ([]byte, error) {
	if id.isStr {
		return json.Marshal(id.str)
	}
	return []byte(strconv.FormatInt(id.num, 10)), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (id *ID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*id = StringID(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("jsonrpc2: invalid id %s: %w", data, err)
	}
	*id = NumberID(n)
	return nil
}

// Request is an incoming request or notification from the peer.
type Request struct {
	// ID is nil for notifications.
	ID     *ID
	Method string
	Params json.RawMessage
}

// IsNotification reports whether the request expects no response.
func (r *Request) IsNotification() bool { return r.ID == nil }

// UnmarshalParams decodes the request parameters into v.
func (r *Request) UnmarshalParams(v any) error {
	if len(r.Params) == 0 || string(r.Params) == "null" {
		return nil
	}
	if err := json.Unmarshal(r.Params, v); err != nil {
		return &Error{Code: CodeInvalidParams, Message: err.Error()}
	}
	return nil
}

// wireMessage is the union of all JSON-RPC message shapes.
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

var errInvalidMessage = errors.New("jsonrpc2: invalid message")

func (m *wireMessage) isRequest() bool  { return m.Method != "" && m.ID != nil }
func (m *wireMessage) isNotify() bool   { return m.Method != "" && m.ID == nil }
func (m *wireMessage) isResponse() bool { return m.Method == "" && m.ID != nil }
