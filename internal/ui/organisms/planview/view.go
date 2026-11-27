// Package planview is the plan mode view of the agent sidebar (D567):
// the goal and state of a plan, the roles with their state and elapsed
// time (a click shows the role's transcript), the summary, Tiegel's gate
// results, disputes between DEV and QA with both positions ("DEV is
// right" / "QA is right" / comment), the changed files, and the actions
// of the state: Accept / Request changes / Discard after planning, Stop
// while running, Resume after a cancel, Apply / Keep branch / Discard at
// the end. The host fills the exported fields and handles the Events.
package planview

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Role is a row of the role list.
type Role struct {
	// Name is the role ID (planner-architecture, dev …), Title its label.
	Name, Title string
	// State is idle, running, done or failed; Elapsed a short duration.
	State, Elapsed, Task string
}

// Gate is the result of one of Tiegel's checks.
type Gate struct {
	Name string
	// State is passed, failed or skipped.
	State, Text string
}

// Position is one side of a dispute.
type Position struct{ Role, Text string }

// Dispute is a review item waiting for the user.
type Dispute struct {
	ID, Claim, Evidence string
	Positions           []Position
}

// Change is a changed file of the plan.
type Change struct {
	Path           string
	Added, Removed int
}

// Event is reported by View.Update.
type Event interface{ isPlanEvent() }

// Events of the view.
type (
	// AcceptEvent: the user accepted the plan (the host executes it).
	AcceptEvent struct{}
	// ChangesEvent: the user requested changes with a comment.
	ChangesEvent struct{ Comment string }
	// DiscardEvent: discard the plan (and its branch).
	DiscardEvent struct{}
	// DocumentEvent: open the working document.
	DocumentEvent struct{}
	// RoleEvent: show the transcript of a role.
	RoleEvent struct{ Role string }
	// ResolveEvent: decide a dispute (Decision dev, qa or comment).
	ResolveEvent struct{ Item, Decision, Comment string }
	// ApplyEvent: apply the changes to the working copy.
	ApplyEvent struct{}
	// KeepEvent: keep the plan branch, remove the worktree.
	KeepEvent struct{}
	// CancelEvent: stop the running orchestration.
	CancelEvent struct{}
	// ResumeEvent: continue a cancelled or interrupted plan.
	ResumeEvent struct{}
	// DiffEvent: show the diff of a changed file.
	DiffEvent struct{ Path string }
	// CloseEvent: back to the conversation.
	CloseEvent struct{}
)

func (AcceptEvent) isPlanEvent()   {}
func (ChangesEvent) isPlanEvent()  {}
func (DiscardEvent) isPlanEvent()  {}
func (DocumentEvent) isPlanEvent() {}
func (RoleEvent) isPlanEvent()     {}
func (ResolveEvent) isPlanEvent()  {}
func (ApplyEvent) isPlanEvent()    {}
func (KeepEvent) isPlanEvent()     {}
func (CancelEvent) isPlanEvent()   {}
func (ResumeEvent) isPlanEvent()   {}
func (DiffEvent) isPlanEvent()     {}
func (CloseEvent) isPlanEvent()    {}

// Plan states the view knows (api.Plan*).
const (
	StateDrafting   = "drafting"
	StateReview     = "awaiting-acceptance"
	StateAccepted   = "accepted"
	StateExecuting  = "executing"
	StateNeedsUser  = "needs-user"
	StateDone       = "done"
	StateIncomplete = "incomplete"
	StateFailed     = "failed"
	StateCancelled  = "cancelled"
	StateDiscarded  = "discarded"
	StateApplied    = "applied"
)

// View is the plan view.
type View struct {
	Title, State, Phase string
	Running             bool
	Iteration           int
	Roles               []Role
	// Summary is the plan summary (planning) or the final report.
	Summary   string
	Gates     []Gate
	Disputes  []Dispute
	Changes   []Change
	Workspace string
	// Worktree enables "Keep branch".
	Worktree bool
	Notes    []string
	Error    string

	ok                                  bool
	list                                molecules.ScrollList
	comment                             atoms.TextField
	accept, changes, discard, doc       atoms.Button
	apply, keep, stop, resume, close    atoms.Button
	roleRows                            map[string]*molecules.ListRow
	changeRows                          map[string]*molecules.ListRow
	devRight, qaRight, commentOn        map[string]*atoms.Button
	pending                             []Event
	confirmDiscard, yesDiscard, noClose atoms.Button
	confirming                          bool
}

func (v *View) init() {
	if v.ok {
		return
	}
	v.ok = true
	v.comment.Hint = "Comment: what should change?"
	v.accept = atoms.Button{Text: "Accept", Kind: atoms.Primary, Glyph: atoms.GlyphArrowRight}
	v.changes = atoms.Button{Text: "Request Changes", Kind: atoms.Secondary}
	v.discard = atoms.Button{Text: "Discard", Kind: atoms.Ghost}
	v.doc = atoms.Button{Text: "Open Document", Kind: atoms.Ghost}
	v.apply = atoms.Button{Text: "Apply to Working Copy", Kind: atoms.Primary}
	v.keep = atoms.Button{Text: "Keep Branch", Kind: atoms.Secondary}
	v.stop = atoms.Button{Text: "Stop", Kind: atoms.Secondary, Glyph: atoms.GlyphStop}
	v.resume = atoms.Button{Text: "Resume", Kind: atoms.Primary}
	v.close = atoms.Button{Text: "Back to Chat", Kind: atoms.Ghost}
	v.yesDiscard = atoms.Button{Text: "Discard Plan", Kind: atoms.Primary}
	v.noClose = atoms.Button{Text: "Cancel", Kind: atoms.Ghost}
	v.roleRows = map[string]*molecules.ListRow{}
	v.changeRows = map[string]*molecules.ListRow{}
	v.devRight, v.qaRight, v.commentOn = map[string]*atoms.Button{}, map[string]*atoms.Button{}, map[string]*atoms.Button{}
}

func (v *View) emit(e Event) { v.pending = append(v.pending, e) }

// Update returns the user's intentions since the last call.
func (v *View) Update(gtx layout.Context) []Event {
	v.init()
	text := strings.TrimSpace(v.comment.Text())
	if v.accept.Clicked(gtx) {
		v.emit(AcceptEvent{})
	}
	if v.changes.Clicked(gtx) && text != "" {
		v.comment.SetText("")
		v.emit(ChangesEvent{Comment: text})
	}
	if v.discard.Clicked(gtx) {
		v.confirming = true
	}
	if v.noClose.Clicked(gtx) {
		v.confirming = false
	}
	if v.yesDiscard.Clicked(gtx) {
		v.confirming = false
		v.emit(DiscardEvent{})
	}
	if v.doc.Clicked(gtx) {
		v.emit(DocumentEvent{})
	}
	if v.apply.Clicked(gtx) {
		v.emit(ApplyEvent{})
	}
	if v.keep.Clicked(gtx) {
		v.emit(KeepEvent{})
	}
	if v.stop.Clicked(gtx) {
		v.emit(CancelEvent{})
	}
	if v.resume.Clicked(gtx) {
		v.emit(ResumeEvent{})
	}
	if v.close.Clicked(gtx) {
		v.emit(CloseEvent{})
	}
	for _, r := range v.Roles {
		if row := v.roleRows[r.Name]; row != nil {
			if c, _ := row.Update(gtx); c {
				v.emit(RoleEvent{Role: r.Name})
			}
		}
	}
	for _, c := range v.Changes {
		if row := v.changeRows[c.Path]; row != nil {
			if cl, _ := row.Update(gtx); cl {
				v.emit(DiffEvent{Path: c.Path})
			}
		}
	}
	for _, d := range v.Disputes {
		if b := v.devRight[d.ID]; b != nil && b.Clicked(gtx) {
			v.emit(ResolveEvent{Item: d.ID, Decision: "dev", Comment: text})
		}
		if b := v.qaRight[d.ID]; b != nil && b.Clicked(gtx) {
			v.emit(ResolveEvent{Item: d.ID, Decision: "qa", Comment: text})
		}
		if b := v.commentOn[d.ID]; b != nil && b.Clicked(gtx) && text != "" {
			v.comment.SetText("")
			v.emit(ResolveEvent{Item: d.ID, Decision: "comment", Comment: text})
		}
	}
	out := v.pending
	v.pending = nil
	return out
}

// StateText is the user-facing text of a plan state.
func StateText(state string) (string, atoms.BadgeKind) {
	switch state {
	case StateDrafting:
		return "planning…", atoms.BadgeAccent
	case StateReview:
		return "awaiting your review", atoms.BadgeWarning
	case StateAccepted:
		return "accepted", atoms.BadgeInfo
	case StateExecuting:
		return "implementing…", atoms.BadgeAccent
	case StateNeedsUser:
		return "needs your decision", atoms.BadgeWarning
	case StateDone:
		return "done – all gates passed", atoms.BadgeSuccess
	case StateIncomplete:
		return "incomplete", atoms.BadgeWarning
	case StateFailed:
		return "failed", atoms.BadgeDanger
	case StateCancelled:
		return "stopped", atoms.BadgeNeutral
	case StateApplied:
		return "applied", atoms.BadgeSuccess
	case StateDiscarded:
		return "discarded", atoms.BadgeNeutral
	}
	return state, atoms.BadgeNeutral
}

// Layout draws the view filling the constraints.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.init()
	v.pending = append(v.pending, v.Update(gtx)...)
	size := gtx.Constraints.Max
	gtx.Constraints = layout.Exact(size)
	var rows []layout.Widget
	rows = append(rows, v.header(th))
	if v.Error != "" {
		rows = append(rows, banner(th, molecules.BannerError, "The plan stopped", v.Error))
	}
	for _, n := range v.Notes {
		rows = append(rows, banner(th, molecules.BannerInfo, n, ""))
	}
	if len(v.Roles) > 0 {
		rows = append(rows, section(th, "Roles", fmt.Sprint(len(v.Roles))))
		live := map[string]bool{}
		for _, r := range v.Roles {
			live[r.Name] = true
			rows = append(rows, v.roleRow(th, r))
		}
		prune(v.roleRows, live)
	}
	if v.Summary != "" {
		title := "Summary"
		if v.Iteration > 0 {
			title = "Report"
		}
		rows = append(rows, section(th, title, ""), text(th, v.Summary))
	}
	if len(v.Disputes) > 0 {
		rows = append(rows, section(th, "Disputes", fmt.Sprint(len(v.Disputes))))
		for _, d := range v.Disputes {
			rows = append(rows, v.dispute(th, d))
		}
	}
	if len(v.Gates) > 0 {
		rows = append(rows, section(th, "Checks by Tiegel", ""))
		for _, g := range v.Gates {
			rows = append(rows, gate(th, g))
		}
	}
	if len(v.Changes) > 0 {
		rows = append(rows, section(th, "Changed files", fmt.Sprint(len(v.Changes))))
		live := map[string]bool{}
		for _, c := range v.Changes {
			live[c.Path] = true
			rows = append(rows, v.changeRow(th, c))
		}
		prune(v.changeRows, live)
	}
	if v.Workspace != "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return inset(th).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return atoms.Label{Text: v.Workspace, Role: atoms.RoleSmall, Color: th.Color.TextMuted}.Layout(gtx, th)
			})
		})
	}
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return v.list.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return rows[i](gtx)
			})
		}),
		layout.Rigid(with(th, atoms.Divider{})),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions { return v.actions(gtx, th) })
		}),
	)
	return layout.Dimensions{Size: size}
}

func (v *View) header(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.M, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := atoms.Strong(v.Title)
					l.MaxLines = 3
					return l.Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					s, kind := StateText(v.State)
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(with(th, atoms.Badge{Text: s, Kind: kind})),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								p := v.Phase
								if v.Iteration > 0 {
									p = fmt.Sprintf("%s · iteration %d", p, v.Iteration)
								}
								if p == "" {
									return layout.Dimensions{}
								}
								return layout.Inset{Left: th.Space.S}.Layout(gtx, with(th, atoms.Label{Text: p, Role: atoms.RoleSmall, Color: th.Color.TextMuted}))
							}),
						)
					})
				}),
			)
		})
	}
}

func (v *View) roleRow(th *tokens.Theme, r Role) layout.Widget {
	row := v.roleRows[r.Name]
	if row == nil {
		row = &molecules.ListRow{}
		v.roleRows[r.Name] = row
	}
	return func(gtx layout.Context) layout.Dimensions {
		return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			glyph, col := atoms.GlyphPause, th.Color.TextMuted
			switch r.State {
			case "running":
				glyph, col = atoms.GlyphRun, th.Color.Accent
			case "done":
				glyph, col = atoms.GlyphCheck, th.Color.Success
			case "failed":
				glyph, col = atoms.GlyphError, th.Color.Danger
			}
			title := r.Title
			if title == "" {
				title = r.Name
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: th.Space.S}.Layout(gtx, with(th, atoms.Icon{Glyph: glyph, Size: th.Size.IconSmall, Color: col}))
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := atoms.Label{Text: title}
							l.MaxLines = 1
							return l.Layout(gtx, th)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if r.Task == "" {
								return layout.Dimensions{}
							}
							l := atoms.Label{Text: r.Task, Role: atoms.RoleSmall, Color: th.Color.TextMuted}
							l.MaxLines = 1
							return l.Layout(gtx, th)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return atoms.Label{Text: r.Elapsed, Role: atoms.RoleSmall, Color: th.Color.TextMuted}.Layout(gtx, th)
				}),
			)
		})
	}
}

func (v *View) changeRow(th *tokens.Theme, c Change) layout.Widget {
	row := v.changeRows[c.Path]
	if row == nil {
		row = &molecules.ListRow{}
		v.changeRows[c.Path] = row
	}
	return func(gtx layout.Context) layout.Dimensions {
		return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := atoms.Label{Text: c.Path, Role: atoms.RoleCode}
					l.MaxLines = 1
					return l.Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return atoms.Label{Text: fmt.Sprintf("+%d", c.Added), Role: atoms.RoleSmall, Color: th.Color.Success}.Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, with(th, atoms.Label{Text: fmt.Sprintf("−%d", c.Removed), Role: atoms.RoleSmall, Color: th.Color.Danger}))
				}),
			)
		})
	}
}

func (v *View) dispute(th *tokens.Theme, d Dispute) layout.Widget {
	get := func(m map[string]*atoms.Button, text string, kind atoms.ButtonKind) *atoms.Button {
		b := m[d.ID]
		if b == nil {
			b = &atoms.Button{Text: text, Kind: kind}
			m[d.ID] = b
		}
		return b
	}
	dev, qa, com := get(v.devRight, "DEV is right", atoms.Secondary), get(v.qaRight, "QA is right", atoms.Secondary), get(v.commentOn, "Comment", atoms.Ghost)
	return func(gtx layout.Context) layout.Dimensions {
		return inset(th).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			kids := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return atoms.Strong(d.ID+" · "+d.Claim).Layout(gtx, th)
				}),
			}
			if d.Evidence != "" {
				kids = append(kids, layout.Rigid(with(th, atoms.Label{Text: d.Evidence, Role: atoms.RoleCode, Color: th.Color.TextMuted})))
			}
			for _, p := range d.Positions {
				p := p
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Dp(unit.Dp(40))
								return atoms.Label{Text: strings.ToUpper(p.Role), Role: atoms.RoleSmall, Color: th.Color.Accent}.Layout(gtx, th)
							}),
							layout.Flexed(1, with(th, atoms.Label{Text: p.Text})),
						)
					})
				}))
			}
			kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(with(th, dev)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Space.XS, Right: th.Space.XS}.Layout(gtx, with(th, qa))
						}),
						layout.Rigid(with(th, com)),
					)
				})
			}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		})
	}
}

// actions are the buttons of the current state.
func (v *View) actions(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if v.confirming {
		return row(gtx, th, "Discard the plan and all its changes?", &v.yesDiscard, &v.noClose)
	}
	switch {
	case v.Running:
		return row(gtx, th, "Agents are working – nothing is committed.", &v.stop)
	case v.State == StateReview || v.State == StateNeedsUser:
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return v.comment.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					v.changes.Disabled = strings.TrimSpace(v.comment.Text()) == ""
					if v.State == StateNeedsUser {
						return row(gtx, th, "", &v.doc, &v.discard)
					}
					return two(gtx, th, []*atoms.Button{&v.doc, &v.discard}, []*atoms.Button{&v.changes, &v.accept})
				})
			}),
		)
	case v.State == StateCancelled || v.State == StateAccepted:
		return row(gtx, th, "", &v.close, &v.discard, &v.resume)
	case v.State == StateDone || v.State == StateIncomplete || v.State == StateFailed:
		if v.Worktree {
			return two(gtx, th, []*atoms.Button{&v.doc, &v.discard}, []*atoms.Button{&v.keep, &v.apply})
		}
		v.apply.Text = "Keep Changes"
		v.discard.Text = "Revert All"
		return two(gtx, th, []*atoms.Button{&v.doc, &v.discard}, []*atoms.Button{&v.apply})
	}
	return row(gtx, th, "", &v.doc, &v.close)
}

// two lays out secondary buttons above the main ones.
func two(gtx layout.Context, th *tokens.Theme, top, bottom []*atoms.Button) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return row(gtx, th, "", top...) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return row(gtx, th, "", bottom...) })
		}),
	)
}

// row lays out an optional hint and right-aligned buttons; on narrow
// widths the buttons wrap.
func row(gtx layout.Context, th *tokens.Theme, hint string, bs ...*atoms.Button) layout.Dimensions {
	var kids []layout.FlexChild
	kids = append(kids, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		if hint == "" {
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
		}
		return atoms.Label{Text: hint, Role: atoms.RoleSmall, Color: th.Color.TextMuted}.Layout(gtx, th)
	}))
	for i, b := range bs {
		b := b
		left := th.Space.XS
		if i == 0 {
			left = 0
		}
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: left}.Layout(gtx, with(th, b))
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
}

func gate(th *tokens.Theme, g Gate) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			kind := atoms.BadgeSuccess
			switch g.State {
			case "failed":
				kind = atoms.BadgeDanger
			case "skipped":
				kind = atoms.BadgeNeutral
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(unit.Dp(84))
					return atoms.Label{Text: g.Name}.Layout(gtx, th)
				}),
				layout.Rigid(with(th, atoms.Badge{Text: g.State, Kind: kind})),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := atoms.Label{Text: g.Text, Role: atoms.RoleSmall, Color: th.Color.TextMuted}
					l.MaxLines = 2
					return layout.Inset{Left: th.Space.S}.Layout(gtx, with(th, l))
				}),
			)
		})
	}
}

func section(th *tokens.Theme, title, badge string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: th.Space.S}.Layout(gtx, with(th, molecules.SectionHeader{Title: title, Badge: badge}))
	}
}

func text(th *tokens.Theme, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return inset(th).Layout(gtx, with(th, atoms.Label{Text: s}))
	}
}

func banner(th *tokens.Theme, kind molecules.BannerKind, title, detail string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return inset(th).Layout(gtx, with(th, molecules.Banner{Kind: kind, Title: title, Detail: detail}))
	}
}

func inset(th *tokens.Theme) layout.Inset {
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}
}

func prune[T any](m map[string]*T, live map[string]bool) {
	for k := range m {
		if !live[k] {
			delete(m, k)
		}
	}
}

type themed interface {
	Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions
}

func with(th *tokens.Theme, c themed) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return c.Layout(gtx, th) }
}
