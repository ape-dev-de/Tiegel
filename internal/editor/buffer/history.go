package buffer

// GroupKind classifies an undo group. Consecutive groups of the same
// mergeable kind are combined into one undo step until a boundary is set
// with BreakUndo.
type GroupKind uint8

const (
	// GroupOther groups are never merged with neighbours.
	GroupOther GroupKind = iota
	// GroupTyping is used for inserting typed text.
	GroupTyping
	// GroupDeleting is used for backspace/delete key presses.
	GroupDeleting
)

// change is one recorded replacement: at off, del was replaced by ins.
type change struct {
	off int
	del []byte
	ins []byte
}

// group is one undo step.
type group struct {
	changes []change
	kind    GroupKind
	auto    bool // created implicitly by a single ungrouped edit
	before  any  // caller state (e.g. cursors) before the step
	after   any  // caller state after the step
	id      uint64
}

type history struct {
	undo   []*group
	redo   []*group
	open   bool // the top undo group may absorb further edits
	depth  int  // BeginGroup nesting
	cur    *group
	curNew bool
	nextID uint64
	saved  uint64
	limit  int
}

func (h *history) newID() uint64 {
	h.nextID++
	return h.nextID
}

func (h *history) top() *group {
	if len(h.undo) == 0 {
		return nil
	}
	return h.undo[len(h.undo)-1]
}

func (h *history) push(g *group) {
	h.undo = append(h.undo, g)
	if h.limit > 0 && len(h.undo) > h.limit {
		drop := len(h.undo) - h.limit
		clear(h.undo[:drop])
		h.undo = h.undo[drop:]
	}
}

// record adds a change made by Replace.
func (h *history) record(c change) {
	h.redo = h.redo[:0]
	if h.depth > 0 {
		h.cur.changes = append(h.cur.changes, c)
		return
	}
	kind := GroupOther
	switch {
	case len(c.del) == 0:
		kind = GroupTyping
	case len(c.ins) == 0:
		kind = GroupDeleting
	}
	if t := h.top(); h.open && t != nil && t.auto && t.kind == kind && kind != GroupOther && contiguous(t, c) {
		t.changes = append(t.changes, c)
		t.id = h.newID()
		return
	}
	h.push(&group{changes: []change{c}, kind: kind, auto: true, id: h.newID()})
	h.open = kind != GroupOther
}

// contiguous reports whether c continues the typing/deleting run of g.
func contiguous(g *group, c change) bool {
	l := g.changes[len(g.changes)-1]
	if g.kind == GroupTyping {
		return c.off == l.off+len(l.ins)
	}
	return c.off+len(c.del) == l.off || c.off == l.off
}

// BeginGroup starts an explicit undo group: all changes until the matching
// EndGroup are undone together. Groups nest; only the outermost counts. If
// the previous step has the same mergeable kind and no BreakUndo happened
// since, the changes are appended to it (continuous typing). before is the
// caller state (e.g. cursor positions) restored by Undo.
func (b *Buffer) BeginGroup(kind GroupKind, before any) {
	h := &b.hist
	h.depth++
	if h.depth > 1 {
		return
	}
	if t := h.top(); h.open && t != nil && kind != GroupOther && t.kind == kind {
		h.cur, h.curNew = t, false
		return
	}
	h.cur, h.curNew = &group{kind: kind, before: before}, true
}

// EndGroup closes the group opened by BeginGroup. after is the caller
// state restored by Redo. Calls without a matching BeginGroup are ignored.
func (b *Buffer) EndGroup(after any) {
	h := &b.hist
	if h.depth == 0 {
		return
	}
	h.depth--
	if h.depth > 0 {
		return
	}
	g := h.cur
	h.cur = nil
	if len(g.changes) == 0 {
		return
	}
	g.after = after
	g.auto = false
	g.id = h.newID()
	if h.curNew {
		h.push(g)
	}
	h.open = g.kind != GroupOther
}

// BreakUndo sets an undo boundary: the next change starts a new undo step.
// Call it e.g. when the cursor is moved without editing.
func (b *Buffer) BreakUndo() { b.hist.open = false }

// SetUndoLimit limits the number of undo steps kept; n <= 0 means
// unlimited.
func (b *Buffer) SetUndoLimit(n int) { b.hist.limit = n }

// CanUndo reports whether there is a step to undo.
func (b *Buffer) CanUndo() bool { return len(b.hist.undo) > 0 && b.hist.depth == 0 }

// CanRedo reports whether there is a step to redo.
func (b *Buffer) CanRedo() bool { return len(b.hist.redo) > 0 && b.hist.depth == 0 }

// Undo reverts the most recent undo step. It returns the edits applied to
// the document (in order) and the caller state stored as "before" for the
// step. ok is false if there is nothing to undo or a group is open.
func (b *Buffer) Undo() (edits []Edit, state any, ok bool) {
	h := &b.hist
	if !b.CanUndo() {
		return nil, nil, false
	}
	g := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	for i := len(g.changes) - 1; i >= 0; i-- {
		c := g.changes[i]
		edits = append(edits, b.apply(c.off, len(c.ins), c.del))
	}
	h.redo = append(h.redo, g)
	h.open = false
	return edits, g.before, true
}

// Redo re-applies the most recently undone step. It returns the edits and
// the caller state stored as "after" for the step.
func (b *Buffer) Redo() (edits []Edit, state any, ok bool) {
	h := &b.hist
	if !b.CanRedo() {
		return nil, nil, false
	}
	g := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	for _, c := range g.changes {
		edits = append(edits, b.apply(c.off, len(c.del), c.ins))
	}
	h.undo = append(h.undo, g)
	h.open = false
	return edits, g.after, true
}

// stateID identifies the current position in the history.
func (h *history) stateID() uint64 {
	if t := h.top(); t != nil {
		return t.id
	}
	return 0
}

// MarkSaved records the current state as the saved one.
func (b *Buffer) MarkSaved() { b.hist.saved = b.hist.stateID() }

// Modified reports whether the document differs from the state recorded
// by MarkSaved (or from the initial content). Undoing back to the saved
// state makes it unmodified again.
func (b *Buffer) Modified() bool { return b.hist.stateID() != b.hist.saved }
