// Package knowledgeview is the Knowledge tool window (D560): the state of
// the project knowledge base (units, stale units, last run, agent and
// embedding model, store size), Update / Rebuild buttons that first show
// the estimate for confirmation, the progress of a running build with
// cancel, a search box with results and the list of units. The host
// fills the exported fields every frame and handles the Events.
package knowledgeview

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Notice is a banner above the content (missing model, unreachable
// server, failed embedding …) with an optional action button.
type Notice struct {
	Title, Detail string
	// Action labels the button ("Pull model"); ActionID is reported in
	// NoticeEvent.
	Action, ActionID string
	Error            bool
}

// Unit is a row of the unit list.
type Unit struct {
	ID, Title, Dir string
	Files          int
	// Confidence is shown when HasDoc.
	Confidence float64
	HasDoc     bool
	// State is "" (current), new, changed, failed or missing.
	State string
	Error string
}

// Step is a unit of the running build.
type Step struct {
	Title string
	// Step is describe, validate or write; State queued, running, done
	// or failed.
	Step, State, Error string
}

// Hit is a search result.
type Hit struct {
	Heading, Path, Text string
	Score               float64
}

// Event is reported by View.Update.
type Event interface{ isKnowledgeEvent() }

// BuildEvent: Update (All false) or Rebuild all was clicked; the host
// fetches the estimate and sets Confirm.
type BuildEvent struct{ All bool }

// ConfirmEvent: the user confirmed the estimate.
type ConfirmEvent struct{}

// DismissEvent: the user dismissed the estimate.
type DismissEvent struct{}

// CancelEvent: cancel the running build.
type CancelEvent struct{}

// SearchEvent: a query was submitted (Enter) or cleared.
type SearchEvent struct{ Query string }

// OpenUnitEvent: a unit's document should open (ID "" = index.md).
type OpenUnitEvent struct{ ID string }

// OpenHitEvent: a search result was clicked (index into Hits).
type OpenHitEvent struct{ Index int }

// NoticeEvent: the notice's action was clicked.
type NoticeEvent struct{ ActionID string }

// SettingsEvent: the settings button was clicked.
type SettingsEvent struct{}

func (BuildEvent) isKnowledgeEvent()    {}
func (ConfirmEvent) isKnowledgeEvent()  {}
func (DismissEvent) isKnowledgeEvent()  {}
func (CancelEvent) isKnowledgeEvent()   {}
func (SearchEvent) isKnowledgeEvent()   {}
func (OpenUnitEvent) isKnowledgeEvent() {}
func (OpenHitEvent) isKnowledgeEvent()  {}
func (NoticeEvent) isKnowledgeEvent()   {}
func (SettingsEvent) isKnowledgeEvent() {}

// View is the organism. Keep it in the window state.
type View struct {
	// Summary is the status line ("12 units · 2 to update · last run …"),
	// Details the agent and embedding lines.
	Summary string
	Details []string
	// Exists enables the index button and the search.
	Exists bool
	// Stale counts units to build (the Update button's label).
	Stale  int
	Notice *Notice
	// Confirm is the estimate waiting for confirmation ("" = none).
	Confirm string
	// Running, Phase and Steps describe a running build; Done/Total
	// count finished units.
	Running     bool
	Phase       string
	Done, Total int
	Steps       []Step
	// Hits are the search results; SearchNote explains a fallback;
	// Searching shows a spinner.
	Hits       []Hit
	SearchNote string
	Searching  bool
	Units      []Unit
	// Disabled shows only a hint (plugin disabled or no project).
	Disabled string

	search                   *molecules.SearchField
	update, rebuild, confirm atoms.Button
	dismiss, cancel, notice  atoms.Button
	settings, index          atoms.IconButton
	list                     molecules.ScrollList
	unitRows, hitRows        []molecules.ListRow
	lastQuery                string
}

func (v *View) init() {
	if v.search != nil {
		return
	}
	v.search = molecules.NewSearchField("Search the knowledge (Enter)")
	v.update = atoms.Button{Kind: atoms.Primary, Text: "Update", Glyph: atoms.GlyphRefresh}
	v.rebuild = atoms.Button{Kind: atoms.Ghost, Text: "Rebuild all"}
	v.confirm = atoms.Button{Kind: atoms.Primary, Text: "Build"}
	v.dismiss = atoms.Button{Kind: atoms.Ghost, Text: "Not now"}
	v.cancel = atoms.Button{Kind: atoms.Secondary, Text: "Cancel", Glyph: atoms.GlyphStop}
	v.notice = atoms.Button{Kind: atoms.Secondary}
	v.index = atoms.IconButton{Glyph: atoms.GlyphFileText, Description: "Open the table of contents (index.md)"}
	v.settings = atoms.IconButton{Glyph: atoms.GlyphSettings, Description: "Knowledge settings"}
}

// Query returns the text of the search box.
func (v *View) Query() string {
	v.init()
	return v.search.Text()
}

// Update processes input and returns the events.
func (v *View) Update(gtx layout.Context) []Event {
	v.init()
	var out []Event
	if v.update.Clicked(gtx) {
		out = append(out, BuildEvent{})
	}
	if v.rebuild.Clicked(gtx) {
		out = append(out, BuildEvent{All: true})
	}
	if v.confirm.Clicked(gtx) {
		out = append(out, ConfirmEvent{})
	}
	if v.dismiss.Clicked(gtx) {
		out = append(out, DismissEvent{})
	}
	if v.cancel.Clicked(gtx) {
		out = append(out, CancelEvent{})
	}
	if v.notice.Clicked(gtx) && v.Notice != nil {
		out = append(out, NoticeEvent{ActionID: v.Notice.ActionID})
	}
	if v.index.Clicked(gtx) {
		out = append(out, OpenUnitEvent{})
	}
	if v.settings.Clicked(gtx) {
		out = append(out, SettingsEvent{})
	}
	changed, submitted := v.search.Update(gtx)
	q := strings.TrimSpace(v.search.Text())
	if submitted || changed && q == "" && v.lastQuery != "" {
		v.lastQuery = q
		out = append(out, SearchEvent{Query: q})
	}
	v.ensureRows()
	for i := range v.Hits {
		if c, _ := v.hitRows[i].Update(gtx); c {
			out = append(out, OpenHitEvent{Index: i})
		}
	}
	for i, u := range v.Units {
		if c, _ := v.unitRows[i].Update(gtx); c && u.HasDoc {
			out = append(out, OpenUnitEvent{ID: u.ID})
		}
	}
	return out
}

// ensureRows grows the row states to the items.
func (v *View) ensureRows() {
	for len(v.hitRows) < len(v.Hits) {
		v.hitRows = append(v.hitRows, molecules.ListRow{})
	}
	for len(v.unitRows) < len(v.Units) {
		v.unitRows = append(v.unitRows, molecules.ListRow{})
	}
}

// item kinds of the scrolling part.
const (
	itemNotice = iota
	itemConfirm
	itemProgress
	itemSearchNote
	itemHit
	itemUnitsHeader
	itemUnit
	itemEmpty
)

type item struct{ kind, i int }

// Layout draws the window filling the available space.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.init()
	v.ensureRows()
	if v.Disabled != "" {
		return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted(v.Disabled).Fn(th))
	}
	v.update.Disabled = v.Running || v.Confirm != ""
	v.rebuild.Disabled = v.update.Disabled
	v.index.Disabled = !v.Exists
	if v.Stale > 0 {
		v.update.Text = fmt.Sprintf("Update %d", v.Stale)
	} else {
		v.update.Text = "Update"
	}
	var items []item
	if v.Notice != nil {
		items = append(items, item{itemNotice, 0})
	}
	if v.Confirm != "" {
		items = append(items, item{itemConfirm, 0})
	}
	if v.Running {
		items = append(items, item{itemProgress, 0})
	}
	if v.SearchNote != "" {
		items = append(items, item{itemSearchNote, 0})
	}
	for i := range v.Hits {
		items = append(items, item{itemHit, i})
	}
	items = append(items, item{itemUnitsHeader, 0})
	if len(v.Units) == 0 {
		items = append(items, item{itemEmpty, 0})
	}
	for i := range v.Units {
		items = append(items, item{itemUnit, i})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutHeader(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.M, Right: th.Space.M, Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				v.search.Field.Disabled = !v.Exists
				return v.search.Layout(gtx, th)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return v.list.Layout(gtx, th, len(items), func(gtx layout.Context, i int) layout.Dimensions {
				return v.layoutItem(gtx, th, items[i])
			})
		}),
	)
}

func (v *View) layoutHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.UniformInset(th.Space.M).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return atoms.Strong(v.Summary).Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						v.index.Size, v.index.IconSize = th.Size.ControlH-th.Space.XS, th.Size.IconSmall
						return v.index.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						v.settings.Size, v.settings.IconSize = th.Size.ControlH-th.Space.XS, th.Size.IconSmall
						return v.settings.Layout(gtx, th)
					}),
				)
			}),
		}
		for _, d := range v.Details {
			children = append(children, layout.Rigid(atoms.Small(d).Fn(th)))
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.update.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.rebuild.Layout(gtx, th) }),
				)
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func inset(th *tokens.Theme, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: th.Space.M, Right: th.Space.M, Bottom: th.Space.S}.Layout(gtx, w)
	}
}

func (v *View) layoutItem(gtx layout.Context, th *tokens.Theme, it item) layout.Dimensions {
	switch it.kind {
	case itemNotice:
		n := v.Notice
		kind := molecules.BannerWarning
		if n.Error {
			kind = molecules.BannerError
		}
		return inset(th, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(molecules.Banner{Kind: kind, Title: n.Title, Detail: n.Detail}.Fn(th)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if n.Action == "" {
						return layout.Dimensions{}
					}
					v.notice.Text = n.Action
					gtx.Constraints.Min.X = 0
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return v.notice.Layout(gtx, th) })
				}),
			)
		})(gtx)
	case itemConfirm:
		return inset(th, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(molecules.Banner{Kind: molecules.BannerInfo, Title: "Build the knowledge base?", Detail: v.Confirm}.Fn(th)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = 0
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.confirm.Layout(gtx, th) }),
							layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.dismiss.Layout(gtx, th) }),
						)
					})
				}),
			)
		})(gtx)
	case itemProgress:
		return inset(th, v.layoutProgress(th))(gtx)
	case itemSearchNote:
		return inset(th, atoms.Small(v.SearchNote).Fn(th))(gtx)
	case itemHit:
		return v.layoutHit(gtx, th, it.i)
	case itemUnitsHeader:
		title := "Units"
		if v.Searching {
			title = "Searching…"
		}
		return molecules.SectionHeader{Title: title, Badge: fmt.Sprint(len(v.Units))}.Layout(gtx, th)
	case itemEmpty:
		return inset(th, atoms.Muted("No PHP units found in this project.").Fn(th))(gtx)
	case itemUnit:
		return v.layoutUnit(gtx, th, it.i)
	}
	return layout.Dimensions{}
}

func (v *View) layoutProgress(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		val := float32(-1)
		if v.Total > 0 && v.Phase != "embedding" {
			val = float32(v.Done) / float32(v.Total)
		}
		label := fmt.Sprintf("Building: %d of %d units", v.Done, v.Total)
		if v.Phase == "embedding" {
			label = "Embedding the documents…"
		}
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return atoms.Strong(label).Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = 0
						return v.cancel.Layout(gtx, th)
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, atoms.ProgressBar{Value: val}.Fn(th))
			}),
		}
		for _, s := range v.Steps {
			s := s
			if s.State == "queued" || s.State == "" {
				continue
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				g, col := atoms.GlyphRun, th.Color.Accent
				switch s.State {
				case "done":
					g, col = atoms.GlyphDone, th.Color.Success
				case "failed":
					g, col = atoms.GlyphError, th.Color.Danger
				}
				text := s.Title + " · " + s.Step
				if s.Error != "" {
					text += " – " + s.Error
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}.Fn(th)),
					layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = 0
						return atoms.Small(text).SingleLine().Layout(gtx, th)
					}),
				)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

func (v *View) layoutHit(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	h := v.Hits[i]
	return v.hitRows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		text := strings.Join(strings.Fields(h.Text), " ")
		return layout.Inset{Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(atoms.Icon{Glyph: atoms.GlyphSearch, Size: th.Size.IconSmall, Color: th.Color.Accent}.Fn(th)),
						layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return atoms.Strong(h.Heading).SingleLine().Layout(gtx, th)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(fmt.Sprintf("%.2f", h.Score)).Fn(th))
						}),
					)
				}),
				layout.Rigid(atoms.Label{Text: text, Role: atoms.RoleMuted, MaxLines: 2}.Fn(th)),
			)
		})
	})
}

// stateBadge returns the badge of a unit state.
func stateBadge(u Unit) (string, atoms.BadgeKind) {
	switch u.State {
	case "":
		return "", 0
	case "failed":
		return "failed", atoms.BadgeDanger
	case "new":
		return "new", atoms.BadgeInfo
	}
	return u.State, atoms.BadgeWarning
}

func (v *View) layoutUnit(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	u := v.Units[i]
	return v.unitRows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		g, col := atoms.GlyphFileText, th.Color.TextMuted
		if u.HasDoc {
			col = th.Color.Accent
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}.Fn(th)),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = 0
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return atoms.Text(u.Title).SingleLine().Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						info := plural(u.Files, "file")
						if u.HasDoc {
							info += fmt.Sprintf(" · %.0f%%", u.Confidence*100)
						}
						return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(info).SingleLine().Fn(th))
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				text, kind := stateBadge(u)
				if text == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return atoms.Badge{Text: text, Kind: kind}.Layout(gtx, th)
				})
			}),
		)
	})
}

// plural returns "1 file" or "n files".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
