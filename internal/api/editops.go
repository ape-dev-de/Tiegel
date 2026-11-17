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
