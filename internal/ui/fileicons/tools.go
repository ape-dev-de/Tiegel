package fileicons

import "math"

// ToolName names a tool-window or toolbar glyph (see Tool).
type ToolName string

// Tool-window and toolbar glyphs. All share the 16-unit grid and a
// 1.5-unit stroke; they are monochrome and drawn in Icon.Color.
const (
	ToolProject       ToolName = "project"
	ToolCommit        ToolName = "commit"
	ToolStructure     ToolName = "structure"
	ToolTerminal      ToolName = "terminal"
	ToolRun           ToolName = "run"
	ToolDebug         ToolName = "debug"
	ToolProblems      ToolName = "problems"
	ToolGit           ToolName = "git" // version-control log
	ToolServices      ToolName = "services"
	ToolTests         ToolName = "tests"
	ToolDatabase      ToolName = "database"
	ToolNotifications ToolName = "notifications"
	ToolFind          ToolName = "find" // find-in-files results
	ToolBookmarks     ToolName = "bookmarks"
	ToolTodo          ToolName = "todo"
	ToolPlugins       ToolName = "plugins"
	ToolAgent         ToolName = "agent"
	ToolBranch        ToolName = "branch"
	ToolSettings      ToolName = "settings"
	ToolSearch        ToolName = "search"
	ToolStop          ToolName = "stop"
	ToolSleep         ToolName = "sleep"     // hibernate a project or service
	ToolKnowledge     ToolName = "knowledge" // project knowledge (a brain)
)

// toolAliases maps alternative names to glyphs.
var toolAliases = map[ToolName]ToolName{
	"log":       ToolGit,
	"vcs":       ToolGit,
	"hibernate": ToolSleep,
	"test":      ToolTests,
	"brain":     ToolKnowledge,
}

// sw is the stroke width of tool glyphs.
const sw = 1.5

var toolDefs = map[ToolName]func(d *drawer){
	ToolProject: func(d *drawer) {
		d.stroke(sw, true, pt{1.5, 2.5}, pt{6, 2.5}, pt{7.5, 4.5}, pt{14.5, 4.5}, pt{14.5, 13.5}, pt{1.5, 13.5})
	},
	ToolCommit: func(d *drawer) {
		d.ring(8, 8, 3, sw)
		d.line(sw, 0.75, 8, 4.25, 8)
		d.line(sw, 11.75, 8, 15.25, 8)
	},
	ToolStructure: func(d *drawer) {
		d.rrect(1.5, 1.5, 7, 5.5, 1)
		d.stroke(sw, false, pt{4, 5.5}, pt{4, 12}, pt{8.5, 12})
		d.line(sw, 4, 8, 8.5, 8)
		d.rrect(9, 6.25, 14.5, 9.75, 1)
		d.rrect(9, 10.25, 14.5, 13.75, 1)
	},
	ToolTerminal: func(d *drawer) {
		d.rrectRing(1, 2, 15, 14, 2, sw)
		d.stroke(sw, false, pt{4.5, 6}, pt{6.75, 8}, pt{4.5, 10})
		d.line(sw, 8.5, 10.5, 11.5, 10.5)
	},
	ToolRun: func(d *drawer) {
		d.stroke(1, true, pt{4.5, 3}, pt{13, 8}, pt{4.5, 13})
		d.poly([]pt{{4.5, 3}, {13, 8}, {4.5, 13}}, false)
	},
	ToolDebug: func(d *drawer) {
		d.ellipseRing(8, 9.75, 3.5, 4.25, sw)
		d.line(sw, 8, 7, 8, 13.5)
		d.poly(d.arcPts(8, 5.5, 2.5, 2.5, math.Pi, 2*math.Pi), false)
		d.line(sw, 1.5, 7, 4.5, 8)
		d.line(sw, 14.5, 7, 11.5, 8)
		d.line(sw, 1.5, 12.5, 4.5, 11.5)
		d.line(sw, 14.5, 12.5, 11.5, 11.5)
	},
	ToolProblems: func(d *drawer) {
		d.stroke(sw, true, pt{8, 1.75}, pt{14.75, 13.75}, pt{1.25, 13.75})
		d.line(sw, 8, 6, 8, 9)
		d.circle(8, 11.4, 0.95)
	},
	ToolGit: func(d *drawer) {
		d.line(sw, 8, 0.75, 8, 15.25)
		d.circle(8, 4, 2+sw/2)
		d.circle(8, 12, 2+sw/2)
		d.fill(colorOpaque)
		d.circle(8, 4, 2-sw/2)
		d.circle(8, 12, 2-sw/2)
		d.erase()
	},
	ToolServices: func(d *drawer) {
		d.rrectRing(1, 1.5, 15, 7, 1.5, sw)
		d.rrectRing(1, 9, 15, 14.5, 1.5, sw)
		d.circle(4.5, 4.25, 1)
		d.circle(4.5, 11.75, 1)
	},
	ToolTests: func(d *drawer) {
		d.stroke(sw, false, pt{6.25, 1.75}, pt{6.25, 6}, pt{2, 13.75}, pt{14, 13.75}, pt{9.75, 6}, pt{9.75, 1.75})
		d.line(sw, 4.75, 1.75, 11.25, 1.75)
		d.poly([]pt{{4.2, 10}, {11.8, 10}, {14, 13.75}, {2, 13.75}}, false)
	},
	ToolDatabase: func(d *drawer) {
		d.ellipseRing(8, 3.75, 5.75, 2.25, sw)
		d.line(sw, 2.25, 3.75, 2.25, 12.25)
		d.line(sw, 13.75, 3.75, 13.75, 12.25)
		d.stroke(sw, false, d.arcPts(8, 12.25, 5.75, 2.25, 0, math.Pi)...)
		d.stroke(sw, false, d.arcPts(8, 8, 5.75, 2.25, 0, math.Pi)...)
	},
	ToolNotifications: func(d *drawer) {
		ps := []pt{{2.25, 12}, {4, 10}}
		ps = append(ps, d.arcPts(8, 6.75, 4, 4.25, math.Pi, 2*math.Pi)...)
		ps = append(ps, pt{12, 10}, pt{13.75, 12})
		d.stroke(sw, true, ps...)
		d.line(sw, 6.5, 14.5, 9.5, 14.5)
	},
	ToolFind: func(d *drawer) {
		d.line(sw, 1.5, 2.5, 7, 2.5)
		d.line(sw, 1.5, 6.5, 4, 6.5)
		d.line(sw, 1.5, 10.5, 4, 10.5)
		d.ring(10, 8.5, 3, sw)
		d.line(sw, 12.4, 10.9, 14.75, 13.25)
	},
	ToolBookmarks: func(d *drawer) {
		d.stroke(sw, true, pt{3.5, 1.5}, pt{12.5, 1.5}, pt{12.5, 14.5}, pt{8, 10.75}, pt{3.5, 14.5})
	},
	ToolTodo: func(d *drawer) {
		d.stroke(sw, false, pt{1.5, 4}, pt{3.25, 5.75}, pt{6.25, 2.25})
		d.rrectRing(1.25, 9, 6, 13.75, 1, sw)
		d.line(sw, 8.5, 4, 14.5, 4)
		d.line(sw, 8.5, 11.5, 14.5, 11.5)
	},
	ToolPlugins: func(d *drawer) {
		d.line(sw, 6, 1.25, 6, 4.5)
		d.line(sw, 10, 1.25, 10, 4.5)
		d.stroke(sw, true, pt{3.5, 4.75}, pt{12.5, 4.75}, pt{12.5, 8}, pt{10, 11}, pt{6, 11}, pt{3.5, 8})
		d.line(sw, 8, 11, 8, 14.75)
	},
	ToolAgent: func(d *drawer) {
		d.poly([]pt{{7, 1.5}, {8.4, 6.1}, {13, 7.5}, {8.4, 8.9}, {7, 13.5}, {5.6, 8.9}, {1, 7.5}, {5.6, 6.1}}, false)
		d.poly([]pt{{12.5, 9.5}, {13.1, 11.4}, {15, 12}, {13.1, 12.6}, {12.5, 14.5}, {11.9, 12.6}, {10, 12}, {11.9, 11.4}}, false)
	},
	ToolBranch: func(d *drawer) {
		d.ring(4.5, 3.25, 1.75, sw)
		d.ring(4.5, 12.75, 1.75, sw)
		d.ring(11.5, 4.5, 1.75, sw)
		d.line(sw, 4.5, 5, 4.5, 11)
		d.stroke(sw, false, quadPts(pt{11.5, 6.25}, pt{11.5, 9.5}, pt{4.5, 10.5}, 10)...)
	},
	ToolSettings: func(d *drawer) {
		const teeth = 8
		var ps []pt
		ro, ri := float32(7.25), float32(5.5)
		for k := 0; k < teeth; k++ {
			a := float64(k) * 2 * math.Pi / teeth
			at := func(r float32, da float64) pt {
				return pt{8 + r*float32(math.Cos(a+da)), 8 + r*float32(math.Sin(a+da))}
			}
			ps = append(ps, at(ri, -0.36), at(ro, -0.2), at(ro, 0.2), at(ri, 0.36))
		}
		d.poly(ps, false)
		d.poly(d.circlePts(8, 8, 2.5), true)
	},
	ToolSearch: func(d *drawer) {
		d.ring(6.75, 6.75, 4.5, sw)
		d.line(sw, 10.25, 10.25, 14.5, 14.5)
	},
	ToolStop: func(d *drawer) {
		d.rrect(3, 3, 13, 13, 1.5)
	},
	ToolKnowledge: func(d *drawer) {
		// Left hemisphere as three lobes, mirrored for the right one.
		var half []pt
		half = append(half, d.arcPts(5.6, 4, 2.4, 2.4, -0.45, -math.Pi)...)
		half = append(half, d.arcPts(3.6, 7.6, 2.4, 2.4, 4.55, 2.2)...)
		half = append(half, d.arcPts(5.2, 11, 2.6, 2.5, 3.6, 0.35)...)
		mirror := func(ps []pt) []pt {
			out := make([]pt, len(ps))
			for i, p := range ps {
				out[i] = pt{16 - p.x, p.y}
			}
			return out
		}
		d.stroke(sw, false, half...)
		d.stroke(sw, false, mirror(half)...)
		d.line(sw, 8, 2.75, 8, 13.25)
		// Folds between the lobes.
		for _, f := range [][3]pt{
			{{3.6, 5.6}, {5.6, 5.4}, {5.8, 7.4}},
			{{2.8, 9.6}, {5, 9.2}, {5.6, 11.2}},
		} {
			fold := quadPts(f[0], f[1], f[2], 8)
			d.stroke(sw*0.85, false, fold...)
			d.stroke(sw*0.85, false, mirror(fold)...)
		}
	},
	ToolSleep: func(d *drawer) {
		d.circle(7, 9, 5.75)
		d.fill(colorOpaque)
		d.circle(10.5, 5.5, 4.75)
		d.erase()
		d.stroke(1.25, false, pt{10.5, 1.25}, pt{14.25, 1.25}, pt{10.5, 5}, pt{14.25, 5})
	},
}

// toolOrder is the canonical order of Tools.
var toolOrder = []ToolName{
	ToolProject, ToolCommit, ToolStructure, ToolTerminal, ToolRun, ToolDebug,
	ToolProblems, ToolGit, ToolServices, ToolTests, ToolDatabase,
	ToolNotifications, ToolFind, ToolBookmarks, ToolTodo, ToolPlugins,
	ToolAgent, ToolBranch, ToolSettings, ToolSearch, ToolStop, ToolSleep,
	ToolKnowledge,
}

// Tools returns all tool glyph names in a stable order.
func Tools() []ToolName { return append([]ToolName(nil), toolOrder...) }
