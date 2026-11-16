package debug

import (
	"fmt"
	"image"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// DebugPanel is the debugger tool window template: the toolbar on top, the
// sessions (when more than one) and the call stack plus breakpoints on the
// left, variables and watches on the right. Each part is an exported
// organism the host fills with data; Update collects all their events.
type DebugPanel struct {
	Toolbar     DebugToolbar
	Sessions    SessionList
	Stack       CallStackList
	Variables   VariablesTree
	Watches     WatchesList
	Breakpoints BreakpointsList

	main, left, right, top molecules.Split
}

// NewDebugPanel returns a panel with default split ratios.
func NewDebugPanel() *DebugPanel {
	p := &DebugPanel{}
	p.main = molecules.Split{Ratio: 0.38}
	p.left = molecules.Split{Vertical: true, Ratio: 0.6, MinPane: 48}
	p.right = molecules.Split{Vertical: true, Ratio: 0.65, MinPane: 48}
	p.top = molecules.Split{Vertical: true, Ratio: 0.3, MinPane: 48}
	return p
}

// Update returns the events of all parts since the last call.
func (p *DebugPanel) Update(gtx layout.Context) []Event {
	var out []Event
	out = append(out, p.Toolbar.Update(gtx)...)
	out = append(out, p.Sessions.Update(gtx)...)
	out = append(out, p.Stack.Update(gtx)...)
	out = append(out, p.Variables.Update(gtx)...)
	out = append(out, p.Watches.Update(gtx)...)
	out = append(out, p.Breakpoints.Update(gtx)...)
	return out
}

// Layout draws the panel; it fills the available space.
func (p *DebugPanel) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.Update(gtx)
	size := gtx.Constraints.Max
	if size.X > unbounded {
		size.X = gtx.Dp(720)
	}
	if size.Y > unbounded {
		size.Y = gtx.Dp(420)
	}
	gtx.Constraints = layout.Exact(size)
	atoms.Surface{}.Paint(gtx, th)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			h := gtx.Dp(th.Size.ControlH) + gtx.Dp(th.Space.S)
			sz := image.Pt(gtx.Constraints.Max.X, h)
			cgtx := gtx
			cgtx.Constraints = layout.Exact(sz)
			atoms.Surface{Kind: atoms.SurfaceAlt}.Paint(cgtx, th)
			cgtx.Constraints = layout.Constraints{Max: sz}
			layout.Inset{Left: th.Space.XS, Top: th.Space.XS}.Layout(cgtx, func(gtx layout.Context) layout.Dimensions {
				return p.Toolbar.Layout(gtx, th)
			})
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(atoms.Divider{}.Fn(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return p.main.Layout(gtx, th, p.layoutLeft(th), p.layoutRight(th))
		}),
	)
}

func (p *DebugPanel) layoutLeft(th *tokens.Theme) layout.Widget {
	stack := section(th, "Frames", count(len(p.Stack.Frames)), nil, func(gtx layout.Context) layout.Dimensions {
		return p.Stack.Layout(gtx, th)
	})
	if len(p.Sessions.Items) > 1 {
		sessions := section(th, "Sessions", count(len(p.Sessions.Items)), nil, func(gtx layout.Context) layout.Dimensions {
			return p.Sessions.Layout(gtx, th)
		})
		inner := stack
		stack = func(gtx layout.Context) layout.Dimensions { return p.top.Layout(gtx, th, sessions, inner) }
	}
	bps := section(th, "Breakpoints", count(len(p.Breakpoints.Items)), nil, func(gtx layout.Context) layout.Dimensions {
		return p.Breakpoints.Layout(gtx, th)
	})
	return func(gtx layout.Context) layout.Dimensions { return p.left.Layout(gtx, th, stack, bps) }
}

func (p *DebugPanel) layoutRight(th *tokens.Theme) layout.Widget {
	vars := section(th, "Variables", count(len(p.Variables.Roots)), nil, func(gtx layout.Context) layout.Dimensions {
		return p.Variables.Layout(gtx, th)
	})
	watches := section(th, "Watches", count(len(p.Watches.Items)), nil, func(gtx layout.Context) layout.Dimensions {
		return p.Watches.Layout(gtx, th)
	})
	return func(gtx layout.Context) layout.Dimensions { return p.right.Layout(gtx, th, vars, watches) }
}

func count(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// section stacks a SectionHeader above content filling the rest.
func section(th *tokens.Theme, title, badge string, trailing layout.Widget, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(molecules.SectionHeader{Title: title, Badge: badge, Trailing: trailing}.Fn(th)),
			layout.Flexed(1, content),
		)
	}
}
