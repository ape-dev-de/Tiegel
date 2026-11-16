package runtime

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Mapping maps a host path to a path inside the container.
type Mapping struct {
	Host   string
	Remote string
}

type mappingRow struct {
	host, remote atoms.TextField
	remove       atoms.IconButton
	focused      bool // a field of the row had focus in the last frame
	inited       bool
}

// PathMappingEditor edits the host ↔ container path mappings in a table.
// A row is committed (MappingChangeEvent) when Enter is pressed or focus
// leaves the row; while a row is focused the host's data does not
// overwrite what the user types.
type PathMappingEditor struct {
	Items []Mapping

	table molecules.Table
	rows  []*mappingRow
	add   atoms.Button
}

// RemoteValid reports whether a container path is acceptable (absolute,
// forward slashes).
func RemoteValid(p string) bool {
	return p == "" || strings.HasPrefix(p, "/") && !strings.Contains(p, "\\")
}

func (e *PathMappingEditor) ensureRows() {
	for len(e.rows) < len(e.Items) {
		e.rows = append(e.rows, &mappingRow{})
	}
	e.rows = e.rows[:len(e.Items)]
	for i := range e.rows {
		r := e.rows[i]
		if !r.inited {
			r.inited = true
			for _, f := range []*atoms.TextField{&r.host, &r.remote} {
				f.Editor.SingleLine, f.Editor.Submit = true, true
				f.Mono = true
			}
			r.host.Hint, r.remote.Hint = "/path/on/host", "/var/www/html"
		}
	}
}

// syncTexts copies the data into the fields of rows the user is not
// editing.
func (e *PathMappingEditor) syncTexts(gtx layout.Context) {
	for i, r := range e.rows {
		if !r.host.Focused(gtx) && !r.remote.Focused(gtx) {
			it := e.Items[i]
			if r.host.Text() != it.Host {
				r.host.SetText(it.Host)
			}
			if r.remote.Text() != it.Remote {
				r.remote.SetText(it.Remote)
			}
		}
	}
}

// Update returns mapping events.
func (e *PathMappingEditor) Update(gtx layout.Context) []Event {
	e.ensureRows()
	defer e.syncTexts(gtx)
	var out []Event
	if e.add.Clicked(gtx) {
		out = append(out, MappingAddEvent{})
	}
	for i := range e.rows {
		r := e.rows[i]
		if r.remove.Clicked(gtx) {
			out = append(out, MappingRemoveEvent{Index: i})
			continue
		}
		submit := false
		for _, f := range []*atoms.TextField{&r.host, &r.remote} {
			for {
				ev, ok := f.Editor.Update(gtx)
				if !ok {
					break
				}
				if _, ok := ev.(widget.SubmitEvent); ok {
					submit = true
				}
			}
		}
		focused := r.host.Focused(gtx) || r.remote.Focused(gtx)
		blurred := r.focused && !focused
		r.focused = focused
		host, remote := strings.TrimSpace(r.host.Text()), strings.TrimSpace(r.remote.Text())
		it := e.Items[i]
		if (submit || blurred) && (host != it.Host || remote != it.Remote) {
			out = append(out, MappingChangeEvent{Index: i, Host: host, Remote: remote})
		}
	}
	return out
}

// Layout draws the table and the add button; it fills the available
// space.
func (e *PathMappingEditor) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	e.Update(gtx)
	e.table.Columns = []molecules.TableColumn{
		{Title: "Host path"}, {Title: "Container path"}, {Width: th.Size.ControlH + th.Space.S},
	}
	e.add.Kind, e.add.Text, e.add.Glyph = atoms.Ghost, "Add mapping", atoms.GlyphAdd
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return e.table.Layout(gtx, th, len(e.rows), func(gtx layout.Context, row, col int) layout.Dimensions {
				r := e.rows[row]
				switch col {
				case 0:
					return r.host.Layout(gtx, th)
				case 1:
					r.remote.Invalid = !RemoteValid(strings.TrimSpace(r.remote.Text()))
					return r.remote.Layout(gtx, th)
				}
				r.remove.Glyph, r.remove.Description = atoms.GlyphRemove, "Remove mapping"
				r.remove.Size = unit.Dp(th.Size.ControlH)
				return r.remove.Layout(gtx, th)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return e.add.Layout(gtx, th) })
		}),
	)
}
