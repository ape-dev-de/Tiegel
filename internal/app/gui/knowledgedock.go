package gui

import (
	"fmt"
	"path/filepath"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/fileicons"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/knowledgeview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Knowledge window (D560, D565): the knowledge plugin's state, build per
// level with estimate and confirmation, progress with cancel, search and
// the pages grouped by level, which open read-only in the Markdown
// preview (they live outside the project). Search and pages stay usable
// while a build runs.

// Knowledge tool window and action IDs.
const (
	ToolKnowledge = "knowledge"

	ActKnowledgeView      keymap.Action = "view.knowledge"
	ActKnowledgeOpen      keymap.Action = "knowledge.open"
	knowledgeOrder                      = 45
	knowledgeSettingsPage               = "plugin:knowledge"
)

type knowledgeDock struct {
	v       *View
	view    knowledgeview.View
	version uint64
	project string
}

func installKnowledge(v *View) *knowledgeDock {
	d := &knowledgeDock{v: v}
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolKnowledge, Title: "Knowledge", Glyph: fileicons.ToolGlyph(fileicons.ToolKnowledge), Side: Right, Order: knowledgeOrder,
		ActionID: ActKnowledgeView,
		Badge: func() (string, atoms.BadgeKind) {
			k := v.ctl.Knowledge
			if k.Status.Run.Running {
				return "…", atoms.BadgeInfo
			}
			return "", 0
		},
		Layout: d.layout,
		OnOpen: func() { v.ctl.Knowledge.Load() },
	})
	v.Registry.AddAction(Action{ID: ActKnowledgeOpen, Title: "Project Knowledge", Menu: "View", Section: "agent",
		Run: func(v *View) { v.ShowTool(ToolKnowledge) }})
	v.Registry.AddFrameHandler(d.frame)
	return d
}

// frame follows the project.
func (d *knowledgeDock) frame(v *View, _ layout.Context) {
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		v.ctl.Knowledge.ProjectChanged()
	}
}

// humanBytes formats a size.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/1024)
	}
	return fmt.Sprintf("%d B", n)
}

// sync copies the controller state into the organism.
func (d *knowledgeDock) sync() {
	k := d.v.ctl.Knowledge
	if k.Version == d.version && d.version != 0 {
		return
	}
	d.version = k.Version
	w := &d.view
	w.Disabled = ""
	switch {
	case d.v.ctl.Project.Root == "":
		w.Disabled = "Open a project to build its knowledge base."
		return
	case k.Disabled:
		w.Disabled = "The knowledge plugin is disabled. Enable it in Settings › Plugins."
		return
	case !k.Loaded:
		w.Summary = "Loading…"
	}
	st := k.Status
	w.Exists, w.Stale = st.Exists || countDocs(k.Units) > 0, st.Stale
	switch {
	case !w.Exists && st.Units == 0:
		w.Summary = "No knowledge base yet"
	default:
		w.Summary = plural(st.Units, "page")
		if st.Stale > 0 {
			w.Summary += fmt.Sprintf(" · %d to update", st.Stale)
		}
		if st.Failed > 0 {
			w.Summary += fmt.Sprintf(" · %d failed", st.Failed)
		}
		if !st.LastRun.IsZero() {
			w.Summary += " · " + st.LastRun.Local().Format("Jan 2 15:04")
		}
	}
	model := st.Model
	if model == "" {
		model = "agent's default model"
	}
	e := st.Embedding
	where := "local"
	if !e.Local {
		where = "sent to " + e.Endpoint
	}
	w.Details = []string{
		"Agent: " + st.Agent + " · " + model,
		fmt.Sprintf("Search: %s · %s (%s)", e.Provider, e.Model, where),
	}
	if e.Chunks > 0 {
		w.Details = append(w.Details, fmt.Sprintf("Store: %d sections, %d embedded, %s", e.Chunks, e.Embedded, humanBytes(e.Bytes)))
	}
	if len(st.Levels) > 0 {
		var parts []string
		for _, l := range st.Levels {
			if l.Pages > 0 || l.Planned > 0 {
				parts = append(parts, fmt.Sprintf("%s %d/%d", l.Title, l.Pages, l.Planned))
			}
		}
		if len(parts) > 0 {
			w.Details = append(w.Details, strings.Join(parts, " · "))
		}
	}
	w.Questions = ""
	if q := st.Questions; q.Open+q.Answered > 0 {
		w.Questions = fmt.Sprintf("%d open · %d answered", q.Open, q.Answered)
	}
	w.Notice = d.notice()
	w.Confirm = ""
	if k.Plan != nil {
		e := k.Plan.Estimate
		if e.Units == 0 {
			w.Confirm = fmt.Sprintf("All %d pages are current – nothing to build.", k.Plan.Total)
		} else {
			var parts []string
			for _, l := range e.Levels {
				if l.Pages > 0 {
					s := fmt.Sprintf("%s %d", l.Title, l.Pages)
					if l.Later {
						s += " (at most)"
					}
					parts = append(parts, s)
				}
			}
			w.Confirm = fmt.Sprintf("%s. %s", strings.Join(parts, ", "), e.Note)
		}
	}
	r := st.Run
	w.Running, w.Phase, w.Total, w.Done, w.Started = r.Running, r.Phase, len(r.Units), 0, r.Started
	w.Steps = w.Steps[:0]
	for _, p := range r.Units {
		if p.State == "done" || p.State == "failed" {
			w.Done++
		}
		w.Steps = append(w.Steps, knowledgeview.Step{ID: p.Unit, Title: p.Title, Level: levelTitle(p.Level), Step: p.Step, State: p.State, Error: p.Error,
			StepIndex: p.StepIndex, Steps: p.Steps, Started: p.Started, Tools: p.Tools, Reads: p.Reads, Chars: p.Chars})
	}
	// The pages of the last run stay inspectable (transcripts in memory).
	w.LastRun = !r.Running && !r.Ended.IsZero() && w.Done > 0
	w.Peek, w.Transcript = k.Peek, k.Transcript
	w.Hits = w.Hits[:0]
	for _, h := range k.Hits {
		w.Hits = append(w.Hits, knowledgeview.Hit{Heading: h.Heading, Path: h.Path, Text: h.Snippet, Score: h.Score})
	}
	w.SearchNote, w.Searching = k.SearchNote, k.Searching
	if k.Query != "" && len(k.Hits) == 0 && !k.Searching && w.SearchNote == "" {
		w.SearchNote = "No results for “" + k.Query + "”."
	}
	w.Units = w.Units[:0]
	for _, u := range k.Units {
		conf, dir := u.Confidence, u.Dir
		if u.Mechanical {
			conf = -1
		}
		if u.Level == api.KnowledgeLevelQuestions {
			dir = u.Summary
		}
		w.Units = append(w.Units, knowledgeview.Unit{ID: u.ID, Title: u.Title, Dir: dir, Level: u.Level, LevelTitle: levelTitle(u.Level), Files: u.Files, Confidence: conf,
			HasDoc: u.MD != "", State: u.Stale, Error: u.Error})
	}
}

// levelTitle is the title of a knowledge level.
func levelTitle(level string) string {
	switch level {
	case api.KnowledgeLevelOverview:
		return "Overview"
	case api.KnowledgeLevelEntryPoints:
		return "Entry points"
	case api.KnowledgeLevelProcesses:
		return "Processes"
	case api.KnowledgeLevelComponents:
		return "Components"
	case api.KnowledgeLevelQuestions:
		return "Open questions"
	}
	return level
}

func countDocs(us []api.KnowledgeUnit) int {
	n := 0
	for _, u := range us {
		if u.MD != "" && u.Level != api.KnowledgeLevelQuestions {
			n++
		}
	}
	return n
}

// notice picks the most important hint.
func (d *knowledgeDock) notice() *knowledgeview.Notice {
	k := d.v.ctl.Knowledge
	m := k.Model
	switch {
	case k.Error != "":
		return &knowledgeview.Notice{Title: "Knowledge", Detail: k.Error, Error: true}
	case m.Pulling:
		return &knowledgeview.Notice{Title: "Pulling " + m.Model, Detail: m.PullStatus}
	case m.Provider == "ollama" && m.Error != "" && m.Reachable:
		return &knowledgeview.Notice{Title: "Pulling the model failed", Detail: m.Error, Error: true, Action: "Try again", ActionID: "pull"}
	case m.Provider == "ollama" && m.Model != "" && !m.Reachable && k.Status.Exists:
		return &knowledgeview.Notice{Title: "Ollama is not running", Detail: "Search works with keywords only. Start Ollama (ollama serve) for semantic search, or choose another provider in the settings."}
	case m.Provider == "ollama" && m.Reachable && !m.Available:
		return &knowledgeview.Notice{Title: "Embedding model missing", Detail: "Semantic search needs the model " + m.Model + ". Pull downloads it on the Ollama server: " + m.PullCommand,
			Action: "Pull model", ActionID: "pull"}
	case k.Status.Run.EmbedError != "" && !k.Status.Run.Running:
		return &knowledgeview.Notice{Title: "Embedding failed", Detail: k.Status.Run.EmbedError + " – keyword search works.", Action: "Embed again", ActionID: "embed"}
	case k.Status.Embedding.Recovered != "":
		return &knowledgeview.Notice{Title: "The search index was reset", Detail: k.Status.Embedding.Recovered, Action: "Embed again", ActionID: "embed"}
	case k.Status.Exists && k.Status.Embedding.Chunks > k.Status.Embedding.Embedded && m.Available && !k.Status.Run.Running:
		return &knowledgeview.Notice{Title: "Not all sections are embedded", Detail: "Search falls back to keywords for them.", Action: "Embed", ActionID: "embed"}
	}
	return nil
}

func (d *knowledgeDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.sync()
	k := d.v.ctl.Knowledge
	for _, e := range d.view.Update(gtx) {
		switch e := e.(type) {
		case knowledgeview.BuildEvent:
			k.AskBuild(e.All, e.Level)
		case knowledgeview.ConfirmEvent:
			if k.Plan != nil && len(k.Plan.Units) == 0 {
				k.DismissPlan()
				continue
			}
			k.Build()
		case knowledgeview.DismissEvent:
			k.DismissPlan()
		case knowledgeview.CancelEvent:
			k.Cancel()
		case knowledgeview.PeekEvent:
			k.PeekAt(e.ID)
		case knowledgeview.SearchEvent:
			k.Search(e.Query)
		case knowledgeview.OpenUnitEvent:
			d.openUnit(e.ID)
		case knowledgeview.OpenHitEvent:
			if e.Index < len(k.Hits) {
				h := k.Hits[e.Index]
				d.v.mdd.openAt(h.File, h.Anchor)
			}
		case knowledgeview.NoticeEvent:
			switch e.ActionID {
			case "pull":
				k.Pull()
			case "embed":
				k.Embed()
			}
		case knowledgeview.SettingsEvent:
			d.v.openSettings()
			d.v.settings.Selected = knowledgeSettingsPage
		}
	}
	return d.view.Layout(gtx, th)
}

// openUnit opens a unit's document ("" = index.md) in the preview.
func (d *knowledgeDock) openUnit(id string) {
	k := d.v.ctl.Knowledge
	if id == "" || id == "questions" {
		name := "index.md"
		if id == "questions" {
			name = "questions.md"
		}
		if k.Status.Dir != "" {
			d.v.mdd.openAt(filepath.Join(k.Status.Dir, name), "")
		}
		return
	}
	for _, u := range k.Units {
		if u.ID == id && u.MD != "" {
			d.v.mdd.openAt(u.MD, "")
			return
		}
	}
}
