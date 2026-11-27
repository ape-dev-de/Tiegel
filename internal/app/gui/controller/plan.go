package controller

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Plan is the plan mode state of the agent sidebar (D567): the current
// plan's status (plan.*), its changes and the transcript of the role the
// user looks at, kept up to date by plan.progress and plan.activity.
type Plan struct {
	c *Controller
	// Mode is the "Plan mode" checkbox; Available is false while the
	// planmode module does not run.
	Mode      bool
	Available bool
	// Status is the current plan (ID "" = none); Show displays the plan
	// view instead of the conversation.
	Status  api.PlanStatus
	Show    bool
	Changes []api.PlanChange
	// Summary is the summary section of the document (plan summary or
	// final report), without its heading.
	Summary string
	// Role is the role whose transcript is shown ("" = plan view).
	Role       string
	Transcript []api.AgentEntry
	// Version changes with every update.
	Version uint64

	project string
}

func newPlan(c *Controller) *Plan { return &Plan{c: c, Available: true} }

// Load fetches the current plan of the project.
func (p *Plan) Load() {
	if p.c.Project.Root == "" {
		return
	}
	p.c.Go(func(ctx context.Context) func() {
		var st api.PlanStatus
		err := p.c.call(ctx, "plan.status", nil, &st)
		cs, sum := p.details(ctx, st)
		return func() {
			p.Available = !disabledErr(err)
			if err == nil {
				p.Status, p.Changes, p.Summary = st, cs, sum
			}
			p.Version++
		}
	})
}

// ProjectChanged forgets the plan of the previous project.
func (p *Plan) ProjectChanged() {
	if p.c.Project.Root == p.project {
		return
	}
	p.project = p.c.Project.Root
	p.Status, p.Changes, p.Summary, p.Show, p.Role, p.Transcript = api.PlanStatus{}, nil, "", false, "", nil
	p.Version++
}

// details fetches the changes and the summary of plan id.
func (p *Plan) details(ctx context.Context, st api.PlanStatus) (cs []api.PlanChange, summary string) {
	if st.ID == "" {
		return nil, ""
	}
	if st.Workspace != nil {
		_ = p.c.call(ctx, "plan.changes", api.PlanIDParams{ID: st.ID}, &cs)
	}
	var doc api.PlanDocumentResult
	if p.c.call(ctx, "plan.document", api.PlanDocumentParams{ID: st.ID, Section: "summary"}, &doc) == nil {
		md := doc.Markdown
		if i := strings.Index(md, "\n"); strings.HasPrefix(md, "## ") && i >= 0 {
			md = md[i+1:]
		}
		summary = strings.TrimSpace(md)
	}
	return cs, summary
}

// call runs a plan method that returns the status and shows the plan.
func (p *Plan) call(method string, params any, then func()) {
	p.c.Go(func(ctx context.Context) func() {
		var st api.PlanStatus
		err := p.c.call(ctx, method, params, &st)
		cs, sum := p.details(ctx, st)
		return func() {
			if err != nil {
				p.c.Status.Notice = "Plan: " + err.Error()
				return
			}
			p.Status, p.Changes, p.Summary, p.Show = st, cs, sum, true
			p.Version++
			if then != nil {
				then()
			}
		}
	})
}

// Start starts a plan for goal (nothing changes before Accept).
func (p *Plan) Start(goal string) {
	p.Role, p.Transcript = "", nil
	p.call("plan.start", api.PlanStartParams{Goal: goal}, nil)
}

func (p *Plan) id() api.PlanIDParams { return api.PlanIDParams{ID: p.Status.ID} }

// Accept accepts the plan and starts its execution.
func (p *Plan) Accept() {
	p.call("plan.accept", api.PlanCommentParams{ID: p.Status.ID}, func() {
		p.call("plan.execute", api.PlanExecuteParams{ID: p.Status.ID}, nil)
	})
}

// RequestChanges revises the plan with comment.
func (p *Plan) RequestChanges(comment string) {
	p.call("plan.requestChanges", api.PlanCommentParams{ID: p.Status.ID, Comment: comment}, nil)
}

// Resolve decides a dispute and continues the execution.
func (p *Plan) Resolve(item, decision, comment string) {
	p.call("plan.resolve", api.PlanResolveParams{ID: p.Status.ID, Item: item, Decision: decision, Comment: comment}, func() {
		if len(p.Status.Disputes) == 0 {
			p.call("plan.execute", api.PlanExecuteParams{ID: p.Status.ID}, nil)
		}
	})
}

// Discard discards the plan (worktree and branch).
func (p *Plan) Discard() { p.call("plan.discard", p.id(), nil) }

// Apply applies the plan's changes to the working copy.
func (p *Plan) Apply() { p.call("plan.apply", p.id(), nil) }

// Keep keeps the plan branch.
func (p *Plan) Keep() { p.call("plan.keepBranch", p.id(), nil) }

// Cancel stops the running orchestration.
func (p *Plan) Cancel() { p.call("plan.cancelPlan", p.id(), nil) }

// Resume continues a cancelled plan.
func (p *Plan) Resume() { p.call("plan.resume", api.PlanExecuteParams{ID: p.Status.ID}, nil) }

// ShowRole loads the transcript of role ("" goes back to the plan view).
func (p *Plan) ShowRole(role string) {
	p.Role, p.Transcript = role, nil
	p.Version++
	if role == "" {
		return
	}
	p.c.Go(func(ctx context.Context) func() {
		var es []api.AgentEntry
		err := p.c.call(ctx, "plan.transcript", api.PlanTranscriptParams{ID: p.Status.ID, Role: role}, &es)
		return func() {
			if err == nil && p.Role == role {
				p.Transcript = es
				p.Version++
			}
		}
	})
}

// OpenDocument opens the working document in the editor.
func (p *Plan) OpenDocument() {
	if p.Status.Document != "" {
		p.c.OpenFile(p.Status.Document, nil)
	}
}

// OpenDiff shows a changed file before the plan and now.
func (p *Plan) OpenDiff(path string) {
	p.c.Go(func(ctx context.Context) func() {
		var d api.PlanDiff
		err := p.c.call(ctx, "plan.diff", api.PlanDiffParams{ID: p.Status.ID, Path: path}, &d)
		return func() {
			if err != nil {
				p.c.Status.Notice = "Diff: " + err.Error()
				return
			}
			p.c.Work.OpenDiff(DiffSpec{Path: d.Path, Title: d.Path + " (plan)",
				Texts: &DiffTexts{Left: d.Original, Right: d.Current, LeftLabel: "Before the plan", RightLabel: "Plan", Key: "plan:" + d.Path}})
		}
	})
}

// handleEvent applies plan.progress and plan.activity.
func (p *Plan) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicPlanProgress:
		var st api.PlanStatus
		if json.Unmarshal(payload, &st) != nil || (p.Status.ID != "" && st.ID != p.Status.ID) {
			return
		}
		changed := st.State != p.Status.State || st.Running != p.Status.Running
		p.Status = st
		if changed {
			p.c.Go(func(ctx context.Context) func() {
				cs, sum := p.details(ctx, st)
				return func() {
					if p.Status.ID == st.ID {
						p.Changes, p.Summary = cs, sum
						p.Version++
					}
				}
			})
		}
	case api.TopicPlanActivity:
		var a api.PlanActivity
		if json.Unmarshal(payload, &a) != nil || a.Plan != p.Status.ID || a.Role != p.Role {
			return
		}
		i := a.Entry.Index
		switch {
		case i < len(p.Transcript) && i >= 0 && len(p.Transcript) > 0 && p.Transcript[i].Index == i:
			p.Transcript[i] = a.Entry
		default:
			p.Transcript = append(p.Transcript, a.Entry)
		}
	}
	p.Version++
}
