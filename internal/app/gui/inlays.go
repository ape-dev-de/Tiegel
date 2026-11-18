package gui

import (
	"path"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// InlayDelay debounces inlay hint requests after edits (AP17.3).
var InlayDelay = 400 * time.Millisecond

// inlayState tracks the inlay hints of an editor session.
type inlayState struct {
	on      bool      // hints are enabled for the session
	version uint64    // buffer version the pending request is for
	since   time.Time // when version was first seen
	sent    bool      // the request for version went out
	busy    bool      // a request is running
}

// inlayHints keeps the language server's inlay hints of a PHP editor up
// to date: after edits it waits InlayDelay, then asks again; meanwhile
// the old hints move with the text. The caller holds the document lock.
func (v *View) inlayHints(gtx layout.Context, s *editorSession, version uint64) {
	c := v.ctl
	st := &s.inlay
	on := c.Prefs.InlayHints && c.Status.LSPRunning && !s.ed.Wrapped() && strings.EqualFold(path.Ext(s.file), ".php")
	if !on {
		if st.on {
			s.ed.SetInlayHints(nil)
		}
		*st = inlayState{busy: st.busy}
		return
	}
	st.on = true
	if st.version != version {
		st.version, st.since, st.sent = version, gtx.Now, false
	}
	if st.sent || st.busy {
		return
	}
	if due := st.since.Add(InlayDelay); gtx.Now.Before(due) {
		gtx.Execute(op.InvalidateCmd{At: due})
		return
	}
	st.sent, st.busy = true, true
	c.LoadInlayHints(s.file, func(res controller.InlayHints) {
		st.busy = false
		if !st.on {
			return
		}
		s.doc.Lock()
		defer s.doc.Unlock()
		if !res.Supported || res.Version != s.doc.Buffer().Version() {
			return // edited meanwhile: the new version asks again
		}
		out := make([]editorview.InlayHint, 0, len(res.Hints))
		for _, h := range res.Hints {
			out = append(out, editorview.InlayHint{Offset: h.Offset, Label: h.Label, PadLeft: h.PadLeft, PadRight: h.PadRight})
		}
		s.ed.SetInlayHints(out)
	})
}
