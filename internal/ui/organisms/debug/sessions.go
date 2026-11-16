package debug

import (
	"image/color"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Session is one debug connection.
type Session struct {
	ID string
	// Title is usually the script, e.g. "index.php".
	Title string
	// Detail is shown muted, e.g. the IDE key or the request URI.
	Detail string
	State  State
}

type sessionRow struct {
	row  molecules.TreeRow
	stop atoms.IconButton
}

// SessionList shows concurrent debug sessions; clicking selects one, the
// stop button asks to end it.
type SessionList struct {
	Items []Session
	// Selected is the ID of the session the panel shows.
	Selected string

	rows map[string]*sessionRow
	list molecules.ScrollList
}

func (s *SessionList) row(id string) *sessionRow {
	if s.rows == nil {
		s.rows = map[string]*sessionRow{}
	}
	r, ok := s.rows[id]
	if !ok {
		r = &sessionRow{}
		s.rows[id] = r
	}
	return r
}

// Update returns select and stop events.
func (s *SessionList) Update(gtx layout.Context) []Event {
	var out []Event
	for _, it := range s.Items {
		r := s.row(it.ID)
		if r.stop.Clicked(gtx) {
			out = append(out, SessionStopEvent{ID: it.ID})
			continue
		}
		if clicked, _, _ := r.row.Update(gtx); clicked && s.Selected != it.ID {
			s.Selected = it.ID
			out = append(out, SessionSelectEvent{ID: it.ID})
		}
	}
	return out
}

// Layout draws the list; it fills the available space.
func (s *SessionList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	s.Update(gtx)
	if len(s.Items) == 0 {
		return emptyState(gtx, th, "", "No debug sessions")
	}
	return s.list.Layout(gtx, th, len(s.Items), func(gtx layout.Context, i int) layout.Dimensions {
		it := s.Items[i]
		r := s.row(it.ID)
		r.row.Name, r.row.Value, r.row.Detail = it.Title, "", it.State.String()
		if it.Detail != "" {
			r.row.Detail = it.Detail + " · " + it.State.String()
		}
		r.row.Selected = it.ID == s.Selected
		r.row.Glyph, r.row.GlyphColor = atoms.GlyphDebug, stateColor(th, it.State)
		r.stop.Glyph, r.stop.Description = atoms.GlyphStop, "Stop session"
		r.stop.Size, r.stop.IconSize = th.Size.RowH-th.Space.XS, th.Size.IconSmall
		r.stop.Disabled = !it.State.Active()
		r.row.Trailing = func(gtx layout.Context) layout.Dimensions { return r.stop.Layout(gtx, th) }
		return r.row.Layout(gtx, th)
	})
}

func stateColor(th *tokens.Theme, s State) color.NRGBA {
	switch s {
	case StateBreak:
		return th.Color.Warning
	case StateRunning, StateStarting:
		return th.Color.Success
	}
	return th.Color.TextMuted
}
