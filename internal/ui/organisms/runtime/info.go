package runtime

import (
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ProbeInfo is the view model of an interpreter probe (interp.probe).
type ProbeInfo struct {
	Version string
	SAPI    string
	OS      string
	// Xdebug is the Xdebug version; empty when not loaded.
	Xdebug string
	// IniFile is the loaded php.ini, IniScanned the additional files.
	IniFile    string
	IniScanned []string
	Extensions []string
	// ProbedAt is a display time, e.g. "2026-11-17 10:42".
	ProbedAt string
	// Cached is true when the info was not read just now.
	Cached bool
}

// InterpreterInfoPanel shows what the probe learned about the selected
// interpreter (version, Xdebug, ini files, extensions) with a "Re-read"
// button. Without Info it shows Error or a hint to read it.
type InterpreterInfoPanel struct {
	// ID is the interpreter the info belongs to (sent with the event).
	ID   string
	Info *ProbeInfo
	// Error explains a failed probe (e.g. the container is not running).
	Error string
	// Busy disables the button while a probe runs.
	Busy bool

	refresh atoms.Button
}

// Update returns a ProbeRefreshEvent when "Re-read" was clicked.
func (p *InterpreterInfoPanel) Update(gtx layout.Context) []Event {
	if p.refresh.Clicked(gtx) && !p.Busy && p.ID != "" {
		return []Event{ProbeRefreshEvent{ID: p.ID}}
	}
	return nil
}

// Layout draws the panel.
func (p *InterpreterInfoPanel) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.Update(gtx)
	gtx.Constraints.Min.Y = 0
	p.refresh.Kind, p.refresh.Glyph, p.refresh.Disabled = atoms.Secondary, atoms.GlyphRefresh, p.Busy || p.ID == ""
	p.refresh.Text = "Re-read"
	if p.Info == nil {
		p.refresh.Text = "Read info"
	}
	var rows []layout.FlexChild
	row := func(w layout.Widget) {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.XS}.Layout(gtx, w)
		}))
	}
	field := func(name, value string, code bool) {
		row(func(gtx layout.Context) layout.Dimensions {
			v := atoms.Small(value)
			if code {
				v = atoms.Label{Text: value, Role: atoms.RoleCode, Color: th.Color.TextMuted}
			}
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(96)
					gtx.Constraints.Max.X = gtx.Constraints.Min.X
					return atoms.Small(name).WithColor(th.Color.TextMuted).Fn(th)(gtx)
				}),
				layout.Flexed(1, v.Fn(th)),
			)
		})
	}
	if i := p.Info; i != nil {
		row(func(gtx layout.Context) layout.Dimensions {
			badges := []layout.FlexChild{layout.Rigid(atoms.Badge{Text: "PHP " + i.Version, Kind: atoms.BadgeInfo}.Fn(th))}
			xd := atoms.Badge{Text: "Xdebug " + i.Xdebug, Kind: atoms.BadgeSuccess}
			if i.Xdebug == "" {
				xd = atoms.Badge{Text: "no Xdebug", Kind: atoms.BadgeWarning}
			}
			badges = append(badges, layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout), layout.Rigid(xd.Fn(th)))
			if meta := strings.TrimSpace(i.SAPI + " " + i.OS); meta != "" {
				badges = append(badges, layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Rigid(atoms.Small(meta).WithColor(th.Color.TextMuted).Fn(th)))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, badges...)
		})
		ini := i.IniFile
		if ini == "" {
			ini = "(none)"
		}
		field("php.ini", ini, true)
		if len(i.IniScanned) > 0 {
			field("Additional", strings.Join(i.IniScanned, "\n"), true)
		}
		field("Extensions", strings.Join(i.Extensions, ", "), false)
		if i.ProbedAt != "" {
			when := "Read " + i.ProbedAt
			if i.Cached {
				when += " (cached)"
			}
			field("", when, false)
		}
	} else {
		msg := "Not read yet. Reading runs PHP in this interpreter once."
		if p.Error != "" {
			msg = p.Error
		}
		row(atoms.Small(msg).Fn(th))
	}
	if p.Info != nil && p.Error != "" {
		row(atoms.Small(p.Error).WithColor(th.Color.Warning).Fn(th))
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return p.refresh.Layout(gtx, th)
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
