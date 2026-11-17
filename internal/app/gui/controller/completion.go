package controller

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Completion is the state of the completion popup.
type Completion struct {
	Open    bool
	Loading bool
	File    string
	// Start is the byte offset where the completed word starts; ReqCaret
	// is the caret at request time.
	Start, ReqCaret int
	// Prefix is the typed text between Start and the caret.
	Prefix     string
	Items      []CompletionItem
	Incomplete bool
	// Visible indexes Items after filtering; Selected indexes Visible.
	Visible  []int
	Selected int
	seq      int
}

// SignatureHelp is the state of the signature popup.
type SignatureHelp struct {
	Open  bool
	File  string
	Label string
	// ParamStart/ParamEnd mark the active parameter within Label (byte
	// offsets; equal if unknown).
	ParamStart, ParamEnd int
	seq                  int
}

// HoverInfo is the state of the hover popup.
type HoverInfo struct {
	Open   bool
	File   string
	Offset int
	Text   string
	seq    int
}

// Edit replaces bytes [Start, End) of a document with Text.
type Edit struct {
	Start, End int
	Text       string
}

// IsIdentByte reports whether b belongs to a PHP identifier or variable.
func IsIdentByte(b byte) bool {
	return b == '_' || b == '$' || b == '\\' || b >= 0x80 ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// WordStart returns the start of the identifier that ends at off in text.
func WordStart(text []byte, off int) int {
	i := off
	for i > 0 && IsIdentByte(text[i-1]) && text[i-1] != '\\' {
		i--
		if text[i] == '$' {
			break
		}
	}
	return i
}

// AutoTrigger decides from the text before the caret (after typing) whether
// completion should open automatically: after "->", "?->", "::" and "$".
func AutoTrigger(before string) bool {
	return strings.HasSuffix(before, "->") || strings.HasSuffix(before, "::") || strings.HasSuffix(before, "$")
}

// SignatureTrigger reports whether typing typed should request (open) or
// close the signature help.
func SignatureTrigger(typed string) (request, closeHelp bool) {
	switch typed {
	case "(", ",":
		return true, false
	case ")":
		return false, true
	}
	return false, false
}

// RequestCompletion asks the language server for completions at caret in
// file; start is the beginning of the word being completed and prefix the
// text between start and caret.
func (c *Controller) RequestCompletion(file string, caret, start int, prefix string) {
	if !c.Status.LSPRunning {
		c.Status.Notice = NoticeNeedsLSP("Completion")
		return
	}
	c.Completion.seq++
	seq := c.Completion.seq
	c.Completion = Completion{Open: true, Loading: true, File: file, Start: start, ReqCaret: caret, Prefix: prefix, seq: seq}
	c.Go(func(ctx context.Context) func() {
		var res completionResult
		err := c.call(ctx, "lsp.completion", posParams{Path: file, At: pos{Offset: caret}}, &res)
		return func() {
			if c.Completion.seq != seq || !c.Completion.Open {
				return
			}
			c.Completion.Loading = false
			if err != nil {
				c.CloseCompletion()
				c.notice(err)
				return
			}
			c.Completion.Items, c.Completion.Incomplete = res.Items, res.Incomplete
			c.filterCompletion()
		}
	})
}

// UpdateCompletion is called after the user typed or moved while the popup
// is open: prefix is the text from Start to the caret. It returns false if
// the popup was closed (caret left the word).
func (c *Controller) UpdateCompletion(prefix string, caret int) bool {
	cp := &c.Completion
	if !cp.Open {
		return false
	}
	if caret < cp.Start || strings.ContainsAny(prefix, " \t\n();,{}[]=") {
		c.CloseCompletion()
		return false
	}
	if prefix == cp.Prefix {
		return true
	}
	cp.Prefix = prefix
	if cp.Incomplete && !cp.Loading {
		// The server's list was cut; ask again for the longer prefix.
		c.RequestCompletion(cp.File, caret, cp.Start, prefix)
		return true
	}
	c.filterCompletion()
	return true
}

// CloseCompletion hides the popup.
func (c *Controller) CloseCompletion() {
	c.Completion.seq++
	c.Completion = Completion{seq: c.Completion.seq}
}

// MoveSelection moves the completion selection by delta (wrapping).
func (c *Controller) MoveSelection(delta int) {
	n := len(c.Completion.Visible)
	if n == 0 {
		return
	}
	c.Completion.Selected = ((c.Completion.Selected+delta)%n + n) % n
}

func (c *Controller) filterCompletion() {
	cp := &c.Completion
	type scored struct{ idx, rank, score int }
	var list []scored
	p := strings.ToLower(strings.TrimPrefix(cp.Prefix, "$"))
	for i, it := range cp.Items {
		key := it.FilterText
		if key == "" {
			key = it.Label
		}
		k := strings.ToLower(strings.TrimPrefix(key, "$"))
		switch {
		case p == "" || strings.HasPrefix(k, p):
			list = append(list, scored{i, 0, 0})
		default:
			if sc, _, ok := FuzzyMatch(p, k); ok {
				list = append(list, scored{i, 1, -sc})
			}
		}
	}
	sort.SliceStable(list, func(a, b int) bool {
		x, y := list[a], list[b]
		if x.rank != y.rank {
			return x.rank < y.rank
		}
		if x.score != y.score {
			return x.score < y.score
		}
		ix, iy := cp.Items[x.idx], cp.Items[y.idx]
		if ix.SortText != iy.SortText {
			return ix.SortText < iy.SortText
		}
		return ix.Label < iy.Label
	})
	cp.Visible = cp.Visible[:0]
	for _, s := range list {
		cp.Visible = append(cp.Visible, s.idx)
	}
	cp.Selected = 0
	if len(cp.Visible) == 0 && !cp.Loading && !cp.Incomplete && cp.Prefix != "" {
		c.CloseCompletion()
	}
}

// SelectedItem returns the selected item.
func (c *Controller) SelectedItem() (CompletionItem, bool) {
	cp := &c.Completion
	if !cp.Open || cp.Selected >= len(cp.Visible) {
		return CompletionItem{}, false
	}
	return cp.Items[cp.Visible[cp.Selected]], true
}

// AcceptCompletion returns the edits for the selected item at the current
// caret (in descending offset order, ready to apply one after the other)
// and the caret offset after applying them, then closes the popup.
func (c *Controller) AcceptCompletion(caret int) (edits []Edit, newCaret int, ok bool) {
	it, ok := c.SelectedItem()
	if !ok {
		return nil, 0, false
	}
	cp := c.Completion
	c.CloseCompletion()
	edits, newCaret = CompletionEdits(it, cp.Start, cp.ReqCaret, caret)
	return edits, newCaret, true
}

// CompletionEdits computes the edits of item: the main edit replaces the
// word [start, caret) (or the item's text edit range, extended by what was
// typed since the request) and additional edits (e.g. use statements) are
// applied as well. Snippets are simplified to plain text with the caret at
// the first placeholder.
func CompletionEdits(it CompletionItem, start, reqCaret, caret int) ([]Edit, int) {
	text := it.InsertText
	if text == "" {
		text = it.Label
	}
	from, to := start, caret
	if it.Edit != nil {
		text = it.Edit.NewText
		from = it.Edit.Range.Start.Offset
		to = it.Edit.Range.End.Offset + (caret - reqCaret)
		if to < caret {
			to = caret
		}
	}
	cur := len(text)
	if it.Snippet {
		text, cur = ExpandSnippet(text)
	}
	main := Edit{Start: from, End: to, Text: text}
	edits := []Edit{main}
	shift := 0
	for _, a := range it.Additional {
		e := Edit{Start: a.Range.Start.Offset, End: a.Range.End.Offset, Text: a.NewText}
		if e.End <= from {
			shift += len(e.Text) - (e.End - e.Start)
		}
		edits = append(edits, e)
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	return edits, from + cur + shift
}

// ExpandSnippet turns an LSP snippet into plain text: placeholders keep
// their default text, choices their first option, tab stops vanish. caret
// is the offset of the first tab stop ($1, else $0, else the end).
func ExpandSnippet(s string) (text string, caret int) {
	var b strings.Builder
	stops := map[int]int{}
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s) && strings.IndexByte(`$}\`, s[i+1]) >= 0:
			b.WriteByte(s[i+1])
			i += 2
		case ch == '$' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n, _ := strconv.Atoi(s[i+1 : j])
			if _, ok := stops[n]; !ok {
				stops[n] = b.Len()
			}
			i = j
		case ch == '$' && i+1 < len(s) && s[i+1] == '{':
			j := i + 2
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n, err := strconv.Atoi(s[i+2 : j])
			if err != nil {
				b.WriteByte(ch)
				i++
				continue
			}
			if _, ok := stops[n]; !ok {
				stops[n] = b.Len()
			}
			end := matchingBrace(s, i+1)
			inner := ""
			if j < end {
				inner = s[j:end]
			}
			switch {
			case strings.HasPrefix(inner, ":"):
				t, _ := ExpandSnippet(inner[1:])
				b.WriteString(t)
			case strings.HasPrefix(inner, "|"):
				opts := strings.TrimSuffix(inner[1:], "|")
				b.WriteString(strings.SplitN(opts, ",", 2)[0])
			}
			i = end + 1
		default:
			b.WriteByte(ch)
			i++
		}
	}
	text = b.String()
	caret = len(text)
	best := -1
	for n, at := range stops {
		if n > 0 && (best < 0 || n < best) {
			best = n
			caret = at
		}
	}
	if best < 0 {
		if at, ok := stops[0]; ok {
			caret = at
		}
	}
	return text, caret
}

// matchingBrace returns the index of the '}' closing the '{' at open.
func matchingBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s) - 1
}

// ---- signature help, hover, navigation -------------------------------------

// RequestSignature asks for signature help at caret.
func (c *Controller) RequestSignature(file string, caret int) {
	if !c.Status.LSPRunning {
		return
	}
	c.Signature.seq++
	seq := c.Signature.seq
	c.Go(func(ctx context.Context) func() {
		var res signatureResult
		err := c.call(ctx, "lsp.signature", posParams{Path: file, At: pos{Offset: caret}}, &res)
		return func() {
			if c.Signature.seq != seq {
				return
			}
			if err != nil || len(res.Signatures) == 0 {
				c.Signature = SignatureHelp{seq: seq}
				return
			}
			si := min(max(res.ActiveSignature, 0), len(res.Signatures)-1)
			sig := res.Signatures[si]
			h := SignatureHelp{Open: true, File: file, Label: sig.Label, seq: seq}
			if ap := res.ActiveParameter; ap >= 0 && ap < len(sig.Parameters) {
				if i := strings.Index(sig.Label, sig.Parameters[ap].Label); i >= 0 {
					h.ParamStart, h.ParamEnd = i, i+len(sig.Parameters[ap].Label)
				}
			}
			c.Signature = h
		}
	})
}

// CloseSignature hides the signature help.
func (c *Controller) CloseSignature() {
	c.Signature.seq++
	c.Signature = SignatureHelp{seq: c.Signature.seq}
}

// RequestHover shows diagnostics messages (immediately) and the language
// server's hover text at off.
func (c *Controller) RequestHover(file string, off int, diagnostics []string) {
	c.Hover.seq++
	seq := c.Hover.seq
	text := strings.Join(diagnostics, "\n")
	c.Hover = HoverInfo{Open: text != "", File: file, Offset: off, Text: text, seq: seq}
	if !c.Status.LSPRunning {
		return
	}
	c.Go(func(ctx context.Context) func() {
		var res hoverResult
		err := c.call(ctx, "lsp.hover", posParams{Path: file, At: pos{Offset: off}}, &res)
		return func() {
			if c.Hover.seq != seq || err != nil || !res.Found || strings.TrimSpace(res.Contents) == "" {
				return
			}
			h := strings.TrimSpace(stripMarkdown(res.Contents))
			if c.Hover.Text != "" {
				h = c.Hover.Text + "\n\n" + h
			}
			c.Hover.Text, c.Hover.Open = h, true
		}
	})
}

// CloseHover hides the hover popup.
func (c *Controller) CloseHover() {
	c.Hover.seq++
	c.Hover = HoverInfo{seq: c.Hover.seq}
}

// stripMarkdown removes code fences and emphasis markers from hover text.
func stripMarkdown(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			continue
		}
		out = append(out, strings.NewReplacer("**", "", "__", "", "`", "").Replace(l))
	}
	return strings.Join(out, "\n")
}

// GoToDefinition jumps to the definition (method "lsp.definition") or the
// implementations ("lsp.implementation") of the symbol at off. Several
// targets are offered in Locations.
func (c *Controller) GoToDefinition(cur Location, method string) {
	if !c.Status.LSPRunning {
		c.Status.Notice = NoticeNeedsLSP("Navigation")
		return
	}
	c.Go(func(ctx context.Context) func() {
		var locs []Location
		err := c.call(ctx, method, posParams{Path: cur.File, At: pos{Offset: cur.Range.Start.Offset}}, &locs)
		return func() {
			switch {
			case err != nil:
				c.notice(err)
			case len(locs) == 0:
				c.Status.Notice = "No target found"
			case len(locs) == 1:
				c.GoTo(cur, c.normLoc(locs[0]))
			default:
				c.Locations = c.Locations[:0]
				for _, l := range locs {
					c.Locations = append(c.Locations, c.normLoc(l))
				}
				c.LocationsTitle = "Choose target"
				if method == "lsp.implementation" {
					c.LocationsTitle = "Choose implementation"
				}
				c.Nav.Push(cur)
			}
		}
	})
}

func (c *Controller) normLoc(l Location) Location {
	if l.File == "" {
		l.File = c.Abs(l.Path)
	}
	if l.Path == "" {
		l.Path = c.Rel(l.File)
	}
	return l
}

// ChooseLocation opens one of Locations and clears the choice.
func (c *Controller) ChooseLocation(i int) {
	if i >= 0 && i < len(c.Locations) {
		loc := c.Locations[i]
		c.OpenFile(loc.File, &loc)
	}
	c.Locations = nil
}

// ParseLineCol parses "line" or "line:col" (1-based) as typed in the go to
// line dialog.
func ParseLineCol(s string) (line, col int, ok bool) {
	s = strings.TrimSpace(s)
	l, cs, hasCol := strings.Cut(s, ":")
	line, err := strconv.Atoi(strings.TrimSpace(l))
	if err != nil || line < 1 {
		return 0, 0, false
	}
	col = 1
	if hasCol {
		if col, err = strconv.Atoi(strings.TrimSpace(cs)); err != nil || col < 1 {
			return 0, 0, false
		}
	}
	return line, col, true
}

// IsWordRune reports whether r continues an identifier (for typed text).
func IsWordRune(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
