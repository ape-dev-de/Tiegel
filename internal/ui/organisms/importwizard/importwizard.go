// Package importwizard provides the "Import Settings…" wizard body: the
// areas of a settings import with checkboxes and item counts, the report
// (would import / imported / skipped / unsupported / failed per item) and
// the Import and Close buttons. The host fills it from the import.ide.*
// API and reacts to its events; the organism knows no API types.
package importwizard

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Item states (the values of the import API).
const (
	StatusReady       = "ready"
	StatusImported    = "imported"
	StatusSkipped     = "skipped"
	StatusUnsupported = "unsupported"
	StatusFailed      = "failed"
)

// Area is a checkbox row.
type Area struct {
	ID, Label string
	// Count items would be imported, Total items were found.
	Count, Total int
	Selected     bool
}

// Item is a report row.
type Item struct {
	Area, Name, Status, Detail, Source string
}

// Event is reported by View.Update.
type Event interface{ isImportEvent() }

// ToggleEvent: the user changed the selection of an area.
type ToggleEvent struct {
	ID       string
	Selected bool
}

// ImportEvent: the user confirmed the import of the selected areas.
type ImportEvent struct{ Areas []string }

// CloseEvent: the user closed the wizard.
type CloseEvent struct{}

func (ToggleEvent) isImportEvent() {}
func (ImportEvent) isImportEvent() {}
func (CloseEvent) isImportEvent()  {}

// View is the wizard body.
type View struct {
	// Source and Project describe what is imported.
	Source, Project string
	Areas           []Area
	Items           []Item
	// Notes are remarks of the scan (nothing found, no .idea).
	Notes []string
	// Done: the report is the result of an import (the Import button
	// disappears).
	Done bool
	// Busy shows a progress text and disables Import.
	Busy  bool
	Error string

	checks []*atoms.Checkbox
	list   molecules.ScrollList
	ok     atoms.Button
	close  atoms.Button
}

// selected returns the IDs of the selected areas.
func (v *View) selected() []string {
	var out []string
	for _, a := range v.Areas {
		if a.Selected {
			out = append(out, a.ID)
		}
	}
	return out
}

// Update processes the checkboxes and buttons.
func (v *View) Update(gtx layout.Context) []Event {
	for len(v.checks) < len(v.Areas) {
		v.checks = append(v.checks, &atoms.Checkbox{})
	}
	var out []Event
	for i := range v.Areas {
		a := &v.Areas[i]
		c := v.checks[i]
		c.Disabled = v.Done || v.Busy || a.Total == 0
		if c.Update(gtx) {
			a.Selected = c.Checked()
			out = append(out, ToggleEvent{ID: a.ID, Selected: a.Selected})
		}
	}
	if v.ok.Clicked(gtx) && !v.Done && !v.Busy && len(v.selected()) > 0 {
		out = append(out, ImportEvent{Areas: v.selected()})
	}
	if v.close.Clicked(gtx) {
		out = append(out, CloseEvent{})
	}
	return out
}

// badge returns the status badge of an item.
func badge(status string, done bool) atoms.Badge {
	switch status {
	case StatusReady:
		if done {
			return atoms.Badge{Text: "not imported", Kind: atoms.BadgeNeutral}
		}
		return atoms.Badge{Text: "import", Kind: atoms.BadgeAccent}
	case StatusImported:
		return atoms.Badge{Text: "imported", Kind: atoms.BadgeSuccess}
	case StatusUnsupported:
		return atoms.Badge{Text: "not supported", Kind: atoms.BadgeWarning}
	case StatusFailed:
		return atoms.Badge{Text: "failed", Kind: atoms.BadgeDanger}
	}
	return atoms.Badge{Text: "skipped", Kind: atoms.BadgeNeutral}
}

// Layout draws the wizard body; it fills the given height.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.Update(gtx)
	v.ok.Text, v.ok.Kind = "Import", atoms.Primary
	v.close.Text, v.close.Kind = "Close", atoms.Secondary
	if !v.Done {
		v.close.Text = "Cancel"
	}
	head := []layout.FlexChild{}
	if v.Source != "" {
		head = append(head, layout.Rigid(atoms.Small("Configuration: "+v.Source).SingleLine().Fn(th)))
	}
	if v.Project != "" {
		head = append(head, layout.Rigid(atoms.Small("Project: "+v.Project).SingleLine().Fn(th)))
	}
	for _, n := range v.Notes {
		head = append(head, layout.Rigid(atoms.Small(n).Fn(th)))
	}
	rows := append(head,
		layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
		layout.Rigid(molecules.SectionHeader{Title: "Areas"}.Fn(th)),
		layout.Rigid(v.areas(th)),
		layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
		layout.Rigid(molecules.SectionHeader{Title: v.reportTitle()}.Fn(th)),
		layout.Flexed(1, v.report(th)),
		layout.Rigid(v.footer(th)),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (v *View) reportTitle() string {
	if v.Done {
		return "Report"
	}
	return "Preview"
}

func (v *View) areas(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(v.Areas))
		for i, a := range v.Areas {
			if i >= len(v.checks) {
				break
			}
			c := v.checks[i]
			c.Label = fmt.Sprintf("%s  (%d of %d)", a.Label, a.Count, a.Total)
			if a.Selected {
				c.State = atoms.Checked
			} else {
				c.State = atoms.Unchecked
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return c.Layout(gtx, th)
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

func (v *View) report(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(v.Items) == 0 {
			return atoms.Muted("Nothing found to import.").Layout(gtx, th)
		}
		gtx.Constraints.Min = gtx.Constraints.Max
		return v.list.Layout(gtx, th, len(v.Items), func(gtx layout.Context, i int) layout.Dimensions {
			it := v.Items[i]
			return layout.Inset{Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(unit.Dp(96))
						return badge(it.Status, v.Done).Layout(gtx, th)
					}),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(atoms.Strong(it.Name).SingleLine().Fn(th)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								text := it.Detail
								if it.Source != "" {
									text = joinText(text, it.Source)
								}
								if text == "" {
									return layout.Dimensions{}
								}
								return atoms.Small(text).Fn(th)(gtx)
							}),
						)
					}),
				)
			})
		})
	}
}

func joinText(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

func (v *View) footer(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			status := ""
			switch {
			case v.Error != "":
				status = v.Error
			case v.Busy:
				status = "Working…"
			}
			children := []layout.FlexChild{
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = 0
					if status == "" {
						return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
					}
					col := th.Color.TextMuted
					if v.Error != "" {
						col = th.Color.Danger
					}
					d := atoms.Label{Text: status, Role: atoms.RoleSmall, Color: col, MaxLines: 2}.Layout(gtx, th)
					d.Size.X = gtx.Constraints.Max.X
					return d
				}),
			}
			if !v.Done {
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.ok.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.close.Layout(gtx, th) }))
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}
}
