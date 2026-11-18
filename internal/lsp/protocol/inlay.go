package protocol

import (
	"encoding/json"
	"strings"
)

// MethodInlayHint is the textDocument/inlayHint request (LSP 3.17).
const MethodInlayHint = "textDocument/inlayHint"

// Inlay hint kinds.
const (
	InlayHintType      = 1
	InlayHintParameter = 2
)

// InlayHintParams are the textDocument/inlayHint params.
type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

// InlayHintLabelPart is one part of a structured hint label.
type InlayHintLabelPart struct {
	Value string `json:"value"`
}

// InlayHint is a hint shown inline at Position.
type InlayHint struct {
	Position Position `json:"position"`
	// Label is a string or a list of InlayHintLabelPart.
	Label        json.RawMessage `json:"label"`
	Kind         int             `json:"kind,omitempty"`
	PaddingLeft  bool            `json:"paddingLeft,omitempty"`
	PaddingRight bool            `json:"paddingRight,omitempty"`
}

// LabelText returns the label as plain text (parts concatenated).
func (h InlayHint) LabelText() string {
	var s string
	if json.Unmarshal(h.Label, &s) == nil {
		return s
	}
	var parts []InlayHintLabelPart
	if json.Unmarshal(h.Label, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Value)
	}
	return b.String()
}
