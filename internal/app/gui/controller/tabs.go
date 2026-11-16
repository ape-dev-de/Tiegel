package controller

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Tab is an open editor tab.
type Tab struct {
	// File is the absolute path, Path the project-relative display path.
	File string
	Path string
	// Language is the detected language ("php", "json", …).
	Language string
	EOL      string
	// Modified mirrors the buffer's modified flag.
	Modified bool
	// Doc is the shared document (nil until opened).
	Doc DocHandle
	// Generation identifies the opened document instance (doc.open); the
	// tab's doc.close carries it so it cannot close a reopened document.
	Generation uint64
	// Virtual is the kind of a virtual tab (no file, e.g. "diff"); File
	// is then a unique key ("virtual:<kind>:<id>") and Doc is nil. The
	// view draws it with the tab type registered for the kind.
	Virtual string
	// Label overrides the tab title (virtual tabs).
	Label string
	// Data is the payload of a virtual tab (owned by its tab type).
	Data any
}

// pendingCloses holds, per controller and file, a channel that is closed
// once the doc.save/doc.close of a closed tab have finished. Reopening the
// file waits for it, so the late doc.close cannot hit the new tab's
// document.
var pendingCloses sync.Map // closeKey -> chan struct{}

type closeKey struct {
	c    *Controller
	file string
}

// waitPendingClose blocks until a pending close of file has finished.
func (c *Controller) waitPendingClose(ctx context.Context, file string) {
	if ch, ok := pendingCloses.Load(closeKey{c, file}); ok {
		select {
		case <-ch.(chan struct{}):
		case <-ctx.Done():
		}
	}
}

// Title is the tab title (file name or the label of a virtual tab).
func (t *Tab) Title() string {
	if t.Label != "" {
		return t.Label
	}
	return filepath.Base(t.File)
}

// VirtualKey returns the File key of a virtual tab.
func VirtualKey(kind, id string) string { return "virtual:" + kind + ":" + id }

// OpenVirtual opens (or activates) a virtual tab of kind with id in the
// active group, e.g. a diff. title is shown in the tab; data is handed to
// the tab type's layout.
func (c *Controller) OpenVirtual(kind, id, title string, data any) *Tab {
	key := VirtualKey(kind, id)
	if i := c.tabIndex(key); i >= 0 {
		c.Tabs[i].Data, c.Tabs[i].Label = data, title
		c.Activate(i)
		return c.Tabs[i]
	}
	t := &Tab{File: key, Path: title, Virtual: kind, Label: title, Data: data}
	c.insertTab(t)
	return t
}

// insertTab inserts t after the active tab and activates it.
func (c *Controller) insertTab(t *Tab) {
	pos := c.Active + 1
	if pos <= 0 || pos > len(c.Tabs) {
		pos = len(c.Tabs)
	}
	c.Tabs = append(c.Tabs, nil)
	copy(c.Tabs[pos+1:], c.Tabs[pos:])
	c.Tabs[pos] = t
	c.Activate(pos)
}

// Tab returns the open tab of an absolute file, or nil.
func (c *Controller) Tab(file string) *Tab {
	for _, t := range c.Tabs {
		if t.File == file {
			return t
		}
	}
	return nil
}

// ActiveTab returns the active tab or nil.
func (c *Controller) ActiveTab() *Tab {
	if c.Active < 0 || c.Active >= len(c.Tabs) {
		return nil
	}
	return c.Tabs[c.Active]
}

func (c *Controller) tabIndex(file string) int {
	for i, t := range c.Tabs {
		if t.File == file {
			return i
		}
	}
	return -1
}

// OpenFile opens file (absolute or project relative) in a tab and
// activates it; at, if not nil, is revealed once the document is loaded.
// The current location is pushed onto the navigation history.
func (c *Controller) OpenFile(file string, at *Location) {
	abs := c.Abs(file)
	if i := c.tabIndex(abs); i >= 0 {
		c.Activate(i)
		if at != nil {
			c.Reveal = at
		}
		return
	}
	// Open in another group: share its document (no second doc.open).
	if o := c.sharedTab(abs, nil); o != nil && o.Doc != nil {
		c.insertTab(o.clone())
		if at != nil {
			c.Reveal = at
		}
		return
	}
	c.Go(func(ctx context.Context) func() {
		c.waitPendingClose(ctx, abs)
		var st struct {
			DocState
			Generation uint64 `json:"generation"`
		}
		err := c.call(ctx, "doc.open", map[string]any{"path": abs}, &st)
		var doc DocHandle
		if err == nil {
			doc, err = c.be.Document(abs)
		}
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			if i := c.tabIndex(abs); i >= 0 { // opened twice quickly
				c.Activate(i)
				return
			}
			t := &Tab{File: abs, Path: c.Rel(abs), Language: st.Language, EOL: st.EOL, Modified: st.Modified, Doc: doc, Generation: st.Generation}
			// Insert after the active tab like most editors do.
			c.insertTab(t)
			if at != nil {
				c.Reveal = at
			}
		}
	})
}

// Activate makes tab i active. Switching tabs saves modified documents
// (autosave on tab change).
func (c *Controller) Activate(i int) {
	if i < 0 || i >= len(c.Tabs) {
		return
	}
	if c.Active != i && c.Active >= 0 {
		c.Autosave("focusLost")
	}
	c.Active = i
	c.CloseCompletion()
	c.Signature = SignatureHelp{}
	c.Hover = HoverInfo{}
	if c.Tabs[i].Virtual == "" {
		c.touchRecent(c.Tabs[i].File)
	}
}

// touchRecent moves file to the front of the recent files.
func (c *Controller) touchRecent(file string) {
	c.RecentFiles = addRecent(c.RecentFiles, file)
	rel := make([]string, 0, len(c.RecentFiles))
	for _, f := range c.RecentFiles {
		rel = append(rel, c.Rel(f))
	}
	if c.Project.Open {
		c.SetSetting(KeyRecentFiles, "project", rel)
	}
}

// CloseTab saves and closes tab i (doc.save, then doc.close with the tab's
// generation). Unsaved changes are never discarded: if doc.close reports
// them (edited after the modified flag was mirrored), the document is
// saved and the close retried once; a failing save leaves it open.
func (c *Controller) CloseTab(i int) {
	if i < 0 || i >= len(c.Tabs) {
		return
	}
	t := c.Tabs[i]
	c.Tabs = append(c.Tabs[:i], c.Tabs[i+1:]...)
	switch {
	case len(c.Tabs) == 0:
		c.Active = -1
	case c.Active > i || c.Active == len(c.Tabs):
		c.Active--
	}
	if c.Active >= 0 && c.Tabs[c.Active].Virtual == "" {
		c.touchRecent(c.Tabs[c.Active].File)
	}
	c.CloseCompletion()
	// An emptied split closes; its neighbour becomes active.
	if len(c.Tabs) == 0 && len(c.groups) > 1 {
		c.Unsplit()
	}
	// Virtual tabs have no document; a file still open in another group
	// keeps the shared document open.
	if t.Virtual != "" || t.Doc == nil {
		return
	}
	if o := c.sharedTab(t.File, t); o != nil {
		if t.Modified {
			c.Save(t.File)
		}
		return
	}
	key := closeKey{c, t.File}
	done := make(chan struct{})
	pendingCloses.Store(key, done)
	c.Go(func(ctx context.Context) func() {
		defer func() {
			pendingCloses.CompareAndDelete(key, done)
			close(done)
		}()
		params := map[string]any{"path": t.File}
		if t.Generation != 0 {
			params["generation"] = t.Generation
		}
		var err error
		if t.Modified {
			err = c.call(ctx, "doc.save", pathParams{Path: t.File}, nil)
		}
		if err == nil {
			err = c.call(ctx, "doc.close", params, nil)
			if api.ErrorCode(err) == api.CodeConflict {
				if err = c.call(ctx, "doc.save", pathParams{Path: t.File}, nil); err == nil {
					err = c.call(ctx, "doc.close", params, nil)
				}
			}
		}
		return func() { c.notice(err) }
	})
}

// CycleTab activates the next (delta 1) or previous (-1) tab.
func (c *Controller) CycleTab(delta int) {
	n := len(c.Tabs)
	if n == 0 {
		return
	}
	c.Activate(((c.Active+delta)%n + n) % n)
}

// Autosave asks the kernel to save all modified documents (Local History
// and save hooks run in the backend). trigger is focusLost, idle, ….
//
// The controller never takes document locks (the view may hold one while
// calling it); the kernel finds the modified documents itself.
func (c *Controller) Autosave(trigger string) {
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "autosave.trigger", map[string]string{"trigger": trigger}, nil)
		return func() { c.notice(err) }
	})
}

// AutosaveSync saves synchronously (window close). Documents that cannot
// be saved are preserved by the backend as recovery points in Local
// History (history.recovery); the error says so.
func (c *Controller) AutosaveSync(ctx context.Context) error {
	return c.call(ctx, "autosave.trigger", map[string]string{"trigger": "close"}, nil)
}

func (c *Controller) anyModified() bool {
	for _, t := range c.Tabs {
		if t.Modified {
			return true
		}
	}
	return false
}

// Save saves the document of file explicitly (Cmd+S).
func (c *Controller) Save(file string) {
	c.Go(func(ctx context.Context) func() {
		var st DocState
		err := c.call(ctx, "doc.save", pathParams{Path: file}, &st)
		return func() {
			if err != nil && c.Work.saveFailed(file, err) {
				return
			}
			c.notice(err)
			if t := c.Tab(file); t != nil && err == nil {
				t.Modified = st.Modified
			}
		}
	})
}

// ---- navigation -------------------------------------------------------------

// Navigation is a back/forward history of locations.
type Navigation struct {
	back, fwd []Location
}

// maxNav bounds the history.
const maxNav = 100

// Push records loc as the location left by a jump; forward history is
// dropped.
func (n *Navigation) Push(loc Location) {
	if k := len(n.back); k > 0 && n.back[k-1].File == loc.File && n.back[k-1].Range.Start.Offset == loc.Range.Start.Offset {
		return
	}
	n.back = append(n.back, loc)
	if len(n.back) > maxNav {
		n.back = n.back[1:]
	}
	n.fwd = nil
}

// CanBack and CanForward report whether a step is possible.
func (n *Navigation) CanBack() bool { return len(n.back) > 0 }

// CanForward reports whether a forward step is possible.
func (n *Navigation) CanForward() bool { return len(n.fwd) > 0 }

// Back returns the previous location; cur is pushed to the forward stack.
func (n *Navigation) Back(cur Location) (Location, bool) {
	if len(n.back) == 0 {
		return Location{}, false
	}
	loc := n.back[len(n.back)-1]
	n.back = n.back[:len(n.back)-1]
	n.fwd = append(n.fwd, cur)
	return loc, true
}

// Forward returns the next location; cur is pushed to the back stack.
func (n *Navigation) Forward(cur Location) (Location, bool) {
	if len(n.fwd) == 0 {
		return Location{}, false
	}
	loc := n.fwd[len(n.fwd)-1]
	n.fwd = n.fwd[:len(n.fwd)-1]
	n.back = append(n.back, cur)
	return loc, true
}

// At returns a location for file and byte offset.
func At(file string, off int) Location {
	return Location{File: file, Range: TextRange{Start: TextPos{Offset: off}, End: TextPos{Offset: off}}}
}

// GoTo jumps to loc, recording cur in the history.
func (c *Controller) GoTo(cur, loc Location) {
	if cur.File != "" {
		c.Nav.Push(cur)
	}
	c.OpenFile(loc.File, &loc)
}

// NavigateBack goes back in the history from cur.
func (c *Controller) NavigateBack(cur Location) {
	if loc, ok := c.Nav.Back(cur); ok {
		c.OpenFile(loc.File, &loc)
	}
}

// NavigateForward goes forward in the history from cur.
func (c *Controller) NavigateForward(cur Location) {
	if loc, ok := c.Nav.Forward(cur); ok {
		c.OpenFile(loc.File, &loc)
	}
}
