package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Config are the plan mode settings.
type Config struct {
	// Project names the project in the base prompt.
	Project string
	// Subagents runs every role in its own session, independent roles in
	// parallel (MaxParallel); off runs all roles one after another in
	// one session per phase (critic and mediator still get a fresh,
	// sequential session).
	Subagents   bool
	MaxParallel int
	// Mediator is off, auto or always; MediatorRounds is the number of
	// rejected-and-insisted rounds before a dispute goes to the mediator
	// (auto) or the user (off).
	Mediator       string
	MediatorRounds int
	MaxIterations  int
	// MaxContextTokens is the budget of one role prompt.
	MaxContextTokens int
	// Models chooses a model per role ("" = the agent's).
	Models map[string]string
	// MCP offers the plan document tools to the sessions.
	MCP bool
	// Mutation runs mutation-lite when the changed lines are at most
	// MutationMaxLines, with at most MutationMax mutants.
	Mutation         bool
	MutationMaxLines int
	MutationMax      int
	// Coverage asks the test runner for changed-lines coverage.
	Coverage bool
	// Repairs is the number of retries for unusable answers.
	Repairs int
}

// Defaults fills unset values.
func (c Config) Defaults() Config {
	if c.MaxParallel <= 0 {
		c.MaxParallel = 2
	}
	switch c.Mediator {
	case api.MediatorOff, api.MediatorAuto, api.MediatorAlways:
	default:
		c.Mediator = api.MediatorAuto
	}
	if c.MediatorRounds <= 0 {
		c.MediatorRounds = 2
	}
	if c.MaxIterations <= 0 {
		c.MaxIterations = 5
	}
	if c.MaxContextTokens <= 0 {
		c.MaxContextTokens = 40000
	}
	if c.MutationMaxLines <= 0 {
		c.MutationMaxLines = 200
	}
	if c.MutationMax <= 0 {
		c.MutationMax = 8
	}
	if c.Repairs < 0 {
		c.Repairs = 0
	} else if c.Repairs == 0 {
		c.Repairs = 2
	}
	return c
}

// Deps are what the engine works with.
type Deps struct {
	Agent Agent
	Store *Store
	// Root is the project root (planning sessions, evidence checks).
	Root string
	// Workspace and Tests are needed by Execute.
	Workspace Workspace
	Tests     TestRunner
	Symbols   Symbols
	Now       func() time.Time
	// OnChange receives the state after every change.
	OnChange func(State)
}

// Engine runs the phases of one plan.
type Engine struct {
	cfg  Config
	deps Deps

	mu       sync.Mutex
	st       *State
	sessions map[string]Session
	lastRole map[string]string
}

// New creates an engine for st (a new or a loaded plan).
func New(cfg Config, deps Deps, st *State) *Engine {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	e := &Engine{cfg: cfg.Defaults(), deps: deps, st: st, sessions: map[string]Session{}, lastRole: map[string]string{}}
	if st.Mediator == "" {
		st.Mediator = e.cfg.Mediator
	}
	st.Subagents = e.cfg.Subagents
	st.Metrics.Mediator, st.Metrics.Subagents = e.cfg.Mediator, e.cfg.Subagents
	return e
}

// State returns a copy of the state.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.Clone()
}

// Update changes the state under the engine's lock and saves it.
func (e *Engine) Update(f func(st *State)) error {
	e.mu.Lock()
	f(e.st)
	e.mu.Unlock()
	return e.save()
}

// save persists the state and notifies.
func (e *Engine) save() error {
	e.mu.Lock()
	e.st.Doc.Updated = e.deps.Now()
	var err error
	if e.deps.Store != nil {
		err = e.deps.Store.Save(e.st)
		if e.deps.Agent != nil {
			// One transcript per role label: all its sessions in order.
			keys := make([]string, 0, len(e.sessions))
			for k := range e.sessions {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			byRole := map[string][]api.AgentEntry{}
			for _, k := range keys {
				byRole[sessionLabel(k)] = append(byRole[sessionLabel(k)], e.deps.Agent.Transcript(e.sessions[k].ID())...)
			}
			for role, es := range byRole {
				for i := range es {
					es[i].Index = i
				}
				_ = e.deps.Store.SaveTranscript(e.st.Doc.ID, role, es)
			}
		}
	}
	snap := e.st.Clone()
	e.mu.Unlock()
	if e.deps.OnChange != nil {
		e.deps.OnChange(snap)
	}
	return err
}

func sessionLabel(key string) string {
	if i := strings.IndexByte(key, '|'); i >= 0 {
		return key[:i]
	}
	return key
}

func (e *Engine) setPhase(state, phase string) {
	_ = e.Update(func(st *State) {
		if state != "" {
			st.Doc.Status = state
		}
		st.Doc.Phase = phase
	})
}

// ---- role calls ------------------------------------------------------------------

// call describes one role call.
type call struct {
	role string
	tmpl string
	// key selects the session ("" = fresh session closed afterwards).
	key   string
	mode  Mode
	dir   string
	first bool
	parts []Part
	task  string
	out   interface{ check() error }
}

func (e *Engine) roleState(role string, f func(r *api.PlanRole)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.st.Roles {
		if e.st.Roles[i].Role == role {
			f(&e.st.Roles[i])
			return
		}
	}
	e.st.Roles = append(e.st.Roles, api.PlanRole{Role: role, State: "idle"})
	f(&e.st.Roles[len(e.st.Roles)-1])
}

// session returns the session for c (creating it).
func (e *Engine) session(ctx context.Context, c call) (Session, bool, error) {
	key := c.key
	if key != "" && !e.cfg.Subagents {
		// One session per phase directory and mode for all roles.
		key = fmt.Sprintf("shared|%d|%s", c.mode, c.dir)
	}
	e.mu.Lock()
	s := e.sessions[key]
	last := e.lastRole[key]
	e.mu.Unlock()
	if key != "" && s != nil {
		s.SetRole(c.role, c.mode)
		e.mu.Lock()
		e.lastRole[key] = c.role
		e.mu.Unlock()
		return s, last != c.role, nil
	}
	spec := SessionSpec{Role: c.role, Dir: c.dir, Mode: c.mode, Model: e.cfg.Models[c.role], PlanID: e.st.Doc.ID, MCP: e.cfg.MCP}
	s, err := e.deps.Agent.NewSession(ctx, spec)
	if err != nil {
		return nil, false, err
	}
	if key != "" {
		e.mu.Lock()
		e.sessions[key] = s
		e.lastRole[key] = c.role
		e.mu.Unlock()
	} else {
		// Fresh sessions keep their transcript under the role name.
		e.mu.Lock()
		e.sessions[c.role+"|"+s.ID()] = s
		e.mu.Unlock()
	}
	return s, false, nil
}

// ask runs a role call: prompt, decode, repair.
func (e *Engine) ask(ctx context.Context, c call) error {
	if c.dir == "" {
		c.dir = e.deps.Root
	}
	s, switched, err := e.session(ctx, c)
	if err != nil {
		e.roleState(c.role, func(r *api.PlanRole) { r.State, r.Error = "failed", err.Error() })
		return err
	}
	prompt, err := RenderPrompt(PromptData{Template: c.tmpl, Project: e.cfg.Project, Dir: c.dir, ReadOnly: c.mode == ReadOnly,
		MCP: e.cfg.MCP, First: c.first, Switch: switched, Parts: c.parts, Budget: e.cfg.MaxContextTokens})
	if err != nil {
		return err
	}
	start := e.deps.Now()
	e.roleState(c.role, func(r *api.PlanRole) {
		r.State, r.Task, r.Started, r.Error, r.Session = "running", c.task, start, "", s.ID()
	})
	_ = e.save()
	finish := func(err error) {
		el := e.deps.Now().Sub(start).Milliseconds()
		e.roleState(c.role, func(r *api.PlanRole) {
			r.ElapsedMs, r.TotalMs = el, r.TotalMs+el
			r.Turns++
			r.State = "done"
			if err != nil {
				r.State, r.Error = "failed", err.Error()
				if errors.Is(err, context.Canceled) {
					r.State, r.Error = "idle", ""
				}
			}
		})
		e.mu.Lock()
		e.st.Metrics.DurationMs += el
		e.mu.Unlock()
	}
	text := prompt
	for attempt := 0; ; attempt++ {
		reply, err := s.Prompt(ctx, text)
		e.mu.Lock()
		e.st.Metrics.Turns++
		e.st.Metrics.PromptTokens += EstimateTokens(text)
		e.st.Metrics.ReplyTokens += EstimateTokens(reply)
		e.mu.Unlock()
		if err != nil {
			finish(err)
			return err
		}
		derr := decode(reply, c.out)
		if derr == nil {
			break
		}
		if attempt >= e.cfg.Repairs {
			err := fmt.Errorf("%s: unusable answer after %d repairs: %w", c.role, attempt, derr)
			finish(err)
			return err
		}
		e.mu.Lock()
		e.st.Metrics.Repairs++
		e.mu.Unlock()
		text = RepairPrompt(derr)
	}
	finish(nil)
	e.mergeInbox(c.role)
	if c.key == "" && e.deps.Agent != nil {
		s.Close()
	}
	return nil
}

// parallel runs fs, at most MaxParallel at a time with subagents, one
// after another without. The first error is returned after all ended.
func (e *Engine) parallel(ctx context.Context, fs ...func(context.Context) error) error {
	n := 1
	if e.cfg.Subagents {
		n = e.cfg.MaxParallel
	}
	sem := make(chan struct{}, n)
	errs := make([]error, len(fs))
	var wg sync.WaitGroup
	for i, f := range fs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, f func(context.Context) error) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = f(ctx)
		}(i, f)
		if n == 1 {
			wg.Wait()
		}
	}
	wg.Wait()
	return errors.Join(errs...)
}

// mergeInbox merges what the role proposed through the MCP tools.
func (e *Engine) mergeInbox(role string) {
	if e.deps.Store == nil {
		return
	}
	props := e.deps.Store.TakeInbox(e.st.Doc.ID, role)
	if len(props) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d := &e.st.Doc
	for _, p := range props {
		switch p.Op {
		case "finding":
			if strings.TrimSpace(p.Claim) != "" {
				AddFinding(d, api.PlanFinding{Claim: p.Claim, Evidence: p.Evidence, By: role + " (mcp)"})
			}
		case "comment":
			if it := Review(d, p.Item); it != nil {
				it.Responses = append(it.Responses, api.PlanResponse{Role: role, Verdict: "comment", Text: p.Content, Time: e.deps.Now()})
			}
		case "update":
			switch {
			case p.Section == SecContext && strings.HasPrefix(role, "planner"):
				d.Context = p.Content
			case p.Section == SecSummary && role == api.RoleSummarizer:
				d.Summary = p.Content
			default:
				e.st.Note(fmt.Sprintf("%s may not replace section %q (ignored)", role, p.Section))
			}
		}
	}
}

// ---- document parts ---------------------------------------------------------------

func (e *Engine) part(sec string, prio, min int) Part {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Part{Title: SectionTitle(sec), Body: SectionMarkdown(e.st.Doc, sec), Priority: prio, Min: min}
}

func itemsPart(title string, items []api.PlanReviewItem, prio int) Part {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "- %s [%s, from %s, %s] %s", it.ID, it.Severity, it.From, it.Status, oneLine(it.Claim))
		if it.Evidence != "" {
			fmt.Fprintf(&b, " — %s", it.Evidence)
		}
		b.WriteString("\n")
		if it.Suggestion != "" {
			fmt.Fprintf(&b, "  Suggestion: %s\n", oneLine(it.Suggestion))
		}
		for _, r := range it.Responses {
			fmt.Fprintf(&b, "  %s: %s %s %s\n", r.Role, r.Verdict, oneLine(r.Text), r.Evidence)
		}
		if it.Decision != nil {
			fmt.Fprintf(&b, "  DECIDED by %s: %s is right – %s\n", it.Decision.By, it.Decision.Winner, oneLine(it.Decision.Reason))
		}
	}
	return Part{Title: title, Body: b.String(), Priority: prio, Min: 400}
}

// ---- phase A: planning --------------------------------------------------------------

// Plan runs the planning phase up to the user's acceptance (resuming
// from the persisted phase).
func (e *Engine) Plan(ctx context.Context) error {
	e.mu.Lock()
	if e.st.Started.IsZero() {
		e.st.Started = e.deps.Now()
	}
	phase := e.st.Doc.Phase
	e.st.Error = ""
	e.mu.Unlock()
	order := []string{api.PhaseDraft, api.PhaseValidate, api.PhaseQAReview, api.PhaseSummary}
	i := slices.Index(order, phase)
	if i < 0 {
		i = 0
	}
	steps := []func(context.Context) error{e.draft, e.validate, e.qaReview, e.summarize}
	for ; i < len(order); i++ {
		e.setPhase(api.PlanDrafting, order[i])
		if err := steps[i](ctx); err != nil {
			return e.fail(err)
		}
		_ = e.save()
	}
	e.setPhase(api.PlanReview, api.PhaseAcceptance)
	return nil
}

// fail records err (cancel keeps the plan resumable).
func (e *Engine) fail(err error) error {
	_ = e.Update(func(st *State) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			st.Doc.Status = api.PlanCancelled
			st.Error = ""
			for i := range st.Roles {
				if st.Roles[i].State == "running" {
					st.Roles[i].State = "idle"
				}
			}
			return
		}
		st.Doc.Status = api.PlanFailed
		st.Error = err.Error()
	})
	return err
}

func (e *Engine) planCall(role, tmpl, task string, parts []Part, out interface{ check() error }) call {
	return call{role: role, tmpl: tmpl, key: role, mode: ReadOnly, dir: e.deps.Root, parts: parts, task: task, out: out}
}

func (e *Engine) draft(ctx context.Context) error {
	e.mu.Lock()
	drafted := len(e.st.Doc.Steps) > 0 || len(e.st.Doc.Criteria) > 0
	e.mu.Unlock()
	if drafted {
		return nil
	}
	goal := e.part(SecGoal, 100, 1<<30)
	comments := e.part(SecComments, 90, 2000)
	roles := []struct{ role, tmpl, task string }{
		{api.RolePlannerArch, TmplPlannerArch, "architecture, data flow, steps"},
		{api.RolePlannerCode, TmplPlannerCode, "affected code, edge cases"},
		{api.RolePlannerTests, TmplPlannerTests, "acceptance criteria, test plan"},
	}
	out := make([]Contribution, len(roles))
	var fs []func(context.Context) error
	for i, r := range roles {
		fs = append(fs, func(ctx context.Context) error {
			return e.ask(ctx, e.planCall(r.role, r.tmpl, r.task, []Part{goal, comments}, &out[i]))
		})
	}
	if err := e.parallel(ctx, fs...); err != nil {
		return err
	}
	e.mu.Lock()
	for i, r := range roles {
		e.merge(&out[i], r.role)
	}
	e.mu.Unlock()
	return nil
}

// merge adds a contribution to the document (callers hold mu).
func (e *Engine) merge(c *Contribution, by string) {
	d := &e.st.Doc
	if c.Context != "" {
		if d.Context != "" && by != api.RolePlannerArch {
			d.Context += "\n\n" + c.Context
		} else {
			d.Context = c.Context
		}
	}
	for _, r := range c.Refs {
		if !slices.Contains(d.Refs, r) {
			d.Refs = append(d.Refs, r)
		}
	}
	for _, f := range c.Findings {
		AddFinding(d, api.PlanFinding{Claim: f.Claim, Evidence: f.Evidence, By: by})
	}
	for _, s := range c.Steps {
		if old := Step(d, s.ID); s.ID != "" && old != nil {
			old.Title, old.Detail, old.Files = s.Title, s.Detail, s.Files
			continue
		}
		AddStep(d, api.PlanStep{Title: s.Title, Detail: s.Detail, Files: s.Files})
	}
	for _, cr := range c.Criteria {
		var ids []string
		for _, t := range cr.Tests {
			ids = append(ids, AddTest(d, api.PlanTest{Title: t.Title, Given: t.Given, When: t.When, Then: t.Then, File: t.File, Kind: t.Kind}))
		}
		AddCriterion(d, api.PlanCriterion{Text: cr.Text, Tests: ids})
	}
	for _, r := range c.Risks {
		AddRisk(d, api.PlanRisk{Text: r.Text, Kind: r.Kind})
	}
}

func (e *Engine) validate(ctx context.Context) error {
	e.mu.Lock()
	var todo []api.PlanFinding
	for i := range e.st.Doc.Findings {
		f := &e.st.Doc.Findings[i]
		if f.Status != api.FindingUnknown || strings.Contains(f.Note, "validated") {
			continue
		}
		todo = append(todo, *f)
	}
	e.mu.Unlock()
	if len(todo) == 0 {
		return nil
	}
	mech := map[string]string{}
	var list strings.Builder
	for _, f := range todo {
		st, note, fixed := VerifyEvidence(ctx, e.deps.Root, f.Evidence, f.Claim, e.deps.Symbols)
		if st == api.FindingRefuted {
			mech[f.ID] = note
		}
		fmt.Fprintf(&list, "- %s: %s — evidence: %s", f.ID, oneLine(f.Claim), orDash(f.Evidence))
		if note != "" {
			fmt.Fprintf(&list, " (Tiegel: %s)", note)
		}
		if fixed != "" {
			fmt.Fprintf(&list, " (maybe %s)", fixed)
		}
		list.WriteString("\n")
	}
	var v Validation
	err := e.ask(ctx, call{role: api.RoleValidator, tmpl: TmplValidator, mode: ReadOnly, dir: e.deps.Root, task: "check every finding against the code",
		parts: []Part{e.part(SecGoal, 100, 1<<30), {Title: "Findings to check", Body: list.String(), Priority: 90, Min: 2000}}, out: &v})
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	verdicts := map[string]Verdict{}
	for _, x := range v.Findings {
		verdicts[strings.ToUpper(x.ID)] = x
	}
	for _, t := range todo {
		f := Finding(&e.st.Doc, t.ID)
		x, ok := verdicts[strings.ToUpper(t.ID)]
		switch {
		case mech[t.ID] != "":
			f.Status, f.Note = api.FindingRefuted, "Tiegel: "+mech[t.ID]
			if ok && x.Note != "" {
				f.Note += "; validator: " + x.Note
			}
		case ok:
			f.Status, f.Note = x.Status, x.Note
			if x.Evidence != "" && x.Evidence != f.Evidence {
				// The corrected evidence must exist too.
				if st, _, _ := VerifyEvidence(ctx, e.deps.Root, x.Evidence, f.Claim, nil); st != api.FindingRefuted {
					f.Evidence = x.Evidence
				}
			}
		default:
			f.Note = "not validated"
		}
		if f.Note == "" {
			f.Note = "validated"
		}
	}
	return nil
}

func (e *Engine) qaReview(ctx context.Context) error {
	var q QAReview
	parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecComments, 95, 2000), e.part(SecPlan, 90, 3000), e.part(SecTests, 90, 3000),
		e.part(SecCriteria, 90, 2000), e.part(SecFindings, 50, 1500), e.part(SecRisks, 60, 1000), e.part(SecContext, 40, 1000)}
	if err := e.ask(ctx, e.planCall("qa-review", TmplQAReview, "challenge plan and test plan", parts, &q)); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d := &e.st.Doc
	for _, is := range q.Issues {
		claim := is.Claim
		if is.Section != "" {
			claim = "(" + is.Section + ") " + claim
		}
		AddReview(d, api.PlanReviewItem{From: api.RoleQA, To: api.RoleSummarizer, Target: api.TargetPlan, Severity: is.Severity,
			Claim: claim, Evidence: is.Evidence, Suggestion: is.Suggestion})
		e.st.Metrics.FindingsRaised++
	}
	e.merge(&Contribution{Criteria: q.Criteria, Risks: q.Risks}, api.RoleQA)
	return nil
}

func (e *Engine) summarize(ctx context.Context) error {
	e.mu.Lock()
	var open []api.PlanReviewItem
	for _, it := range e.st.Doc.Review {
		if it.Target == api.TargetPlan && IsOpen(it) {
			open = append(open, it)
		}
	}
	e.mu.Unlock()
	var s Summary
	parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecComments, 95, 2000), e.part(SecPlan, 90, 3000), e.part(SecTests, 80, 2000),
		e.part(SecCriteria, 85, 2000), e.part(SecFindings, 50, 1500), e.part(SecRisks, 70, 1000), itemsPart("Open review items", open, 95)}
	if err := e.ask(ctx, e.planCall(api.RoleSummarizer, TmplSummary, "summary for the user", parts, &s)); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d := &e.st.Doc
	d.Summary = s.Summary
	for _, r := range s.Resolutions {
		if it := Review(d, r.Item); it != nil && it.Target == api.TargetPlan {
			it.Responses = append(it.Responses, api.PlanResponse{Role: api.RoleSummarizer, Verdict: "resolved", Text: r.Resolution, Time: e.deps.Now()})
			it.Status = api.ReviewResolved
		}
	}
	for _, c := range s.Criteria {
		var ids []string
		for _, t := range c.Tests {
			if tc := TestCase(d, t); tc != nil {
				ids = append(ids, tc.ID)
			}
		}
		if len(ids) > 0 {
			AddCriterion(d, api.PlanCriterion{Text: c.Text, Tests: ids})
		}
	}
	// Mechanical: every criterion maps to at least one existing test.
	var unmapped []string
	for _, c := range d.Criteria {
		ok := false
		for _, t := range c.Tests {
			ok = ok || TestCase(d, t) != nil
		}
		if !ok {
			unmapped = append(unmapped, c.ID)
		}
	}
	g := api.PlanGate{Name: "criteria", Passed: len(unmapped) == 0 && len(d.Criteria) > 0, Summary: fmt.Sprintf("%d criteria, each mapped to a test case", len(d.Criteria))}
	if len(d.Criteria) == 0 {
		g.Summary = "no acceptance criteria"
	}
	if len(unmapped) > 0 {
		g.Summary = "criteria without a test case: " + strings.Join(unmapped, ", ")
		AddRisk(d, api.PlanRisk{Kind: "question", Text: "Acceptance criteria without a test case: " + strings.Join(unmapped, ", ")})
	}
	setGate(d, g)
	return nil
}

func setGate(d *api.PlanDocument, g api.PlanGate) {
	for i := range d.Gates {
		if d.Gates[i].Name == g.Name {
			d.Gates[i] = g
			return
		}
	}
	d.Gates = append(d.Gates, g)
}

// Revise runs a revision round after the user requested changes.
func (e *Engine) Revise(ctx context.Context, comment string) error {
	_ = e.Update(func(st *State) {
		st.Doc.Comments = append(st.Doc.Comments, api.PlanComment{Text: comment, Time: e.deps.Now()})
		st.Doc.Status, st.Doc.Phase, st.Error = api.PlanDrafting, api.PhaseDraft, ""
	})
	var c Contribution
	parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecComments, 100, 1<<30), e.part(SecContext, 40, 1500), e.part(SecFindings, 50, 1500),
		e.part(SecPlan, 90, 3000), e.part(SecTests, 85, 3000), e.part(SecCriteria, 85, 2000), e.part(SecRisks, 60, 1000), e.part(SecReview, 30, 1000)}
	if err := e.ask(ctx, e.planCall(api.RolePlannerArch, TmplRevise, "revise after the change request", parts, &c)); err != nil {
		return e.fail(err)
	}
	e.mu.Lock()
	d := &e.st.Doc
	for _, sec := range c.Replace {
		switch sec {
		case SecContext:
			d.Context = ""
		case SecFindings:
			d.Findings = nil
		case SecPlan:
			keep := d.Steps
			d.Steps = nil
			for _, s := range c.Steps {
				if s.ID != "" {
					for _, o := range keep {
						if strings.EqualFold(o.ID, s.ID) {
							d.Steps = append(d.Steps, api.PlanStep{ID: o.ID, Title: s.Title, Detail: s.Detail, Files: s.Files, Status: "open"})
						}
					}
				}
			}
			var rest []StepIn
			for _, s := range c.Steps {
				if Step(d, s.ID) == nil || s.ID == "" {
					rest = append(rest, s)
				}
			}
			c.Steps = rest
		case SecTests, SecCriteria:
			if len(c.Criteria) > 0 {
				d.Tests, d.Criteria = nil, nil
			}
		case SecRisks:
			d.Risks = nil
		}
	}
	e.merge(&c, api.RolePlannerArch)
	e.st.Doc.Phase = api.PhaseValidate
	e.mu.Unlock()
	_ = e.save()
	return e.Plan(ctx)
}

// ---- phase B: execution --------------------------------------------------------------

// Execute runs (or resumes) the execution after the user accepted the
// plan. It returns when the plan is done, incomplete (iteration limit),
// or needs the user's decision on a dispute.
func (e *Engine) Execute(ctx context.Context) error {
	ws := e.deps.Workspace
	if ws == nil || e.deps.Tests == nil {
		return errors.New("orchestrate: execute needs a workspace and a test runner")
	}
	info := ws.Info()
	_ = e.Update(func(st *State) {
		st.Workspace = &info
		if info.Note != "" {
			st.Note(info.Note)
		}
		// An iteration interrupted before its gates ran is repeated.
		if (st.Doc.Status == api.PlanCancelled || st.Doc.Status == api.PlanFailed) && st.Iteration > 0 &&
			st.Doc.Phase != api.PhaseDebate && st.Doc.Phase != api.PhaseFinal {
			st.Iteration--
		}
		st.Doc.Status, st.Error = api.PlanExecuting, ""
		if st.Started.IsZero() {
			st.Started = e.deps.Now()
		}
	})
	e.applyDecisions()
	for {
		e.mu.Lock()
		iter := e.st.Iteration
		e.mu.Unlock()
		if iter >= e.cfg.MaxIterations {
			break
		}
		iter++
		_ = e.Update(func(st *State) { st.Iteration, st.Metrics.Iterations = iter, iter })
		e.setPhase("", api.PhaseImplement)
		if err := e.workTurns(ctx, iter == 1); err != nil {
			return e.fail(err)
		}
		if err := ws.Checkpoint(ctx, fmt.Sprintf("plan %s: iteration %d", e.st.Doc.ID, iter)); err != nil {
			e.note("checkpoint: " + err.Error())
		}
		changes, err := ws.Changes(ctx)
		if err != nil {
			return e.fail(err)
		}
		e.setPhase("", api.PhaseCritic)
		if err := e.critics(ctx, changes); err != nil {
			return e.fail(err)
		}
		e.setPhase("", api.PhaseRate)
		if err := e.crossRate(ctx, changes); err != nil {
			return e.fail(err)
		}
		e.setPhase("", api.PhaseGates)
		if err := e.gates(ctx, changes); err != nil {
			return e.fail(err)
		}
		e.setPhase("", api.PhaseDebate)
		if err := e.debate(ctx); err != nil {
			return e.fail(err)
		}
		waiting := false
		_ = e.Update(func(st *State) {
			if waiting = len(Disputes(&st.Doc)) > 0; waiting {
				st.Doc.Status = api.PlanNeedsUser
			}
		})
		if waiting {
			return nil
		}
		if e.done() {
			break
		}
	}
	return e.final(ctx)
}

func (e *Engine) note(n string) {
	_ = e.Update(func(st *State) { st.Note(n) })
}

func (e *Engine) execCall(role, tmpl, task string, first bool, parts []Part, out interface{ check() error }) call {
	return call{role: role, tmpl: tmpl, key: role, mode: Execute, dir: e.deps.Workspace.Dir(), first: first, parts: parts, task: task, out: out}
}

// workTurns lets DEV implement and QA write tests (in parallel with
// subagents).
func (e *Engine) workTurns(ctx context.Context, first bool) error {
	e.mu.Lock()
	devItems := OpenFor(&e.st.Doc, api.RoleDev)
	qaItems := OpenFor(&e.st.Doc, api.RoleQA)
	stepsOpen := false
	for _, s := range e.st.Doc.Steps {
		stepsOpen = stepsOpen || s.Status != "done"
	}
	failures := strings.Join(e.st.Failures, "\n")
	e.mu.Unlock()
	var fs []func(context.Context) error
	var dev DevResult
	var qa QAResult
	if first || len(devItems) > 0 || stepsOpen {
		parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecPlan, 95, 4000), e.part(SecCriteria, 80, 2000),
			itemsPart("Open review items for you", devItems, 96), {Title: "Failing tests", Body: failures, Priority: 94, Min: 1500},
			e.part(SecFindings, 40, 1000), e.part(SecContext, 30, 1000), e.part(SecDecisions, 60, 800)}
		fs = append(fs, func(ctx context.Context) error {
			if err := e.ask(ctx, e.execCall(api.RoleDev, TmplDev, "implement the plan", first, parts, &dev)); err != nil {
				return err
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			for _, r := range dev.Steps {
				if s := Step(&e.st.Doc, r.ID); s != nil {
					s.Status, s.Note = r.Status, r.Note
				}
			}
			e.respond(api.RoleDev, dev.Responses)
			return nil
		})
	}
	if first || len(qaItems) > 0 {
		parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecTests, 96, 4000), e.part(SecCriteria, 95, 2000),
			itemsPart("Open review items for you", qaItems, 97), e.part(SecDecisions, 60, 800)}
		fs = append(fs, func(ctx context.Context) error {
			if err := e.ask(ctx, e.execCall(api.RoleQA, TmplQA, "write the tests of the test plan", first, parts, &qa)); err != nil {
				return err
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			for _, m := range qa.Tests {
				if tc := TestCase(&e.st.Doc, m.Case); tc != nil {
					tc.Tests = m.Tests
					if m.File != "" {
						tc.File = m.File
					}
				}
			}
			e.respond(api.RoleQA, qa.Responses)
			return nil
		})
	}
	if len(fs) == 0 {
		return nil
	}
	err := e.parallel(ctx, fs...)
	_ = e.save()
	return err
}

// respond applies a role's answers to review items (callers hold mu).
func (e *Engine) respond(role string, rs []Response) {
	d := &e.st.Doc
	now := e.deps.Now()
	for _, r := range rs {
		it := Review(d, r.Item)
		if it == nil || it.To != role || !IsOpen(*it) {
			continue
		}
		it.Responses = append(it.Responses, api.PlanResponse{Role: role, Verdict: r.Verdict, Text: r.Text, Evidence: r.Evidence, Round: it.Round, Time: now})
		decidedAgainst := it.Decision != nil && it.Decision.Winner != role
		switch r.Verdict {
		case "accept":
			it.Status = api.ReviewAccepted
			e.st.Metrics.FindingsAccepted++
		case "reject":
			e.st.Metrics.FindingsRejected++
			switch {
			case decidedAgainst:
				// Decided: the role has to act; the item stays open.
			case it.Mechanical:
				// A gate cannot concede: the mediator or the user decides.
				it.Status = api.ReviewDisputed
			default:
				it.Status = api.ReviewRejected
			}
		case "needs-info":
			it.Status = api.ReviewNeedsInfo
		}
	}
}

// raiser is the role that defends an item in the debate: its author, or
// for the critic and the test runner the senior role of the other side.
func raiser(it api.PlanReviewItem) string {
	switch it.From {
	case api.RoleDev, api.RoleQA:
		return it.From
	}
	if it.Mechanical {
		return ""
	}
	if it.Target == api.TargetTests {
		return api.RoleDev
	}
	return api.RoleQA
}

// changesPart renders the diff of the changes (tests or code).
func changesPart(title string, changes []FileChange, tests bool, prio int) Part {
	var b strings.Builder
	for _, c := range changes {
		if c.Test != tests {
			continue
		}
		b.WriteString(c.Unified())
	}
	if b.Len() == 0 {
		b.WriteString("(no changes)")
	}
	return Part{Title: title, Body: b.String(), Priority: prio, Min: 3000}
}

func (e *Engine) addIssues(from string, issues []Issue) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := &e.st.Doc
	for _, is := range issues {
		to := api.RoleDev
		if is.Target == api.TargetTests {
			to = api.RoleQA
		}
		dup := false
		for _, it := range d.Review {
			dup = dup || IsOpen(it) && it.To == to && strings.EqualFold(oneLine(it.Claim), oneLine(is.Claim))
		}
		if dup {
			continue
		}
		AddReview(d, api.PlanReviewItem{From: from, To: to, Target: is.Target, Severity: is.Severity, Claim: is.Claim,
			Evidence: is.Evidence, Suggestion: is.Suggestion})
		e.st.Metrics.FindingsRaised++
	}
}

// critics run the mandatory fresh-session critic after the work turns:
// one for the implementation, one for the tests.
func (e *Engine) critics(ctx context.Context, changes []FileChange) error {
	var ri, rt Rating
	dir := e.deps.Workspace.Dir()
	impl := func(ctx context.Context) error {
		parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecPlan, 90, 3000), e.part(SecCriteria, 85, 2000), changesPart("Diff of the implementation", changes, false, 80)}
		if err := e.ask(ctx, call{role: api.RoleCritic, tmpl: TmplCritic, mode: ReadOnly, dir: dir, task: "review the implementation", parts: parts, out: &ri}); err != nil {
			return err
		}
		e.addIssues(api.RoleCritic, ri.Issues)
		return nil
	}
	tests := func(ctx context.Context) error {
		parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecTests, 90, 3000), e.part(SecCriteria, 85, 2000), changesPart("Diff of the tests", changes, true, 80)}
		if err := e.ask(ctx, call{role: api.RoleCritic, tmpl: TmplCritic, mode: ReadOnly, dir: dir, task: "review the tests", parts: parts, out: &rt}); err != nil {
			return err
		}
		e.addIssues(api.RoleCritic, rt.Issues)
		return nil
	}
	err := e.parallel(ctx, impl, tests)
	_ = e.save()
	return err
}

// crossRate lets QA rate the implementation and DEV the tests; both
// answer the rejected items they defend.
func (e *Engine) crossRate(ctx context.Context, changes []FileChange) error {
	defended := func(role string) []api.PlanReviewItem {
		e.mu.Lock()
		defer e.mu.Unlock()
		var out []api.PlanReviewItem
		for _, it := range e.st.Doc.Review {
			if (it.Status == api.ReviewRejected || it.Status == api.ReviewNeedsInfo) && raiser(it) == role {
				out = append(out, it)
			}
		}
		return out
	}
	rate := func(role, tmpl string, tests bool) func(context.Context) error {
		return func(ctx context.Context) error {
			var r Rating
			parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecCriteria, 85, 2000)}
			if tests {
				parts = append(parts, e.part(SecTests, 90, 3000), changesPart("Diff of the tests", changes, true, 80))
			} else {
				parts = append(parts, e.part(SecPlan, 90, 3000), changesPart("Diff of the implementation", changes, false, 80))
			}
			parts = append(parts, itemsPart("Rejected items you defend", defended(role), 95))
			if err := e.ask(ctx, e.execCall(role, tmpl, "rate the other role's work", false, parts, &r)); err != nil {
				return err
			}
			e.addIssues(role, r.Issues)
			e.rebut(role, r.Rebuttals)
			return nil
		}
	}
	err := e.parallel(ctx, rate(api.RoleQA, TmplRateImpl, false), rate(api.RoleDev, TmplRateTests, true))
	_ = e.save()
	return err
}

// rebut applies the defender's answers to rejected items.
func (e *Engine) rebut(role string, rs []Rebuttal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := &e.st.Doc
	now := e.deps.Now()
	counted := false
	for _, r := range rs {
		it := Review(d, r.Item)
		if it == nil || raiser(*it) != role || (it.Status != api.ReviewRejected && it.Status != api.ReviewNeedsInfo) {
			continue
		}
		it.Responses = append(it.Responses, api.PlanResponse{Role: role, Verdict: r.Verdict, Text: r.Text, Round: it.Round, Time: now})
		if !counted {
			e.st.Metrics.Rounds++
			counted = true
		}
		if r.Verdict == "withdraw" {
			it.Status = api.ReviewWithdrawn
			continue
		}
		if it.Status == api.ReviewNeedsInfo {
			it.Status = api.ReviewOpen // the info was given
			continue
		}
		it.Round++
		switch {
		case e.cfg.Mediator == api.MediatorAlways, it.Round >= e.cfg.MediatorRounds:
			it.Status = api.ReviewDisputed
		default:
			it.Status = api.ReviewOpen
		}
	}
}

// debate sends disputes to the mediator (auto, always); without one they
// wait for the user.
func (e *Engine) debate(ctx context.Context) error {
	e.mu.Lock()
	disputes := Disputes(&e.st.Doc)
	e.mu.Unlock()
	if len(disputes) == 0 || e.cfg.Mediator == api.MediatorOff {
		return nil
	}
	var m Mediation
	parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecCriteria, 80, 2000), itemsPart("Disputed items", disputes, 99)}
	if err := e.ask(ctx, call{role: api.RoleMediator, tmpl: TmplMediator, mode: ReadOnly, dir: e.deps.Workspace.Dir(), task: "decide disputes", parts: parts, out: &m}); err != nil {
		return err
	}
	e.mu.Lock()
	for _, r := range m.Decisions {
		it := Review(&e.st.Doc, r.Item)
		if it == nil || it.Status != api.ReviewDisputed {
			continue
		}
		it.Decision = &api.PlanDecision{By: api.RoleMediator, Winner: r.Winner, Reason: r.Reason, Time: e.deps.Now()}
		e.st.Metrics.MediatorDecisions++
		AddDecision(&e.st.Doc, fmt.Sprintf("%s: %s is right – %s", it.ID, r.Winner, r.Reason), api.RoleMediator, e.deps.Now())
	}
	e.mu.Unlock()
	e.applyDecisions()
	return e.save()
}

// applyDecisions turns decided disputes into work: the addressee wins →
// withdrawn; otherwise the item is open again and has to be fixed.
func (e *Engine) applyDecisions() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.st.Doc.Review {
		it := &e.st.Doc.Review[i]
		if it.Status != api.ReviewDisputed || it.Decision == nil {
			continue
		}
		switch it.Decision.Winner {
		case it.To:
			it.Status = api.ReviewWithdrawn
		case "":
			it.Status = api.ReviewOpen
		default:
			it.Status = api.ReviewOpen
		}
	}
}

// Resolve records the user's decision of a review item.
func (e *Engine) Resolve(item, decision, comment string) error {
	var err error
	_ = e.Update(func(st *State) {
		it := Review(&st.Doc, item)
		if it == nil {
			err = fmt.Errorf("no review item %q", item)
			return
		}
		now := e.deps.Now()
		st.Metrics.UserOverrides++
		switch decision {
		case api.RoleDev, api.RoleQA:
			it.Decision = &api.PlanDecision{By: "user", Winner: decision, Reason: comment, Time: now}
			it.Status = api.ReviewDisputed
			AddDecision(&st.Doc, fmt.Sprintf("%s: %s is right (user) %s", it.ID, decision, comment), "user", now)
		case "dismiss":
			it.Decision = &api.PlanDecision{By: "user", Reason: "dismissed " + comment, Time: now}
			it.Status = api.ReviewWithdrawn
		case "comment":
			it.Responses = append(it.Responses, api.PlanResponse{Role: "user", Verdict: "comment", Text: comment, Time: now})
			it.Status = api.ReviewOpen
		default:
			err = fmt.Errorf("decision must be dev, qa, dismiss or comment")
			st.Metrics.UserOverrides--
		}
		if st.Doc.Status == api.PlanNeedsUser && len(Disputes(&st.Doc)) == 0 {
			st.Doc.Status = api.PlanExecuting
		}
	})
	e.applyDecisions()
	_ = e.save()
	return err
}

// ---- gates ---------------------------------------------------------------------------

// testFiles are the test files to run: changed tests and the test plan's
// target files that exist.
func (e *Engine) testFiles(changes []FileChange) []string {
	var out []string
	for _, c := range changes {
		if c.Test && c.Kind != api.ChangeDeleted && strings.HasSuffix(c.Path, ".php") && !slices.Contains(out, c.Path) {
			out = append(out, c.Path)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.st.Doc.Tests {
		if t.File != "" && !slices.Contains(out, t.File) && fileExists(e.deps.Workspace.Dir(), t.File) {
			out = append(out, t.File)
		}
	}
	slices.Sort(out)
	return out
}

// reconcile replaces the open items of gate with issues: still detected
// items stay (accepted ones reopen), new ones are added, the rest are
// resolved.
func (e *Engine) reconcile(gate string, issues []GateIssue) {
	d := &e.st.Doc
	from := "gate:" + gate
	if gate == "runner" {
		from = "runner"
	}
	seen := map[string]bool{}
	for _, is := range issues {
		it := is.item()
		it.From = from
		if gate == "runner" {
			it.Mechanical = false
		}
		key := it.Claim
		seen[key] = true
		found := false
		for i := range d.Review {
			x := &d.Review[i]
			if x.From == from && x.Claim == key {
				found = true
				if x.Status == api.ReviewAccepted || x.Status == api.ReviewResolved {
					x.Status = api.ReviewOpen
				}
				x.Evidence = it.Evidence
			}
		}
		if !found {
			AddReview(d, it)
			e.st.Metrics.FindingsRaised++
		}
	}
	for i := range d.Review {
		x := &d.Review[i]
		if x.From == from && !seen[x.Claim] && x.Status != api.ReviewWithdrawn && x.Status != api.ReviewDisputed {
			x.Status = api.ReviewResolved
		}
	}
}

func (e *Engine) gates(ctx context.Context, changes []FileChange) error {
	ws := e.deps.Workspace
	files := e.testFiles(changes)
	rep, err := e.deps.Tests.Run(ctx, TestRequest{Dir: ws.Dir(), Files: files, Coverage: e.cfg.Coverage})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		rep.Error = err.Error()
	}
	e.mu.Lock()
	e.st.Metrics.TestRuns++
	d := &e.st.Doc
	// Test results per test case.
	var runner []GateIssue
	var fails []string
	for _, f := range rep.Failed() {
		fails = append(fails, f.Compact())
		ev := f.File
		if f.FailFile != "" {
			ev = fmt.Sprintf("%s:%d", f.FailFile, f.FailLine)
		}
		runner = append(runner, GateIssue{To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityBlocker,
			Claim: "test " + f.ID + " fails: " + shorten(oneLine(f.Message), 300), Evidence: ev,
			Suggest: "fix the implementation – or reject with evidence if the test is wrong (QA then answers)"})
	}
	e.st.Failures = fails
	for i := range d.Tests {
		t := &d.Tests[i]
		switch {
		case len(t.Tests) == 0:
			t.Status = "missing"
		default:
			t.Status = TestPassed
			for _, id := range t.Tests {
				rs := rep.Find(id)
				if len(rs) == 0 && t.Status == TestPassed {
					t.Status = "missing"
				}
				for _, r := range rs {
					if r.Status == TestFailed {
						t.Status = TestFailed
					}
				}
			}
		}
	}
	tg := api.PlanGate{Name: "tests", Passed: rep.Green(), Summary: fmt.Sprintf("%d tests, %d failed", len(rep.Tests), len(rep.Failed()))}
	if rep.Error != "" {
		tg.Passed, tg.Summary = false, "the tests could not run: "+shorten(oneLine(rep.Error), 300)
		runner = append(runner, GateIssue{To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityBlocker,
			Claim: "the test run failed: " + shorten(oneLine(rep.Error), 300), Suggest: "make the tests runnable (syntax errors, missing classes)"})
	}
	if tg.Passed && e.st.Metrics.IterationsToGreen == 0 {
		e.st.Metrics.IterationsToGreen = e.st.Iteration
	}
	setGate(d, tg)
	e.reconcile("runner", runner)
	// Hollow tests.
	var hollow []GateIssue
	for _, c := range changes {
		if c.Test && c.Kind != api.ChangeDeleted && strings.HasSuffix(c.Path, ".php") {
			hollow = append(hollow, HollowTests(c.Path, c.New, c.AddedLines())...)
		}
	}
	e.reconcile("hollow", hollow)
	setGate(d, api.PlanGate{Name: "hollow", Passed: len(hollow) == 0, Summary: fmt.Sprintf("%d hollow tests", len(hollow))})
	// Unfinished work.
	unf := append(Unfinished(changes), StepsDone(d.Steps, changes)...)
	e.reconcile("unfinished", unf)
	setGate(d, api.PlanGate{Name: "unfinished", Passed: len(unf) == 0, Summary: fmt.Sprintf("%d unfinished items", len(unf))})
	// Coverage.
	if e.cfg.Coverage {
		switch {
		case rep.Coverage == nil:
			note := rep.CoverageNote
			if note == "" {
				note = "no coverage data"
			}
			setGate(d, api.PlanGate{Name: "coverage", Skipped: note})
		default:
			unc := Uncovered(changes, rep.Coverage)
			e.reconcile("coverage", unc)
			setGate(d, api.PlanGate{Name: "coverage", Passed: len(unc) == 0, Summary: fmt.Sprintf("%d files with unexecuted changed lines", len(unc))})
		}
	}
	green := rep.Green()
	e.mu.Unlock()

	if err := e.redCheck(ctx, changes); err != nil {
		return err
	}
	if err := e.mutation(ctx, changes, files, green); err != nil {
		return err
	}
	e.mu.Lock()
	e.criteriaGate()
	e.mu.Unlock()
	return e.save()
}

// criteriaGate marks the criteria whose mapped tests all passed (callers
// hold mu).
func (e *Engine) criteriaGate() {
	d := &e.st.Doc
	var unmet []string
	for i := range d.Criteria {
		c := &d.Criteria[i]
		met := len(c.Tests) > 0
		for _, id := range c.Tests {
			t := TestCase(d, id)
			met = met && t != nil && t.Status == TestPassed
		}
		c.Met = met
		if !met {
			unmet = append(unmet, c.ID)
		}
	}
	g := api.PlanGate{Name: "criteria", Passed: len(unmet) == 0 && len(d.Criteria) > 0, Summary: fmt.Sprintf("%d of %d criteria met", len(d.Criteria)-len(unmet), len(d.Criteria))}
	if len(unmet) > 0 {
		g.Summary += "; not met: " + strings.Join(unmet, ", ")
	}
	setGate(d, g)
}

// redCheck runs QA's tests for new behaviour against the base code:
// they must fail there.
func (e *Engine) redCheck(ctx context.Context, changes []FileChange) error {
	ws := e.deps.Workspace
	e.mu.Lock()
	var cases []api.PlanTest
	for _, t := range e.st.Doc.Tests {
		if t.Kind != "regression" && len(t.Tests) > 0 {
			cases = append(cases, t)
		}
	}
	e.mu.Unlock()
	var tests []FileChange
	for _, c := range changes {
		if c.Test && c.Kind != api.ChangeDeleted && strings.HasSuffix(c.Path, ".php") {
			tests = append(tests, c)
		}
	}
	if len(cases) == 0 || len(tests) == 0 {
		e.mu.Lock()
		setGate(&e.st.Doc, api.PlanGate{Name: "red", Skipped: "no new tests yet"})
		e.mu.Unlock()
		return nil
	}
	base, err := ws.BaseDir(ctx)
	if err != nil {
		e.mu.Lock()
		setGate(&e.st.Doc, api.PlanGate{Name: "red", Skipped: err.Error()})
		e.mu.Unlock()
		return nil
	}
	defer func() { _ = ws.ResetBase(context.WithoutCancel(ctx)) }()
	var files []string
	for _, t := range tests {
		if err := writeFile(base, t.Path, t.New); err != nil {
			return err
		}
		files = append(files, t.Path)
	}
	rep, err := e.deps.Tests.Run(ctx, TestRequest{Dir: base, Files: files})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		rep.Error = err.Error()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.st.Metrics.TestRuns++
	var issues []GateIssue
	for _, c := range cases {
		for _, id := range c.Tests {
			for _, r := range rep.Find(id) {
				if r.Status == TestPassed {
					issues = append(issues, GateIssue{Gate: "red", To: api.RoleQA, Target: api.TargetTests, Severity: api.SeverityBlocker,
						Claim: fmt.Sprintf("test %s (%s) passes on the base code: it does not test the change", r.ID, c.ID), Evidence: c.File,
						Suggest: "assert the new behaviour so the test fails without the change (or mark the case as regression in a change request)"})
				}
			}
		}
	}
	e.reconcile("red", issues)
	summary := fmt.Sprintf("%d new tests checked against the base code", len(rep.Tests))
	if rep.Error != "" && len(rep.Tests) == 0 {
		summary = "the new tests do not run on the base code (counts as failing)"
	}
	setGate(&e.st.Doc, api.PlanGate{Name: "red", Passed: len(issues) == 0, Summary: summary})
	return nil
}

// mutation runs mutation-lite on small, green changes.
func (e *Engine) mutation(ctx context.Context, changes []FileChange, files []string, green bool) error {
	if !e.cfg.Mutation {
		return nil
	}
	lines := 0
	for _, c := range changes {
		if !c.Test {
			a, r := c.Stat()
			lines += a + r
		}
	}
	skip := ""
	switch {
	case !green:
		skip = "tests are not green yet"
	case lines > e.cfg.MutationMaxLines:
		skip = fmt.Sprintf("%d changed lines (limit %d)", lines, e.cfg.MutationMaxLines)
	case len(files) == 0:
		skip = "no tests"
	}
	ms := Mutants(changes, e.cfg.MutationMax)
	if skip == "" && len(ms) == 0 {
		skip = "nothing to mutate"
	}
	if skip != "" {
		e.mu.Lock()
		setGate(&e.st.Doc, api.PlanGate{Name: "mutation", Skipped: skip})
		e.mu.Unlock()
		return nil
	}
	dir := e.deps.Workspace.Dir()
	var issues []GateIssue
	killed := 0
	for _, m := range ms {
		orig, err := readFile(dir, m.Path)
		if err != nil {
			continue
		}
		if err := writeFile(dir, m.Path, m.Src); err != nil {
			return err
		}
		rep, rerr := e.deps.Tests.Run(ctx, TestRequest{Dir: dir, Files: files})
		if werr := writeFile(dir, m.Path, orig); werr != nil {
			return werr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e.mu.Lock()
		e.st.Metrics.TestRuns++
		e.mu.Unlock()
		if rerr == nil && rep.Green() {
			issues = append(issues, GateIssue{Gate: "mutation", To: api.RoleQA, Target: api.TargetTests, Severity: api.SeverityMajor,
				Claim: fmt.Sprintf("mutation survived (%s at %s:%d): no test notices", m.What, m.Path, m.Line), Evidence: fmt.Sprintf("%s:%d", m.Path, m.Line),
				Suggest: "add an assertion that fails when this line changes"})
		} else {
			killed++
		}
	}
	e.mu.Lock()
	e.reconcile("mutation", issues)
	setGate(&e.st.Doc, api.PlanGate{Name: "mutation", Passed: len(issues) == 0, Summary: fmt.Sprintf("%d of %d mutants killed", killed, len(ms))})
	e.mu.Unlock()
	return nil
}

// done tells whether the plan is finished: tests and criteria green, no
// failed gate, no open blocker or major item, all steps done.
func (e *Engine) done() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := &e.st.Doc
	for _, g := range d.Gates {
		if g.Skipped == "" && !g.Passed {
			return false
		}
		if g.Skipped != "" && (g.Name == "tests" || g.Name == "criteria") {
			return false
		}
	}
	for _, it := range d.Review {
		if it.Target != api.TargetPlan && IsOpen(it) && it.Severity != api.SeverityMinor {
			return false
		}
	}
	for _, s := range d.Steps {
		if s.Status != "done" {
			return false
		}
	}
	return len(d.Criteria) > 0
}

// final writes the summary and sets done or incomplete. Tiegel appends
// its own status (gates, open items) – the roles' claims are not the
// truth.
func (e *Engine) final(ctx context.Context) error {
	done := e.done()
	e.setPhase("", api.PhaseFinal)
	var open []api.PlanReviewItem
	e.mu.Lock()
	for _, it := range e.st.Doc.Review {
		if it.Target != api.TargetPlan && IsOpen(it) {
			open = append(open, it)
		}
	}
	e.mu.Unlock()
	changes, _ := e.deps.Workspace.Changes(ctx)
	var cl strings.Builder
	for _, c := range changes {
		a, r := c.Stat()
		fmt.Fprintf(&cl, "- %s %s +%d -%d\n", c.Kind, c.Path, a, r)
	}
	var f Final
	parts := []Part{e.part(SecGoal, 100, 1<<30), e.part(SecCriteria, 95, 2000), e.part(SecGates, 95, 2000), e.part(SecPlan, 80, 2000),
		itemsPart("Open review items", open, 90), {Title: "Changed files", Body: cl.String(), Priority: 85, Min: 1000}}
	err := e.ask(ctx, call{role: api.RoleSummarizer, tmpl: TmplFinal, mode: ReadOnly, dir: e.deps.Workspace.Dir(), task: "final report", parts: parts, out: &f})
	if err != nil && errors.Is(err, context.Canceled) {
		return e.fail(err)
	}
	return e.Update(func(st *State) {
		var b strings.Builder
		if err == nil {
			b.WriteString(strings.TrimSpace(f.Summary) + "\n\n")
		} else {
			st.Note("final summary failed: " + err.Error())
		}
		b.WriteString("**Status (checked by Tiegel):** ")
		if done {
			b.WriteString("all gates passed, every acceptance criterion has a passing test, no open blocker.\n")
		} else {
			fmt.Fprintf(&b, "NOT finished after %d iterations.\n", st.Iteration)
			for _, g := range st.Doc.Gates {
				if g.Skipped == "" && !g.Passed {
					fmt.Fprintf(&b, "- gate %s failed: %s\n", g.Name, g.Summary)
				}
			}
			for _, c := range st.Doc.Criteria {
				if !c.Met {
					fmt.Fprintf(&b, "- criterion %s not met: %s\n", c.ID, oneLine(c.Text))
				}
			}
			for _, it := range open {
				fmt.Fprintf(&b, "- open %s (%s): %s\n", it.ID, it.Severity, oneLine(it.Claim))
			}
		}
		for _, g := range st.Doc.Gates {
			if g.Skipped != "" {
				fmt.Fprintf(&b, "- gate %s skipped: %s\n", g.Name, g.Skipped)
			}
		}
		st.Doc.Summary = b.String()
		st.Doc.Status = api.PlanIncomplete
		if done {
			st.Doc.Status = api.PlanDone
		}
		if e.deps.Agent != nil {
			st.Metrics.Blocked = e.deps.Agent.Blocked()
		}
	})
}

// Close ends the sessions.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.sessions {
		s.Close()
	}
}
