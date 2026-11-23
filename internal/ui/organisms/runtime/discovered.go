package runtime

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Discovered is an interpreter found around the project that is not
// configured yet (a compose service in a parent folder, an image of a
// compose file).
type Discovered struct {
	ID    string
	Label string
	Kind  Kind
	// Where tells where it was found, e.g. "../../docker-compose.yaml".
	Where string
	// Mapping is the main path mapping, e.g. "project → /var/www/project".
	Mapping string
	Score   int
	Reasons []string
}

// UseDiscoveredEvent: "Use" was clicked for a discovered interpreter.
type UseDiscoveredEvent struct{ ID string }

func (UseDiscoveredEvent) isRuntimeEvent() {}

// DiscoveredList shows the discovered interpreters, best first, each with
// a "Use" button: nothing is configured before the click (D194, D524).
type DiscoveredList struct {
	Items []Discovered
	// Loading shows a progress text while the discovery runs.
	Loading bool

	use []atoms.Button
}

func (l *DiscoveredList) sync() {
	for len(l.use) < len(l.Items) {
		l.use = append(l.use, atoms.Button{Text: "Use", Kind: atoms.Secondary})
	}
}

// Update returns a UseDiscoveredEvent for a clicked "Use".
func (l *DiscoveredList) Update(gtx layout.Context) []Event {
	l.sync()
	var out []Event
	for i := range l.Items {
		if l.use[i].Clicked(gtx) {
			out = append(out, UseDiscoveredEvent{ID: l.Items[i].ID})
		}
	}
	return out
}

// Layout draws one row per item; nothing when the list is empty.
func (l *DiscoveredList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	l.sync()
	if l.Loading && len(l.Items) == 0 {
		return atoms.Small("Looking for compose files around the project…").Layout(gtx, th)
	}
	rows := make([]layout.FlexChild, 0, len(l.Items))
	for i := range l.Items {
		i := i
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return l.row(gtx, th, i) })
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (l *DiscoveredList) row(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	it := l.Items[i]
	gtx.Constraints.Min.X = 0
	details := []string{}
	if it.Where != "" {
		details = append(details, it.Where)
	}
	if it.Mapping != "" {
		details = append(details, it.Mapping)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(atoms.Icon{Glyph: kindGlyph(it.Kind), Size: th.Size.IconSmall, Color: th.Color.TextMuted}.Fn(th)),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(atoms.Text(it.Label).Fn(th)),
						layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if it.Score <= 0 {
								return layout.Dimensions{}
							}
							kind := atoms.BadgeNeutral
							if i == 0 {
								kind = atoms.BadgeAccent
							}
							return atoms.Badge{Text: fmt.Sprintf("score %d", it.Score), Kind: kind}.Layout(gtx, th)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(details) == 0 {
						return layout.Dimensions{}
					}
					return atoms.Small(strings.Join(details, " · ")).SingleLine().Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(it.Reasons) == 0 {
						return layout.Dimensions{}
					}
					return atoms.Small(strings.Join(it.Reasons, "; ")).Layout(gtx, th)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return l.use[i].Layout(gtx, th) }),
	)
}
