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
// left, variables and watches on the right. Narrower than two columns of
// tokens.ShellSizes.PanelColumnMin (docked at a side) the columns are
// stacked, so section titles and placeholders never get squeezed. Each
// part is an exported organism the host fills with data; Update collects
// all their events.
type DebugPanel struct {
	Toolbar     DebugToolbar
	Sessions    SessionList
	Stack       CallStackList
	Variables   VariablesTree
	Watches     WatchesList
	Breakpoints BreakpointsList

	// main divides the columns side by side, stacked when narrow.
	main, stacked, left, right, top molecules.Split
	isStacked                       bool
}

// NewDebugPanel returns a panel with default split ratios.
func NewDebugPanel() *DebugPanel {
	p := &DebugPanel{}
	p.main = molecules.Split{Ratio: 0.38}
	p.stacked = molecules.Split{Vertical: true, Ratio: 0.45}
	p.left = molecules.Split{Vertical: true, Ratio: 0.6}
	p.right = molecules.Split{Vertical: true, Ratio: 0.65}
	p.top = molecules.Split{Vertical: true, Ratio: 0.3}
	return p
}

// Stacked reports whether the last Layout stacked the columns.
func (p *DebugPanel) Stacked() bool { return p.isStacked }

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
	sh := th.Shell()
	size := gtx.Constraints.Max
	if size.X > unbounded {
		size.X = gtx.Dp(sh.PanelFallbackW)
	}
	if size.Y > unbounded {
		size.Y = gtx.Dp(sh.PanelFallbackH)
	}
	p.isStacked = size.X < 2*gtx.Dp(sh.PanelColumnMin)+gtx.Dp(th.Size.BorderWidth)
	for _, sp := range []*molecules.Split{&p.stacked, &p.left, &p.right, &p.top} {
		sp.MinPane = sh.PanelSectionMin
	}
	p.main.MinPane = sh.PanelColumnMin
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
			if p.isStacked {
				return p.stacked.Layout(gtx, th, p.layoutLeft(th), p.layoutRight(th))
			}
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
