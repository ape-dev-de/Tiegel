package langphp

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// PosParams address a position in an open PHP document (API conventions:
// byte offset or 1-based line/column).
type PosParams struct {
	Path string  `json:"path"`
	At   api.Pos `json:"at"`
}

// request is the prepared state of a feature request.
type request struct {
	s    *server
	snap api.DocSnapshot
	uri  protocol.DocumentURI
	pos  protocol.Position
}

func (m *Module) prepare(path string, at *api.Pos) (request, error) {
	s := m.current()
	if s == nil {
		return request{}, notRunning()
	}
	snap, uri, err := m.ensureSync(s, path)
	if err != nil {
		return request{}, err
	}
	r := request{s: s, snap: snap, uri: uri}
	if at != nil {
		off, err := at.ByteOffset(snap.Text)
		if err != nil {
			return request{}, err
		}
		r.pos = protocol.PositionOf(string(snap.Text), off)
	}
	return r, nil
}

// TextEdit is an edit in API coordinates.
type TextEdit struct {
	Range   api.TextRange `json:"range"`
	NewText string        `json:"newText"`
}

// CompletionItem is one completion proposal.
type CompletionItem struct {
	Label         string     `json:"label"`
	Kind          string     `json:"kind,omitempty"`
	Detail        string     `json:"detail,omitempty"`
	Documentation string     `json:"documentation,omitempty"`
	InsertText    string     `json:"insertText,omitempty"`
	Snippet       bool       `json:"snippet,omitempty"`
	SortText      string     `json:"sortText,omitempty"`
	FilterText    string     `json:"filterText,omitempty"`
	Deprecated    bool       `json:"deprecated,omitempty"`
	Edit          *TextEdit  `json:"edit,omitempty"`
	Additional    []TextEdit `json:"additionalEdits,omitempty"`
}

// CompletionResult is the result of "lsp.completion".
type CompletionResult struct {
	Incomplete bool             `json:"incomplete"`
	Items      []CompletionItem `json:"items"`
}

var kindNames = map[protocol.CompletionItemKind]string{
	protocol.KindText: "text", protocol.KindMethod: "method", protocol.KindFunction: "function",
	protocol.KindConstructor: "constructor", protocol.KindField: "field", protocol.KindVariable: "variable",
	protocol.KindClass: "class", protocol.KindInterface: "interface", protocol.KindModule: "module",
	protocol.KindProperty: "property", protocol.KindUnit: "unit", protocol.KindValue: "value",
	protocol.KindEnum: "enum", protocol.KindKeyword: "keyword", protocol.KindSnippet: "snippet",
	protocol.KindColor: "color", protocol.KindFile: "file", protocol.KindReference: "reference",
	protocol.KindFolder: "folder", protocol.KindEnumMember: "enumMember", protocol.KindConstant: "constant",
	protocol.KindStruct: "struct", protocol.KindEvent: "event", protocol.KindOperator: "operator",
	protocol.KindTypeParameter: "typeParameter",
}

func markup(mc *protocol.MarkupContent) string {
	if mc == nil {
		return ""
	}
	return mc.Value
}

func (m *Module) lspCompletion(ctx context.Context, p PosParams) (CompletionResult, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return CompletionResult{}, err
	}
	list, err := r.s.client.Completion(ctx, r.uri, r.pos, nil)
	if err != nil {
		return CompletionResult{}, lspErr("completion", err)
	}
	res := CompletionResult{Incomplete: list.IsIncomplete, Items: []CompletionItem{}}
	for _, it := range list.Items {
		ci := CompletionItem{
			Label: it.Label, Kind: kindNames[it.Kind], Detail: it.Detail, Documentation: markup(it.Documentation),
			InsertText: it.InsertText, Snippet: it.InsertTextFormat == protocol.InsertTextSnippet,
			SortText: it.SortText, FilterText: it.FilterText, Deprecated: it.Deprecated,
		}
		if it.TextEdit != nil {
			ci.Edit = &TextEdit{Range: textRange(r.snap.Text, it.TextEdit.EditRange()), NewText: it.TextEdit.NewText}
		}
		for _, e := range it.AdditionalTextEdits {
			ci.Additional = append(ci.Additional, TextEdit{Range: textRange(r.snap.Text, e.Range), NewText: e.NewText})
		}
		res.Items = append(res.Items, ci)
	}
	return res, nil
}

// HoverResult is the result of "lsp.hover"; Found is false if the server
// has nothing to show.
type HoverResult struct {
	Found    bool           `json:"found"`
	Kind     string         `json:"kind,omitempty"`
	Contents string         `json:"contents,omitempty"`
	Range    *api.TextRange `json:"range,omitempty"`
}

func (m *Module) lspHover(ctx context.Context, p PosParams) (HoverResult, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return HoverResult{}, err
	}
	h, err := r.s.client.Hover(ctx, r.uri, r.pos)
	if err != nil {
		return HoverResult{}, lspErr("hover", err)
	}
	if h == nil {
		return HoverResult{}, nil
	}
	res := HoverResult{Found: true, Kind: h.Contents.Kind, Contents: h.Contents.Value}
	if h.Range != nil {
		tr := textRange(r.snap.Text, *h.Range)
		res.Range = &tr
	}
	return res, nil
}

// Parameter is one parameter of a signature.
type Parameter struct {
	Label         string `json:"label"`
	Documentation string `json:"documentation,omitempty"`
}

// Signature is one signature of signature help.
type Signature struct {
	Label           string      `json:"label"`
	Documentation   string      `json:"documentation,omitempty"`
	Parameters      []Parameter `json:"parameters"`
	ActiveParameter *int        `json:"activeParameter,omitempty"`
}

// SignatureResult is the result of "lsp.signature".
type SignatureResult struct {
	Signatures      []Signature `json:"signatures"`
	ActiveSignature int         `json:"activeSignature"`
	ActiveParameter int         `json:"activeParameter"`
}

// paramLabel resolves a parameter label given as UTF-16 offsets into the
// signature label.
func paramLabel(sig string, l protocol.ParameterLabel) string {
	if l.Offsets == nil {
		return l.Text
	}
	start := protocol.UTF16ToByte(sig, l.Offsets[0])
	end := protocol.UTF16ToByte(sig, l.Offsets[1])
	if start > end || end > len(sig) {
		return ""
	}
	return sig[start:end]
}

func (m *Module) lspSignature(ctx context.Context, p PosParams) (SignatureResult, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return SignatureResult{}, err
	}
	h, err := r.s.client.SignatureHelp(ctx, r.uri, r.pos)
	if err != nil {
		return SignatureResult{}, lspErr("signature help", err)
	}
	res := SignatureResult{Signatures: []Signature{}}
	if h == nil {
		return res, nil
	}
	res.ActiveSignature, res.ActiveParameter = h.ActiveSignature, h.ActiveParameter
	for _, s := range h.Signatures {
		sig := Signature{Label: s.Label, Documentation: markup(s.Documentation), Parameters: []Parameter{}, ActiveParameter: s.ActiveParameter}
		for _, prm := range s.Parameters {
			sig.Parameters = append(sig.Parameters, Parameter{Label: paramLabel(s.Label, prm.Label), Documentation: markup(prm.Documentation)})
		}
		res.Signatures = append(res.Signatures, sig)
	}
	return res, nil
}

// Location is a range in a file, in API coordinates.
type Location struct {
	Path  string        `json:"path"`
	File  string        `json:"file"`
	Range api.TextRange `json:"range"`
}

func (m *Module) locations(r request, locs []protocol.Location) []Location {
	out := []Location{}
	texts := map[string][]byte{r.snap.File: r.snap.Text}
	for _, l := range locs {
		abs, err := l.URI.Path()
		if err != nil {
			continue
		}
		text, ok := texts[abs]
		if !ok {
			text, _, _ = m.app.ReadText(abs)
			texts[abs] = text
		}
		out = append(out, Location{Path: m.app.DisplayPath(abs), File: abs, Range: textRange(text, l.Range)})
	}
	return out
}

func (m *Module) lspDefinition(ctx context.Context, p PosParams) ([]Location, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return nil, err
	}
	locs, err := r.s.client.Definition(ctx, r.uri, r.pos)
	if err != nil {
		return nil, lspErr("definition", err)
	}
	return m.locations(r, locs), nil
}

func (m *Module) lspImplementation(ctx context.Context, p PosParams) ([]Location, error) {
	r, err := m.prepare(p.Path, &p.At)
	if err != nil {
		return nil, err
	}
	locs, err := r.s.client.Implementation(ctx, r.uri, r.pos)
	if err != nil {
		return nil, lspErr("implementation", err)
	}
	return m.locations(r, locs), nil
}

// SemanticToken is a classified range of the document.
type SemanticToken struct {
	Range     api.TextRange `json:"range"`
	Type      string        `json:"type"`
	Modifiers []string      `json:"modifiers,omitempty"`
}

func (m *Module) lspSemanticTokens(ctx context.Context, p api.PathParams) ([]SemanticToken, error) {
	r, err := m.prepare(p.Path, nil)
	if err != nil {
		return nil, err
	}
	toks, err := r.s.client.SemanticTokensFull(ctx, r.uri)
	if err != nil {
		return nil, lspErr("semantic tokens", err)
	}
	out := []SemanticToken{}
	for _, t := range toks {
		rg := protocol.Range{
			Start: protocol.Position{Line: t.Line, Character: t.StartChar},
			End:   protocol.Position{Line: t.Line, Character: t.StartChar + t.Length},
		}
		out = append(out, SemanticToken{Range: textRange(r.snap.Text, rg), Type: t.Type, Modifiers: t.Modifiers})
	}
	return out, nil
}
