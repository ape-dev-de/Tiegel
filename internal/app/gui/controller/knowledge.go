package controller

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Knowledge is the state of the Knowledge window (D560): status, units,
// the running build and search results of the knowledge plugin
// (knowledge.*). Builds start only after the user confirmed the
// estimate; models are pulled only on click.
type Knowledge struct {
	c *Controller
	// Status, Units and Model are the plugin's state (Loaded: fetched;
	// Disabled: the plugin does not run).
	Status   api.KnowledgeStatus
	Units    []api.KnowledgeUnit
	Model    api.KnowledgeModelStatus
	Loaded   bool
	Disabled bool
	// Plan is the estimate waiting for confirmation (nil: none);
	// PlanAll tells whether it is a rebuild, PlanLevel which level ("" =
	// all).
	Plan      *api.KnowledgePlan
	PlanAll   bool
	PlanLevel string
	// Query, Hits and SearchNote are the last search.
	Query      string
	Hits       []api.KnowledgeHit
	SearchNote string
	Searching  bool
	// Error is the last failed call.
	Error string
	// Peek is the page whose live transcript is open ("" = none),
	// Transcript its lines (knowledge.transcript, refreshed on
	// knowledge.activity events of the page).
	Peek       string
	Transcript []string
	// Version changes with every update.
	Version uint64

	// peekBusy is set while a transcript fetch runs; peekDirty asks for
	// another one after it.
	peekBusy, peekDirty bool

	project string
	// opened is set once the window loaded (only then the Ollama
	// server is asked about the model).
	opened bool
}

func newKnowledge(c *Controller) *Knowledge { return &Knowledge{c: c} }

// Load fetches status, units and the model state (the window opened).
func (k *Knowledge) Load() {
	k.opened = true
	k.load()
}

func (k *Knowledge) load() {
	if k.c.Project.Root == "" {
		return
	}
	opened, ms := k.opened, k.Model
	k.c.Go(func(ctx context.Context) func() {
		var st api.KnowledgeStatus
		err := k.c.call(ctx, "knowledge.status", nil, &st)
		var us []api.KnowledgeUnit
		if err == nil {
			_ = k.c.call(ctx, "knowledge.list", nil, &us)
			if opened {
				_ = k.c.call(ctx, "knowledge.modelStatus", nil, &ms)
			}
		}
		return func() {
			k.Loaded = true
			k.Disabled = disabledErr(err)
			if err == nil {
				k.Status, k.Units, k.Model = st, us, ms
			} else if !k.Disabled {
				k.Error = err.Error()
			}
			k.Version++
		}
	})
}

// ProjectChanged forgets the state of the previous project.
func (k *Knowledge) ProjectChanged() {
	if k.c.Project.Root == k.project {
		return
	}
	k.project = k.c.Project.Root
	*k = Knowledge{c: k.c, project: k.project, Version: k.Version + 1}
}

// AskBuild fetches the plan of a level ("" = all levels) with its
// estimate; the window shows it for confirmation (nothing runs yet).
func (k *Knowledge) AskBuild(all bool, level string) {
	k.c.Go(func(ctx context.Context) func() {
		var pl api.KnowledgePlan
		err := k.c.call(ctx, "knowledge.plan", api.KnowledgeScopeParams{All: all, Level: level}, &pl)
		return func() {
			if err != nil {
				k.Error = err.Error()
			} else {
				k.Plan, k.PlanAll, k.PlanLevel, k.Error = &pl, all, level, ""
			}
			k.Version++
		}
	})
}

// DismissPlan drops the pending confirmation.
func (k *Knowledge) DismissPlan() {
	k.Plan = nil
	k.Version++
}

// Build runs the confirmed plan.
func (k *Knowledge) Build() {
	all, level := k.PlanAll, k.PlanLevel
	k.Plan = nil
	k.Version++
	k.c.Go(func(ctx context.Context) func() {
		var run api.KnowledgeRun
		err := k.c.call(ctx, "knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{All: all, Level: level}}, &run)
		return func() {
			if err != nil {
				k.Error = err.Error()
			} else {
				k.Status.Run, k.Error = run, ""
			}
			k.Version++
		}
	})
}

// Cancel cancels the running build.
func (k *Knowledge) Cancel() {
	k.c.Go(func(ctx context.Context) func() {
		var run api.KnowledgeRun
		err := k.c.call(ctx, "knowledge.cancel", nil, &run)
		return func() {
			if err == nil {
				k.Status.Run = run
			}
			k.Version++
		}
	})
}

// Pull pulls the embedding model (the window showed the command).
func (k *Knowledge) Pull() {
	k.c.Go(func(ctx context.Context) func() {
		var ms api.KnowledgeModelStatus
		err := k.c.call(ctx, "knowledge.pullModel", api.KnowledgePullParams{}, &ms)
		return func() {
			if err != nil {
				k.Error = err.Error()
			} else {
				k.Model = ms
			}
			k.Version++
		}
	})
}

// Embed (re-)embeds the documents.
func (k *Knowledge) Embed() {
	k.c.Go(func(ctx context.Context) func() {
		var r api.KnowledgeEmbedResult
		err := k.c.call(ctx, "knowledge.embed", api.KnowledgeEmbedParams{}, &r)
		return func() {
			switch {
			case err != nil:
				k.Error = err.Error()
			case r.Error != "":
				k.Error = "Embedding: " + r.Error
			default:
				k.Error = ""
			}
			k.Load()
		}
	})
}

// Search runs a search.
func (k *Knowledge) Search(q string) {
	q = strings.TrimSpace(q)
	k.Query = q
	if q == "" {
		k.Hits, k.SearchNote = nil, ""
		k.Version++
		return
	}
	k.Searching = true
	k.Version++
	k.c.Go(func(ctx context.Context) func() {
		var r api.KnowledgeSearchResult
		err := k.c.call(ctx, "knowledge.search", api.KnowledgeSearchParams{Query: q, K: 12}, &r)
		return func() {
			if k.Query != q {
				return // a newer search runs
			}
			k.Searching = false
			if err != nil {
				k.Hits, k.SearchNote = nil, err.Error()
			} else {
				k.Hits, k.SearchNote = r.Hits, r.Note
			}
			k.Version++
		}
	})
}

// handleEvent applies a knowledge.* event.
func (k *Knowledge) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicKnowledgeStatus:
		var st api.KnowledgeStatus
		if json.Unmarshal(payload, &st) != nil {
			return
		}
		finished := k.Status.Run.Running && !st.Run.Running
		k.Status, k.Loaded = st, true
		if finished || st.Units != countDocs(k.Units) {
			k.load() // pages changed (also while a build runs)
		}
		if !st.Run.Running && k.opened {
			k.refreshModel()
		}
	case api.TopicKnowledgeProgress:
		var p api.KnowledgeProgress
		if json.Unmarshal(payload, &p) != nil {
			return
		}
		r := &k.Status.Run
		for len(r.Units) <= p.Index {
			r.Units = append(r.Units, api.KnowledgeProgress{})
		}
		if p.Index >= 0 {
			r.Units[p.Index] = p
		}
	case api.TopicKnowledgeActivity:
		var a api.KnowledgeActivity
		if json.Unmarshal(payload, &a) != nil || a.Item != k.Peek || k.Peek == "" {
			return
		}
		k.fetchTranscript()
		return
	}
	k.Version++
}

// PeekAt opens the live transcript of a page ("" closes it).
func (k *Knowledge) PeekAt(id string) {
	k.Peek, k.Transcript = id, nil
	k.Version++
	if id != "" {
		k.fetchTranscript()
	}
}

// fetchTranscript loads the open transcript; requests during a fetch
// coalesce into one more fetch.
func (k *Knowledge) fetchTranscript() {
	if k.peekBusy {
		k.peekDirty = true
		return
	}
	id := k.Peek
	k.peekBusy = true
	k.c.Go(func(ctx context.Context) func() {
		var tr api.KnowledgeTranscript
		err := k.c.call(ctx, "knowledge.transcript", api.KnowledgeTranscriptParams{Item: id}, &tr)
		return func() {
			k.peekBusy = false
			if k.Peek == id {
				if err != nil {
					k.Transcript = []string{err.Error()}
				} else {
					k.Transcript = tr.Lines
				}
				k.Version++
			}
			if k.peekDirty && k.Peek != "" {
				k.peekDirty = false
				k.fetchTranscript()
			}
		}
	})
}

// refreshModel updates the model state while a pull runs or ended.
func (k *Knowledge) refreshModel() {
	k.c.Go(func(ctx context.Context) func() {
		var ms api.KnowledgeModelStatus
		err := k.c.call(ctx, "knowledge.modelStatus", nil, &ms)
		return func() {
			if err == nil {
				k.Model = ms
				k.Version++
			}
		}
	})
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
