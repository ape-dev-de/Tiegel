package api

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
)

// Editing commands on a selection ("edit.*", M13): the same document
// model operations the editor runs for its shortcuts (Join Lines, Toggle
// Case), applied to a range of an open document as one undo step.

// EditRangeParams address a selection of a document: Start (required)
// and End (default: Start, a caret). Version enables optimistic locking.
type EditRangeParams struct {
	Path    string  `json:"path"`
	Start   Pos     `json:"start"`
	End     *Pos    `json:"end,omitempty"`
	Version *uint64 `json:"version,omitempty"`
}

// EditRangeResult is the document state after the command and the
// resulting selection.
type EditRangeResult struct {
	DocState
	// Changed is false if the command had nothing to do.
	Changed bool `json:"changed"`
	// SelStart and SelEnd are the selection afterwards (byte offsets).
	SelStart int `json:"selStart"`
	SelEnd   int `json:"selEnd"`
}

func (a *App) registerEditOps() {
	mustHandle(a, "edit.joinLines", "Join the lines of a selection (or the caret line with the next) like the editor's Join Lines", a.editJoinLines)
	mustHandle(a, "edit.toggleCase", "Toggle the case of a selection (or the word at the caret)", a.editToggleCase)
	mustHandle(a, "edit.columnSelection", "Selections of a column (block) selection between two corners given as line and visual column, like a middle-mouse or Option drag in the editor", a.editColumnSelection)
}

// ColumnSelectionParams are the params of "edit.columnSelection". From
// is the corner where the selection starts (anchor), To the one it was
// dragged to (head); both are 1-based lines and 1-based visual columns
// (tabs expanded to TabWidth, wide characters count two).
type ColumnSelectionParams struct {
	Path string `json:"path"`
	From Pos    `json:"from"`
	To   Pos    `json:"to"`
	// TabWidth defaults to 4.
	TabWidth int `json:"tabWidth,omitempty"`
}

// ColumnSelection is one line of a column selection.
type ColumnSelection struct {
	Line   int    `json:"line"`   // 1-based
	Anchor int    `json:"anchor"` // byte offsets
	Head   int    `json:"head"`
	Text   string `json:"text"`
}

// ColumnSelectionResult lists the selections, one per line, in document
// order; Primary is the index of the primary one (on the To side).
type ColumnSelectionResult struct {
	Version    uint64            `json:"version"`
	Selections []ColumnSelection `json:"selections"`
	Primary    int               `json:"primary"`
}

func (a *App) editColumnSelection(_ context.Context, p ColumnSelectionParams) (ColumnSelectionResult, error) {
	d, err := a.doc(p.Path)
	if err != nil {
		return ColumnSelectionResult{}, err
	}
	if p.From.Line < 1 || p.To.Line < 1 {
		return ColumnSelectionResult{}, Errorf(CodeInvalidParams, "from and to need a line (1-based)")
	}
	tw := p.TabWidth
	if tw <= 0 {
		tw = 4
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.buf.Snapshot()
	for _, l := range []int{p.From.Line, p.To.Line} {
		if l > s.LineCount() {
			return ColumnSelectionResult{}, Errorf(CodeInvalidParams, "line %d out of range (document has %d lines)", l, s.LineCount())
		}
	}
	b := cursor.Block{AnchorLine: p.From.Line - 1, AnchorCol: max(p.From.Col-1, 0), HeadLine: p.To.Line - 1, HeadCol: max(p.To.Col-1, 0)}
	sels, primary := b.Selections(s, tw, nil)
	res := ColumnSelectionResult{Version: s.Version(), Primary: primary, Selections: make([]ColumnSelection, 0, len(sels))}
	for _, sel := range sels {
		res.Selections = append(res.Selections, ColumnSelection{
			Line: s.OffsetToPos(sel.Head).Line + 1, Anchor: sel.Anchor, Head: sel.Head,
			Text: string(s.Slice(sel.Start(), sel.End())),
		})
	}
	return res, nil
}

// selectionEdit runs op on a cursor model with the selection of p.
func (a *App) selectionEdit(p EditRangeParams, op func(m *cursor.Model) bool) (EditRangeResult, error) {
	var res EditRangeResult
	st, err := a.edit(p.Path, p.Version, func(b *buffer.Buffer) error {
		start, err := p.Start.offset(b)
		if err != nil {
			return err
		}
		end := start
		if p.End != nil {
			if end, err = p.End.offset(b); err != nil {
				return err
			}
		}
		m := cursor.New(b)
		defer m.Close()
		m.SetSelections([]cursor.Selection{cursor.Range(start, end)}, 0)
		b.BreakUndo()
		res.Changed = op(m)
		sel := m.Primary()
		res.SelStart, res.SelEnd = sel.Start(), sel.End()
		return nil
	})
	res.DocState = st
	return res, err
}

func (a *App) editJoinLines(_ context.Context, p EditRangeParams) (EditRangeResult, error) {
	return a.selectionEdit(p, (*cursor.Model).JoinLines)
}

func (a *App) editToggleCase(_ context.Context, p EditRangeParams) (EditRangeResult, error) {
	return a.selectionEdit(p, (*cursor.Model).ToggleCase)
}
