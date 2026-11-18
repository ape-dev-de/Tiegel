package langphp

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// InlayHintsParams are the params of "lsp.inlayHints": a document and
// optionally a range (default: the whole document).
type InlayHintsParams struct {
	Path  string   `json:"path"`
	Start *api.Pos `json:"start,omitempty"`
	End   *api.Pos `json:"end,omitempty"`
}

// InlayHint is a hint at a position of the document (API coordinates).
type InlayHint struct {
	api.TextPos
	Label string `json:"label"`
	// Kind is "type", "parameter" or "" (other).
	Kind         string `json:"kind,omitempty"`
	PaddingLeft  bool   `json:"paddingLeft,omitempty"`
	PaddingRight bool   `json:"paddingRight,omitempty"`
}

// InlayHintsResult is the result of "lsp.inlayHints".
type InlayHintsResult struct {
	// Supported is false when the server offers no inlay hints.
	Supported bool        `json:"supported"`
	Hints     []InlayHint `json:"hints"`
	// Version is the document version the hints belong to.
	Version uint64 `json:"version"`
}

func (m *Module) lspInlayHints(ctx context.Context, p InlayHintsParams) (InlayHintsResult, error) {
	r, err := m.prepare(p.Path, nil)
	if err != nil {
		return InlayHintsResult{}, err
	}
	res := InlayHintsResult{Hints: []InlayHint{}, Version: r.snap.Version}
	if !r.s.client.CanInlayHints() {
		return res, nil
	}
	res.Supported = true
	text := string(r.snap.Text)
	start, end := 0, len(text)
	if p.Start != nil {
		if start, err = p.Start.ByteOffset(r.snap.Text); err != nil {
			return InlayHintsResult{}, err
		}
	}
	if p.End != nil {
		if end, err = p.End.ByteOffset(r.snap.Text); err != nil {
			return InlayHintsResult{}, err
		}
	}
	rg := protocol.Range{Start: protocol.PositionOf(text, start), End: protocol.PositionOf(text, max(start, end))}
	hs, err := r.s.client.InlayHints(ctx, r.uri, rg)
	if err != nil {
		return InlayHintsResult{}, lspErr("inlay hints", err)
	}
	for _, h := range hs {
		off, err := protocol.OffsetOf(text, h.Position)
		if err != nil {
			continue // outside the synced text
		}
		ih := InlayHint{TextPos: api.PosAt(r.snap.Text, off), Label: h.LabelText(), PaddingLeft: h.PaddingLeft, PaddingRight: h.PaddingRight}
		switch h.Kind {
		case protocol.InlayHintType:
			ih.Kind = "type"
		case protocol.InlayHintParameter:
			ih.Kind = "parameter"
		}
		if ih.Label != "" {
			res.Hints = append(res.Hints, ih)
		}
	}
	return res, nil
}
