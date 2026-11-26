package agentchat

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/mdview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Chat is the agent sidebar. Keep it in the host's state; fill the data
// fields, call Update, then Layout.
type Chat struct {
	// Agent names the agent ("OpenCode 2.2.0"); State is a short state
	// text ("ready", "working…"); Busy marks a running turn.
	Agent string
	State string
	Busy  bool
	// Models are the model choices, Model the current value.
	Models []Choice
	Model  string
	// Policy is the one-line permission summary.
	Policy string
	// Notice is an optional banner (not installed, login, failure).
	Notice  *NoticeBox
	Entries []Entry
	Changes []Change
	// Empty is shown while there are no entries.
	Empty string

	list    molecules.ScrollList
	input   atoms.TextField
	inputOK bool
	send    atoms.Button
	stop    atoms.Button
	newBtn  atoms.IconButton
	gear    atoms.IconButton
	policy  widget.Clickable
	action  atoms.Button
	model   molecules.Dropdown
	models  []Choice // choices the dropdown was built from

	keepAll, revertAll, confirm, cancelConfirm atoms.Button
	confirming                                 bool
	changeList                                 molecules.ScrollList
	changeRows                                 map[string]*changeRow

	rows    map[int]*row
	pending []Event
}

// row is the widget state of one entry.
type row struct {
	md      mdview.View
	mdText  string
	toggle  molecules.ListRow
	open    bool
	answers []atoms.Button
	answerK string // options the buttons were built for
}

// changeRow is the widget state of one changed file.
type changeRow struct {
	row    molecules.ListRow
	revert atoms.Button
}

func (c *Chat) init() {
	if c.inputOK {
		return
	}
	c.inputOK = true
	c.input.Hint = "Ask the agent… (Enter sends, Shift+Enter: new line)"
	c.input.Editor.Submit = true
	c.send = atoms.Button{Text: "Send", Kind: atoms.Primary, Glyph: atoms.GlyphArrowRight}
	c.stop = atoms.Button{Text: "Stop", Kind: atoms.Secondary, Glyph: atoms.GlyphStop}
	c.newBtn = atoms.IconButton{Glyph: atoms.GlyphAdd, Description: "New Session"}
	c.gear = atoms.IconButton{Glyph: atoms.GlyphSettings, Description: "Agent Settings"}
	c.action = atoms.Button{Kind: atoms.Primary}
	c.keepAll = atoms.Button{Text: "Keep All", Kind: atoms.Ghost}
	c.revertAll = atoms.Button{Text: "Revert All", Kind: atoms.Ghost, Glyph: atoms.GlyphHistory}
	c.confirm = atoms.Button{Text: "Revert", Kind: atoms.Primary}
	c.cancelConfirm = atoms.Button{Text: "Cancel", Kind: atoms.Ghost}
	c.list.List.ScrollToEnd = true
	c.rows = map[int]*row{}
	c.changeRows = map[string]*changeRow{}
	c.model = molecules.Dropdown{Selected: -1, Placeholder: "Model"}
}

// Input returns the message input (the host may focus it).
func (c *Chat) Input() *widget.Editor {
	c.init()
	return &c.input.Editor
}

func (c *Chat) emit(e Event) { c.pending = append(c.pending, e) }

// Update returns the user's intentions since the last call.
func (c *Chat) Update(gtx layout.Context) []Event {
	c.init()
	for {
		ev, ok := c.input.Editor.Update(gtx)
		if !ok {
			break
		}
		if s, ok := ev.(widget.SubmitEvent); ok {
			c.submit(s.Text)
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &c.input.Editor, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press && c.Busy {
			c.emit(CancelEvent{})
		}
	}
	if c.send.Clicked(gtx) {
		c.submit(c.input.Text())
	}
	if c.stop.Clicked(gtx) {
		c.emit(CancelEvent{})
	}
	if c.newBtn.Clicked(gtx) {
		c.emit(NewSessionEvent{})
	}
	if c.gear.Clicked(gtx) || c.policy.Clicked(gtx) {
		c.emit(SettingsEvent{})
	}
	if c.action.Clicked(gtx) {
		c.emit(NoticeActionEvent{})
	}
	if i, ok := c.model.Update(gtx); ok && i >= 0 && i < len(c.models) {
		c.emit(ModelEvent{Value: c.models[i].Value})
	}
	if c.keepAll.Clicked(gtx) {
		c.emit(KeepAllEvent{})
	}
	if c.revertAll.Clicked(gtx) {
		c.confirming = true
	}
	if c.cancelConfirm.Clicked(gtx) {
		c.confirming = false
	}
	if c.confirm.Clicked(gtx) {
		c.confirming = false
		c.emit(RevertAllEvent{})
	}
	for _, ch := range c.Changes {
		r := c.changeRows[ch.Path]
		if r == nil {
			continue
		}
		if r.revert.Clicked(gtx) {
			c.emit(RevertEvent{Path: ch.Path})
		} else if clicked, _ := r.row.Update(gtx); clicked {
			c.emit(DiffEvent{Path: ch.Path})
		}
	}
	for _, e := range c.Entries {
		r := c.rows[e.Key]
		if r == nil {
			continue
		}
		if clicked, _ := r.toggle.Update(gtx); clicked {
			r.open = !r.open
		}
		if e.Perm != nil && !e.Perm.Answered {
			for i := range r.answers {
				if i < len(e.Perm.Options) && r.answers[i].Clicked(gtx) {
					c.emit(AnswerEvent{RequestID: e.Perm.ID, OptionID: e.Perm.Options[i].ID})
				}
			}
		}
		if e.Kind == Agent {
			for _, me := range r.md.Update(gtx) {
				switch me := me.(type) {
				case mdview.LinkEvent:
					c.emit(LinkEvent{Dest: me.Dest})
				case mdview.CopyEvent:
					c.emit(CopyEvent{Text: me.Text})
				}
			}
		}
	}
	out := c.pending
	c.pending = nil
	return out
}

func (c *Chat) submit(text string) {
	text = strings.TrimSpace(text)
	if text == "" || c.Busy {
		return
	}
	c.input.SetText("")
	c.emit(SendEvent{Text: text})
}

// Layout draws the sidebar filling the constraints.
func (c *Chat) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	c.init()
	c.pending = append(c.pending, c.Update(gtx)...)
	size := gtx.Constraints.Max
	draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Surface)
	gtx.Constraints = layout.Exact(size)
	pad := layout.UniformInset(th.Space.S)
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.header(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.modelRow(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.policyRow(gtx, th) }),
	}
	if c.Notice != nil {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return c.notice(gtx, th) })
		}))
	}
	children = append(children,
		layout.Rigid(with(th, atoms.Divider{})),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return c.transcript(gtx, th) }),
	)
	if len(c.Changes) > 0 {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.changes(gtx, th) }))
	}
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return c.inputArea(gtx, th) })
	}))
	layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	return layout.Dimensions{Size: size}
}

func (c *Chat) header(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	c.syncModels()
	return layout.Inset{Left: th.Space.S, Right: th.Space.XS, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				name := c.Agent
				if name == "" {
					name = "Agent"
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						l := atoms.Strong(name)
						l.MaxLines = 1
						return l.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if c.State == "" {
							return layout.Dimensions{}
						}
						kind := atoms.BadgeNeutral
						if c.Busy {
							kind = atoms.BadgeAccent
						}
						return layout.Inset{Left: th.Space.XS, Right: th.Space.XS}.Layout(gtx, with(th, atoms.Badge{Text: c.State, Kind: kind}))
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.newBtn.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.gear.Layout(gtx, th) }),
		)
	})
}

// modelRow is the model choice (full width, below the header).
func (c *Chat) modelRow(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if len(c.models) == 0 {
		return layout.Dimensions{}
	}
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return c.model.Layout(gtx, th)
	})
}

// syncModels rebuilds the model dropdown when the choices changed.
func (c *Chat) syncModels() {
	same := len(c.models) == len(c.Models)
	for i := 0; same && i < len(c.Models); i++ {
		same = c.models[i] == c.Models[i]
	}
	if !same {
		c.models = append([]Choice(nil), c.Models...)
		c.model.Options = make([]molecules.DropdownOption, len(c.models))
		for i, m := range c.models {
			name := m.Name
			if name == "" {
				name = m.Value
			}
			c.model.Options[i] = molecules.DropdownOption{Label: name}
		}
	}
	c.model.Selected = -1
	for i, m := range c.models {
		if m.Value == c.Model {
			c.model.Selected = i
		}
	}
}

func (c *Chat) policyRow(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if c.Policy == "" {
		return layout.Dimensions{}
	}
	return c.policy.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			col := th.Color.TextMuted
			if c.policy.Hovered() {
				col = th.Color.Accent
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: th.Space.XS}.Layout(gtx, with(th, atoms.Icon{Glyph: atoms.GlyphSettings, Size: th.Size.IconSmall, Color: col}))
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return atoms.Label{Text: c.Policy, Role: atoms.RoleSmall, Color: col}.Layout(gtx, th)
				}),
			)
		})
	})
}

func (c *Chat) notice(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	n := c.Notice
	kind := molecules.BannerInfo
	if n.Error {
		kind = molecules.BannerError
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(with(th, molecules.Banner{Kind: kind, Title: n.Title, Detail: n.Detail})),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if n.Action == "" {
				return layout.Dimensions{}
			}
			c.action.Text = n.Action
			gtx.Constraints.Min.X = 0
			return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return c.action.Layout(gtx, th) })
		}),
	)
}

func (c *Chat) transcript(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if len(c.Entries) == 0 {
		msg := c.Empty
		if msg == "" {
			msg = "Ask the agent to change, explain or test code. Its file changes are listed below the conversation, each with a diff and Revert."
		}
		return layout.UniformInset(th.Space.L).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := atoms.Muted(msg)
			return l.Layout(gtx, th)
		})
	}
	live := map[int]bool{}
	for _, e := range c.Entries {
		live[e.Key] = true
		if c.rows[e.Key] == nil {
			c.rows[e.Key] = &row{}
		}
	}
	for k := range c.rows {
		if !live[k] {
			delete(c.rows, k)
		}
	}
	return c.list.Layout(gtx, th, len(c.Entries), func(gtx layout.Context, i int) layout.Dimensions {
		e := c.Entries[i]
		top := th.Space.S
		if i == 0 {
			top = th.Space.M
		}
		bottom := unit.Dp(0)
		if i == len(c.Entries)-1 {
			bottom = th.Space.M
		}
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: top, Bottom: bottom}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return c.entry(gtx, th, e, c.rows[e.Key])
		})
	})
}

func (c *Chat) entry(gtx layout.Context, th *tokens.Theme, e Entry, r *row) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	switch e.Kind {
	case User:
		return atoms.Surface{Kind: atoms.SurfaceAlt, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.S).Layout(gtx, with(th, atoms.Text(e.Text)))
		})
	case Agent:
		if r.mdText != e.Text {
			r.mdText = e.Text
			r.md.SetDoc(markdown.Parse([]byte(e.Text)))
		}
		return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.md.LayoutInline(gtx, th) })
	case Thought:
		return c.thought(gtx, th, e, r)
	case Tool:
		return c.tool(gtx, th, e.Tool, r)
	case Plan:
		return c.plan(gtx, th, e.Plan)
	case Permission:
		return c.permission(gtx, th, e.Perm, r)
	}
	return c.noticeEntry(gtx, th, e)
}

// disclosure lays out a clickable one-line row with a chevron.
func disclosure(gtx layout.Context, th *tokens.Theme, r *row, icon layout.Widget, title layout.Widget, trailing layout.Widget) layout.Dimensions {
	return r.toggle.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		chev := atoms.GlyphChevronRight
		if r.open {
			chev = atoms.GlyphChevronDown
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(with(th, atoms.Icon{Glyph: chev, Size: th.Size.IconSmall, Color: th.Color.TextMuted})),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.XS, Right: th.Space.XS}.Layout(gtx, icon)
			}),
			layout.Flexed(1, title),
			layout.Rigid(trailing),
		)
	})
}

func nothing(layout.Context) layout.Dimensions { return layout.Dimensions{} }

func (c *Chat) thought(gtx layout.Context, th *tokens.Theme, e Entry, r *row) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return disclosure(gtx, th, r, with(th, atoms.Icon{Glyph: atoms.GlyphInfo, Size: th.Size.IconSmall, Color: th.Color.TextMuted}),
				func(gtx layout.Context) layout.Dimensions {
					l := atoms.Label{Text: "Thinking", Role: atoms.RoleSmall, Color: th.Color.TextMuted, MaxLines: 1}
					if !r.open {
						l.Text = "Thinking: " + firstLine(e.Text)
					}
					return l.Layout(gtx, th)
				}, nothing)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !r.open {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.L, Top: th.Space.XS}.Layout(gtx,
				with(th, atoms.Label{Text: e.Text, Role: atoms.RoleSmall, Color: th.Color.TextMuted}))
		}),
	)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// statusIcon returns the glyph and color of a tool status.
func statusIcon(th *tokens.Theme, status string) (*atoms.Glyph, color.NRGBA) {
	switch status {
	case "completed":
		return atoms.GlyphCheck, th.Color.Success
	case "failed":
		return atoms.GlyphError, th.Color.Danger
	case "in_progress":
		return atoms.GlyphRun, th.Color.Accent
	}
	return atoms.GlyphMore, th.Color.TextMuted
}

func (c *Chat) tool(gtx layout.Context, th *tokens.Theme, t *ToolCall, r *row) layout.Dimensions {
	g, col := statusIcon(th, t.Status)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return disclosure(gtx, th, r, with(th, atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}),
				func(gtx layout.Context) layout.Dimensions {
					title := t.Title
					if t.Command != "" && !strings.Contains(title, t.Command) && title != "" {
						title += ": " + t.Command
					}
					if len(t.Paths) > 0 && t.Command == "" && !strings.Contains(title, t.Paths[0]) {
						title += "  " + strings.Join(t.Paths, ", ")
					}
					return atoms.Label{Text: title, Role: atoms.RoleSmall, MaxLines: 1}.Layout(gtx, th)
				},
				func(gtx layout.Context) layout.Dimensions { return diffCounts(gtx, th, t.Diffs) })
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !r.open {
				return layout.Dimensions{}
			}
			var parts []layout.FlexChild
			if t.Command != "" {
				parts = append(parts, layout.Rigid(with(th, atoms.Code("$ "+t.Command))))
			}
			for _, d := range t.Diffs {
				parts = append(parts, layout.Rigid(with(th, atoms.Small(fmt.Sprintf("%s  +%d −%d", d.Path, d.Added, d.Removed)))))
			}
			if len(t.Diffs) == 0 {
				for _, p := range t.Paths {
					parts = append(parts, layout.Rigid(with(th, atoms.Small(p))))
				}
			}
			if t.Output != "" {
				out := atoms.Code(t.Output)
				out.MaxLines = 12
				parts = append(parts, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, with(th, out))
				}))
			}
			return layout.Inset{Left: th.Space.XL, Top: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, parts...)
			})
		}),
	)
}

// diffCounts draws "+3 −1" in success/danger colors.
func diffCounts(gtx layout.Context, th *tokens.Theme, ds []DiffStat) layout.Dimensions {
	a, r := 0, 0
	for _, d := range ds {
		a += d.Added
		r += d.Removed
	}
	if len(ds) == 0 {
		return layout.Dimensions{}
	}
	return counts(gtx, th, a, r)
}

func counts(gtx layout.Context, th *tokens.Theme, a, r int) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(with(th, atoms.Label{Text: fmt.Sprintf("+%d", a), Role: atoms.RoleSmall, Color: th.Color.Success})),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, with(th, atoms.Label{Text: fmt.Sprintf("−%d", r), Role: atoms.RoleSmall, Color: th.Color.Danger}))
		}),
	)
}

func (c *Chat) plan(gtx layout.Context, th *tokens.Theme, items []PlanItem) layout.Dimensions {
	children := []layout.FlexChild{layout.Rigid(with(th, atoms.Label{Text: "Plan", Role: atoms.RoleSmall, Color: th.Color.TextMuted}))}
	for _, it := range items {
		g, col := atoms.GlyphMore, th.Color.TextMuted
		switch it.Status {
		case "completed":
			g, col = atoms.GlyphCheck, th.Color.Success
		case "in_progress":
			g, col = atoms.GlyphArrowRight, th.Color.Accent
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Space.XS}.Layout(gtx, with(th, atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}))
					}),
					layout.Flexed(1, with(th, atoms.Small(it.Text))),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (c *Chat) permission(gtx layout.Context, th *tokens.Theme, p *Request, r *row) layout.Dimensions {
	k := p.ID
	for _, o := range p.Options {
		k += "|" + o.ID
	}
	if r.answerK != k {
		r.answerK = k
		r.answers = make([]atoms.Button, len(p.Options))
		for i, o := range p.Options {
			kind := atoms.Secondary
			if o.Kind == "allow_once" {
				kind = atoms.Primary
			}
			r.answers[i] = atoms.Button{Text: optionLabel(o), Kind: kind}
		}
	}
	body := func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Space.XS}.Layout(gtx, with(th, atoms.Icon{Glyph: atoms.GlyphWarning, Size: th.Size.IconSmall, Color: th.Color.Warning}))
					}),
					layout.Flexed(1, with(th, atoms.Strong("The agent asks for permission"))),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.XS}.Layout(gtx, with(th, atoms.Text(p.Summary)))
			}),
		}
		if p.Answered {
			txt, col := "Rejected", th.Color.Danger
			if p.Allowed {
				txt, col = "Allowed", th.Color.Success
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.XS}.Layout(gtx, with(th, atoms.Label{Text: txt, Role: atoms.RoleSmall, Color: col}))
			}))
		} else {
			btns := make([]layout.Widget, 0, len(r.answers))
			for i := range r.answers {
				b := &r.answers[i]
				btns = append(btns, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return flow(gtx, gtx.Dp(th.Space.XS), btns)
				})
			}))
		}
		return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
	}
	return atoms.Surface{Kind: atoms.SurfaceAlt, Border: true, Rounded: true}.Layout(gtx, th, body)
}

func (c *Chat) noticeEntry(gtx layout.Context, th *tokens.Theme, e Entry) layout.Dimensions {
	g, col, txt := atoms.GlyphInfo, th.Color.TextMuted, th.Color.TextMuted
	switch e.Level {
	case Blocked:
		g, col, txt = atoms.GlyphWarning, th.Color.Warning, th.Color.Text
	case Error:
		g, col, txt = atoms.GlyphError, th.Color.Danger, th.Color.Danger
	}
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.XS, Top: th.Space.XXS}.Layout(gtx, with(th, atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}))
		}),
		layout.Flexed(1, with(th, atoms.Label{Text: e.Text, Role: atoms.RoleSmall, Color: txt})),
	)
}

func (c *Chat) changes(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	live := map[string]bool{}
	for _, ch := range c.Changes {
		live[ch.Path] = true
		if c.changeRows[ch.Path] == nil {
			c.changeRows[ch.Path] = &changeRow{revert: atoms.Button{Text: "Revert", Kind: atoms.Ghost}}
		}
	}
	for p := range c.changeRows {
		if !live[p] {
			delete(c.changeRows, p)
		}
	}
	header := molecules.SectionHeader{Title: "Changes", Badge: fmt.Sprint(len(c.Changes)), Trailing: func(gtx layout.Context) layout.Dimensions {
		if c.confirming {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(with(th, atoms.Small(fmt.Sprintf("Revert %d file(s)?", len(c.Changes))))),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return c.confirm.Layout(gtx, th) })
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.cancelConfirm.Layout(gtx, th) }),
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.keepAll.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.revertAll.Layout(gtx, th) }),
		)
	}}
	maxH := max(gtx.Dp(th.Size.RowH)*2, gtx.Constraints.Max.Y/3)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(with(th, atoms.Divider{})),
		layout.Rigid(with(th, header)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// Rows are as high as their Revert button.
			rowH := max(gtx.Dp(th.Size.RowH), gtx.Dp(th.Size.ControlH))
			gtx.Constraints.Max.Y = min(maxH, rowH*len(c.Changes)+2)
			gtx.Constraints.Min.Y = 0
			return c.changeList.Layout(gtx, th, len(c.Changes), func(gtx layout.Context, i int) layout.Dimensions {
				return c.changeRow(gtx, th, c.Changes[i])
			})
		}),
	)
}

func (c *Chat) changeRow(gtx layout.Context, th *tokens.Theme, ch Change) layout.Dimensions {
	r := c.changeRows[ch.Path]
	letter, kind := "M", atoms.BadgeInfo
	switch ch.Kind {
	case "created":
		letter, kind = "A", atoms.BadgeSuccess
	case "deleted":
		letter, kind = "D", atoms.BadgeDanger
	}
	return r.row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: th.Space.S}.Layout(gtx, with(th, atoms.Badge{Text: letter, Kind: kind}))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return atoms.Label{Text: ch.Path, Role: atoms.RoleSmall, MaxLines: 1}.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !ch.Conflict {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.XS}.Layout(gtx, with(th, atoms.Icon{Glyph: atoms.GlyphWarning, Size: th.Size.IconSmall, Color: th.Color.Warning}))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S, Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return counts(gtx, th, ch.Added, ch.Removed)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.revert.Layout(gtx, th) }),
		)
	})
}

func (c *Chat) inputArea(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// Grow with the text up to a third of the window.
			gtx.Constraints.Max.Y = max(gtx.Dp(th.Size.ControlH), gtx.Constraints.Max.Y/3)
			return c.input.Layout(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						hint := "Escape cancels"
						if !c.Busy {
							hint = ""
						}
						return atoms.Label{Text: hint, Role: atoms.RoleSmall, Color: th.Color.TextMuted}.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if c.Busy {
							return c.stop.Layout(gtx, th)
						}
						c.send.Disabled = strings.TrimSpace(c.input.Text()) == ""
						return c.send.Layout(gtx, th)
					}),
				)
			})
		}),
	)
}

// themed is a component laid out with the theme.
type themed interface {
	Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions
}

// with turns a themed component into a layout.Widget.
func with(th *tokens.Theme, c themed) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return c.Layout(gtx, th) }
}

// optionLabel is a short button text for a permission option.
func optionLabel(o Option) string {
	switch o.Kind {
	case "allow_once":
		return "Allow"
	case "allow_always":
		return "Always"
	case "reject_once":
		return "Reject"
	case "reject_always":
		return "Never"
	}
	return o.Name
}

// flow lays out ws left to right and wraps to a new line when the width
// is used up.
func flow(gtx layout.Context, gap int, ws []layout.Widget) layout.Dimensions {
	maxW := gtx.Constraints.Max.X
	x, y, lineH, w := 0, 0, 0, 0
	for _, wd := range ws {
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(maxW, gtx.Constraints.Max.Y)}
		m := op.Record(gtx.Ops)
		d := wd(cg)
		call := m.Stop()
		if x > 0 && x+d.Size.X > maxW {
			x, y, lineH = 0, y+lineH+gap, 0
		}
		t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		x += d.Size.X + gap
		lineH = max(lineH, d.Size.Y)
		w = max(w, x-gap)
	}
	return layout.Dimensions{Size: image.Pt(w, y+lineH)}
}
