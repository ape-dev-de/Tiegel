package knowledgeview

import (
	"fmt"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Live progress (D566): the bar moves within a page (step k of n), the
// running pages show their step, elapsed time and the agent's counters
// with a sweeping bar, and a click on a page opens its live transcript.

// maxPeekLines caps the transcript lines laid out.
const maxPeekLines = 2000

func (v *View) now(gtx layout.Context) time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return gtx.Now
}

// fraction is the share of the build that is done, counting the steps
// of running pages.
func (v *View) fraction() float32 {
	if v.Total <= 0 {
		return -1
	}
	f := float32(v.Done)
	for _, s := range v.Steps {
		if s.State == "running" && s.Steps > 0 && s.StepIndex > 0 {
			f += float32(s.StepIndex-1) / float32(s.Steps)
		}
	}
	return min(1, f/float32(v.Total))
}

// Elapsed formats a duration as m:ss or h:mm:ss.
func Elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Counters formats the agent's live counters ("" = none).
func Counters(s Step) string {
	var parts []string
	if s.Tools > 0 {
		t := plural(s.Tools, "tool call")
		if s.Reads > 0 {
			t += fmt.Sprintf(" (%d reads)", s.Reads)
		}
		parts = append(parts, t)
	}
	if s.Chars > 0 {
		tok := (s.Chars + 3) / 4
		if tok >= 1000 {
			parts = append(parts, fmt.Sprintf("~%.1fk tokens", float64(tok)/1000))
		} else {
			parts = append(parts, fmt.Sprintf("~%d tokens", tok))
		}
	}
	return strings.Join(parts, " · ")
}

func (v *View) layoutProgress(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	now := v.now(gtx)
	if v.Running {
		// The elapsed timers tick every second.
		gtx.Execute(op.InvalidateCmd{At: now.Truncate(time.Second).Add(time.Second)})
	}
	var label string
	switch {
	case !v.Running:
		done, failed := 0, 0
		for _, s := range v.Steps {
			switch s.State {
			case "done":
				done++
			case "failed":
				failed++
			}
		}
		label = fmt.Sprintf("Last run: %d done", done)
		if failed > 0 {
			label += fmt.Sprintf(", %d failed", failed)
		}
	case v.Phase == "embedding":
		label = "Embedding the pages…"
	default:
		label = fmt.Sprintf("Building: %d of %d pages", v.Done, v.Total)
	}
	if v.Running && !v.Started.IsZero() {
		label += " · " + Elapsed(now.Sub(v.Started))
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
					if v.Running {
						return v.cancel.Layout(gtx, th)
					}
					v.lastRun.Text = map[bool]string{true: "Hide", false: "Details"}[v.showLast || v.Peek != ""]
					return v.lastRun.Layout(gtx, th)
				}),
			)
		}),
	}
	if v.Running {
		val := v.fraction()
		if v.Phase == "embedding" {
			val = -1
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, atoms.ProgressBar{Value: val}.Fn(th))
		}))
	}
	if v.Running || v.showLast || v.Peek != "" {
		for i := range v.Steps {
			s := v.Steps[i]
			if s.State == "queued" || s.State == "" {
				continue
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.layoutStep(gtx, th, i, now)
			}))
			if s.ID != "" && s.ID == v.Peek {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return v.layoutTranscript(gtx, th)
				}))
			}
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutStep draws a page of the build; a click opens its transcript.
func (v *View) layoutStep(gtx layout.Context, th *tokens.Theme, i int, now time.Time) layout.Dimensions {
	s := v.Steps[i]
	return v.stepRows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		g, col := atoms.GlyphRun, th.Color.Accent
		switch s.State {
		case "done":
			g, col = atoms.GlyphDone, th.Color.Success
		case "failed":
			g, col = atoms.GlyphError, th.Color.Danger
		}
		text := s.Title
		if s.Level != "" {
			text = s.Level + " › " + text
		}
		var info []string
		if s.State == "running" {
			st := s.Step
			if s.StepIndex > 0 && s.Steps > 0 {
				st += fmt.Sprintf(" %d/%d", s.StepIndex, s.Steps)
			}
			info = append(info, st)
			if !s.Started.IsZero() {
				info = append(info, Elapsed(now.Sub(s.Started)))
			}
		}
		if c := Counters(s); c != "" {
			info = append(info, c)
		}
		if s.Error != "" {
			info = append(info, s.Error)
		}
		rows := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}.Fn(th)),
					layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return atoms.Small(text).SingleLine().Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if s.ID == "" {
							return layout.Dimensions{}
						}
						gl := atoms.GlyphChevronRight
						if s.ID == v.Peek {
							gl = atoms.GlyphChevronDown
						}
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Icon{Glyph: gl, Size: th.Size.IconSmall, Color: th.Color.TextMuted}.Fn(th))
					}),
				)
			}),
		}
		if len(info) > 0 {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Size.IconSmall + th.Space.XS}.Layout(gtx, atoms.Label{Text: strings.Join(info, " · "), Role: atoms.RoleSmall, MaxLines: 1}.WithColor(th.Color.TextMuted).Fn(th))
			}))
		}
		if s.State == "running" {
			// A thin sweeping bar: the page is alive while the agent works.
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Size.IconSmall + th.Space.XS, Top: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return atoms.ProgressBar{Value: -1, Height: 2}.Layout(gtx, th)
				})
			}))
		}
		return layout.Inset{Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
}

// layoutTranscript draws the open transcript: a read-only log that
// follows new lines while scrolled to the bottom.
func (v *View) layoutTranscript(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	lines := v.Transcript
	if len(lines) > maxPeekLines {
		lines = lines[len(lines)-maxPeekLines:]
	}
	if len(lines) == 0 {
		lines = []string{"Waiting for the agent…"}
	}
	// Follow the end when the last line was visible (or on open).
	if n := len(lines); n != v.peekLines {
		if v.peekLines == 0 || v.peekList.LastVisible() >= v.peekLines-1 {
			v.peekList.ScrollTo(n - 1)
		}
		v.peekLines = n
	}
	return layout.Inset{Top: th.Space.XXS, Bottom: th.Space.S, Left: th.Size.IconSmall + th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Max.Y = gtx.Dp(240)
		gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
		return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.XS).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return v.peekList.Layout(gtx, th, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
					return transcriptLine(lines[i]).Layout(gtx, th)
				})
			})
		})
	})
}

// transcriptLine styles a transcript line by its prefix.
func transcriptLine(l string) atoms.Label {
	switch {
	case strings.HasPrefix(l, "» "):
		return atoms.Label{Text: l, Role: atoms.RoleStrong}
	case strings.HasPrefix(l, "thought: "), strings.HasPrefix(l, "  "):
		return atoms.Label{Text: l, Role: atoms.RoleMuted}
	case strings.HasPrefix(l, "tool: "):
		return atoms.Label{Text: l, Role: atoms.RoleSmall}
	}
	return atoms.Code(l)
}
