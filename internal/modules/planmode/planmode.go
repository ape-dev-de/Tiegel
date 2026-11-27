// Package planmode is the plan mode plugin (D567–D570): a plan is
// written, validated and reviewed by several agent sessions in fixed
// roles around one Markdown working document outside the repository,
// accepted by the user, and then executed by a senior DEV and a senior
// QA in an isolated git worktree with critic passes, a debate (optional
// mediator) and mechanical gates. The orchestration is
// internal/orchestrate; this module wires it to the IDE: the coding
// agent of the agent plugin (agent.command, its permission policy, the
// MCP servers of HookAgentMCPServers), the PHP index (nav.symbols), the
// run infrastructure (PHPUnit in the project interpreter, also in
// Docker) and the plan directory <config>/projects/<hash>/plans.
//
// Agents run only on an explicit user action (plan.start,
// plan.requestChanges, plan.execute, plan.resume, plan.eval).
//
// API (namespace plan.*): plan.start, plan.status, plan.list,
// plan.document, plan.accept, plan.requestChanges, plan.discard,
// plan.execute, plan.resume, plan.resolve, plan.cancel, plan.transcript,
// plan.changes, plan.diff, plan.revert, plan.apply, plan.keepBranch,
// plan.propose, plan.estimate, plan.eval. Events: plan.progress,
// plan.activity.
package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/agentpolicy"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
	"github.com/ape-dev-de/tiegel/internal/orchestrate"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// ID is the module ID.
const ID = "planmode"

// Settings keys.
const (
	SettingSubagents      = ID + ".subagents"
	SettingMaxParallel    = ID + ".maxParallel"
	SettingMediator       = ID + ".mediator"
	SettingMediatorRounds = ID + ".mediatorRounds"
	SettingMaxIterations  = ID + ".maxIterations"
	SettingMaxContext     = ID + ".maxContextTokens"
	SettingMCP            = ID + ".mcp"
	SettingMutation       = ID + ".mutation"
	SettingMutationLines  = ID + ".mutationMaxLines"
	SettingCoverage       = ID + ".coverage"
	SettingWorkspace      = ID + ".workspace"
	SettingDirtyTree      = ID + ".dirtyTree"
	SettingModelPlanner   = ID + ".modelPlanner"
	SettingModelDev       = ID + ".modelDev"
	SettingModelQA        = ID + ".modelQA"
	SettingModelReviewer  = ID + ".modelReviewer"
	// SettingDefaultOn remembers the "Plan mode" checkbox of the Agent
	// window.
	SettingDefaultOn = ID + ".checkbox"
)

// Options configure the module.
type Options struct {
	// Start replaces acp.Start (tests connect a fake agent).
	Start orchestrate.StartFunc
	// LookPath finds the agent executable (nil: the tool PATH).
	LookPath func(name string) (string, error)
	// Tests replaces the PHPUnit runner (tests).
	Tests orchestrate.TestRunner
	// Executable is Tiegel's program file for the MCP server command
	// (nil: os.Executable).
	Executable func() (string, error)
}

// Module is the plan mode plugin.
type Module struct {
	opts Options
	app  atomic.Pointer[api.App]
	bus  atomic.Pointer[bus.Bus]

	mu   sync.Mutex
	runs map[string]*running
}

// running is an orchestration in progress.
type running struct {
	eng    *orchestrate.Engine
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// New creates the module.
func New(opts Options) *Module {
	if opts.Executable == nil {
		opts.Executable = os.Executable
	}
	return &Module{opts: opts, runs: map[string]*running{}}
}

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module.
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"plan.*"} }

// SettingsPage implements module.PageProvider.
func (m *Module) SettingsPage() (section, title string) { return "AI", "Plan mode" }

func intp(v int) *int { return &v }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	const roles, gates, ws = "Roles and sessions", "Gates", "Workspace"
	return []settings.Spec{
		{Key: SettingSubagents, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Separate sessions per role (subagents)", Group: roles,
			Help: "On: every role (planners, validator, QA, DEV, critic, mediator) gets its own agent session, independent roles run in parallel. Off: the roles run one after another in one session per phase – for providers that handle several sessions badly. The critic always gets a fresh session."},
		{Key: SettingMaxParallel, Kind: settings.KindInt, Default: 2, Min: intp(1), Max: intp(8), Scope: settings.ScopeProject, Label: "Parallel sessions", Group: roles,
			Help: "How many role sessions may run at the same time (with subagents)."},
		{Key: SettingMediator, Kind: settings.KindEnum, Options: []string{api.MediatorAuto, api.MediatorAlways, api.MediatorOff}, Default: api.MediatorAuto, Scope: settings.ScopeProject, Label: "Mediator", Group: roles,
			Help: "auto: a mediator agent decides when DEV and QA still disagree after the rounds below. always: every rejected-and-insisted finding goes to the mediator. off: you decide disputes. The default is provisional – compare with: tiegel --headless plan eval."},
		{Key: SettingMediatorRounds, Kind: settings.KindInt, Default: 2, Min: intp(1), Max: intp(10), Scope: settings.ScopeProject, Label: "Debate rounds before mediation", Group: roles},
		{Key: SettingMaxIterations, Kind: settings.KindInt, Default: 5, Min: intp(1), Max: intp(20), Scope: settings.ScopeProject, Label: "Maximum iterations", Group: roles,
			Help: "Implement → critic → rating → gates rounds before the plan stops as incomplete."},
		{Key: SettingMaxContext, Kind: settings.KindInt, Default: 40000, Min: intp(4000), Max: intp(400000), Scope: settings.ScopeProject, Label: "Context budget per role call (tokens)", Group: roles,
			Help: "Each role gets only the document sections it needs, shortened to this budget. Small models lose quality beyond ~50k tokens."},
		{Key: SettingMCP, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Plan document tools over MCP", Group: roles,
			Help: "Give the sessions the tools plan_read, plan_update, plan_add_finding and plan_comment (Tiegel's MCP server). Answers are always structured JSON as well."},
		{Key: SettingModelPlanner, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Model for planners and summary", Group: roles, Help: "Empty: the agent's model."},
		{Key: SettingModelDev, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Model for DEV", Group: roles, Help: "Empty: the agent's model."},
		{Key: SettingModelQA, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Model for QA", Group: roles, Help: "Empty: the agent's model."},
		{Key: SettingModelReviewer, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Model for validator, critic and mediator", Group: roles, Help: "Empty: the agent's model."},
		{Key: SettingMutation, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Mutation-lite check", Group: gates,
			Help: "When the tests are green and the change is small: revert single changes and flip conditions on changed lines, rerun the tests. Surviving mutations become findings for QA."},
		{Key: SettingMutationLines, Kind: settings.KindInt, Default: 200, Min: intp(10), Max: intp(5000), Scope: settings.ScopeProject, Label: "Mutation-lite up to changed lines", Group: gates},
		{Key: SettingCoverage, Kind: settings.KindBool, Default: true, Scope: settings.ScopeProject, Label: "Changed-lines coverage", Group: gates,
			Help: "Needs pcov or Xdebug in the interpreter; skipped with a note otherwise."},
		{Key: SettingWorkspace, Kind: settings.KindEnum, Options: []string{"worktree", "in-place"}, Default: "worktree", Scope: settings.ScopeProject, Label: "Execute in", Group: ws,
			Help: "worktree: a git worktree with the branch tiegel/plan-<id> inside the project (.tiegel/worktrees, also visible in containers); apply, keep or discard it at the end. in-place: directly in your working copy (no red check against the base code)."},
		{Key: SettingDirtyTree, Kind: settings.KindEnum, Options: []string{string(orchestrate.DirtySnapshot), string(orchestrate.DirtyHead), string(orchestrate.DirtyRefuse)}, Default: string(orchestrate.DirtySnapshot), Scope: settings.ScopeProject, Label: "Uncommitted changes", Group: ws,
			Help: "snapshot: the worktree starts with your uncommitted changes of tracked files (git stash create, nothing is stashed). head: start from the last commit. refuse: do not execute while there are local changes."},
		{Key: SettingDefaultOn, Kind: settings.KindBool, Default: false, Scope: settings.ScopeProject, Label: "Plan mode checkbox on", Group: roles,
			Help: "Remembers the Plan mode checkbox of the Agent window."},
	}
}

// Start registers the API.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("planmode: needs an api.App context")
	}
	m.app.Store(app)
	m.bus.Store(ctx.Bus())
	p := api.NewProvider(ctx)
	api.Add(p, "plan.start", "Start plan mode for a goal (agents plan read-only in several sessions, until you accept); wait: until the plan awaits acceptance", m.start)
	api.Add(p, "plan.status", "State of a plan: phase, roles with elapsed time, disputes, gates, metrics, workspace", m.status)
	api.Add(p, "plan.list", "Plans of the project, newest first", m.list)
	api.Add(p, "plan.document", "The working document (Markdown, structured, revision history); section: one section only", m.document)
	api.Add(p, "plan.accept", "Accept the plan (nothing is implemented before); comment: recorded as a decision", m.accept)
	api.Add(p, "plan.requestChanges", "Request changes: another planning round on the affected sections", m.requestChanges)
	api.Add(p, "plan.discard", "Discard the plan (cancels it, removes its worktree and branch)", m.discard)
	api.Add(p, "plan.execute", "Execute the accepted plan: DEV and QA in a worktree, critic, rating, debate, gates, until green or the iteration limit", m.execute)
	api.Add(p, "plan.resume", "Continue a cancelled, failed or interrupted plan from its stored state", m.resume)
	api.Add(p, "plan.resolve", "Decide a disputed review item: dev or qa is right, dismiss, or comment", m.resolve)
	api.Add(p, "plan.cancel", "Stop the running orchestration of a plan (resumable)", m.cancelPlan)
	api.Add(p, "plan.transcript", "Transcript of a role's session(s)", m.transcript)
	api.Add(p, "plan.changes", "Files changed by the execution (compared with the plan's base)", m.changes)
	api.Add(p, "plan.diff", "A changed file before the plan and now (unified diff)", m.diff)
	api.Add(p, "plan.revert", "Restore the base version of a changed file in the plan workspace", m.revert)
	api.Add(p, "plan.apply", "Apply the plan's changes to your working copy (git apply --3way, no commit) and remove the worktree", m.apply)
	api.Add(p, "plan.keepBranch", "Remove the plan worktree but keep its branch tiegel/plan-<id>", m.keepBranch)
	api.Add(p, "plan.propose", "MCP tools of a role session propose a document change (merged after the role's turn)", m.propose)
	api.Add(p, "plan.estimate", "What plan.start would run: roles, sessions, agent calls, rough tokens (no agent call)", m.estimate)
	api.Add(p, "plan.eval", "Run the same goal with mediator off, auto and always (auto-accepted, costs tokens) and compare the metrics", m.eval)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(ctx.Bus().Subscribe(api.TopicProjectClosed, func(bus.Event) { m.cancelAll() }))
	return nil
}

// Stop implements module.Module: running orchestrations are cancelled
// (resumable).
func (m *Module) Stop() error {
	m.cancelAll()
	return nil
}

func (m *Module) cancelAll() {
	m.mu.Lock()
	var rs []*running
	for _, r := range m.runs {
		rs = append(rs, r)
	}
	m.mu.Unlock()
	for _, r := range rs {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
		}
	}
}

func (m *Module) application() *api.App { return m.app.Load() }

func (m *Module) publish(topic string, v any) {
	if b := m.bus.Load(); b != nil {
		b.Publish(topic, v)
	}
}

func setting[T any](m *Module, key string, def T) T {
	app := m.application()
	if app == nil {
		return def
	}
	return api.Setting(app, key, def)
}

// store is the plan directory of the open project.
func (m *Module) store() (*orchestrate.Store, string, error) {
	app := m.application()
	if app == nil {
		return nil, "", api.Errorf(api.CodeFailed, "planmode: module not started")
	}
	root, err := app.RequireProject()
	if err != nil {
		return nil, "", err
	}
	ps := app.ProjectSettings()
	if ps == nil || ps.Dir() == "" {
		return nil, "", api.Errorf(api.CodeFailed, "planmode: the project has no settings directory")
	}
	return &orchestrate.Store{Dir: filepath.Join(ps.Dir(), "plans")}, root, nil
}

// config builds the orchestration settings (the plan's stored choices
// win over the current settings).
func (m *Module) config(st *orchestrate.State) orchestrate.Config {
	root := ""
	if app := m.application(); app != nil {
		root = app.ProjectRoot()
	}
	planner, rev := setting(m, SettingModelPlanner, ""), setting(m, SettingModelReviewer, "")
	models := map[string]string{
		api.RolePlannerArch: planner, api.RolePlannerCode: planner, api.RolePlannerTests: planner, api.RoleSummarizer: planner, "qa-review": setting(m, SettingModelQA, ""),
		api.RoleDev: setting(m, SettingModelDev, ""), api.RoleQA: setting(m, SettingModelQA, ""),
		api.RoleValidator: rev, api.RoleCritic: rev, api.RoleMediator: rev,
	}
	cfg := orchestrate.Config{
		Project:          filepath.Base(root),
		Subagents:        setting(m, SettingSubagents, true),
		MaxParallel:      setting(m, SettingMaxParallel, 2),
		Mediator:         setting(m, SettingMediator, api.MediatorAuto),
		MediatorRounds:   setting(m, SettingMediatorRounds, 2),
		MaxIterations:    setting(m, SettingMaxIterations, 5),
		MaxContextTokens: setting(m, SettingMaxContext, 40000),
		MCP:              setting(m, SettingMCP, true),
		Mutation:         setting(m, SettingMutation, true),
		MutationMaxLines: setting(m, SettingMutationLines, 200),
		Coverage:         setting(m, SettingCoverage, true),
		Models:           models,
	}
	if st != nil {
		cfg.Subagents = st.Subagents
		if st.Mediator != "" {
			cfg.Mediator = st.Mediator
		}
	}
	return cfg.Defaults()
}

// ---- status ---------------------------------------------------------------------

func (m *Module) statusOf(st orchestrate.State, s *orchestrate.Store) api.PlanStatus {
	m.mu.Lock()
	_, isRunning := m.runs[st.Doc.ID]
	m.mu.Unlock()
	ps := api.PlanStatus{ID: st.Doc.ID, Title: st.Doc.Title, State: st.Doc.Status, Phase: st.Doc.Phase, Running: isRunning,
		Iteration: st.Iteration, Round: st.Metrics.Rounds, Roles: st.Roles, Disputes: orchestrate.Disputes(&st.Doc),
		Open: len(orchestrate.OpenFor(&st.Doc, "")), Gates: st.Doc.Gates, Metrics: st.Metrics, Workspace: st.Workspace,
		Started: st.Started, Error: st.Error, Notes: st.Notes}
	if s != nil {
		ps.Document = s.DocPath(st.Doc.ID)
	}
	now := time.Now()
	for i := range ps.Roles {
		if r := &ps.Roles[i]; r.State == "running" && !r.Started.IsZero() {
			r.ElapsedMs = now.Sub(r.Started).Milliseconds()
		}
	}
	return ps
}

// load returns the live state (running) or the stored one; id "" is
// the latest plan.
func (m *Module) load(id string) (orchestrate.State, *orchestrate.Store, error) {
	s, _, err := m.store()
	if err != nil {
		return orchestrate.State{}, nil, err
	}
	if id == "" {
		ids := s.List()
		if len(ids) == 0 {
			return orchestrate.State{}, s, api.Errorf(api.CodeNotFound, "no plan yet (plan.start)")
		}
		id = ids[0]
	}
	if eng := m.engine(id); eng != nil {
		return eng.State(), s, nil
	}
	st, err := s.Load(id)
	if err != nil {
		return orchestrate.State{}, s, api.Errorf(api.CodeNotFound, "plan %s: %v", id, err)
	}
	return *st, s, nil
}

// engine returns the engine of a running plan (nil while it starts or
// when it does not run).
func (m *Module) engine(id string) *orchestrate.Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.runs[id]; r != nil {
		return r.eng
	}
	return nil
}

func (m *Module) status(_ context.Context, p api.PlanIDParams) (api.PlanStatus, error) {
	st, s, err := m.load(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	return m.statusOf(st, s), nil
}

func (m *Module) list(context.Context, struct{}) ([]api.PlanStatus, error) {
	s, _, err := m.store()
	if err != nil {
		return nil, err
	}
	out := []api.PlanStatus{}
	for _, id := range s.List() {
		if st, _, err := m.load(id); err == nil {
			ps := m.statusOf(st, s)
			ps.Roles = nil
			out = append(out, ps)
		}
	}
	return out, nil
}

func (m *Module) document(_ context.Context, p api.PlanDocumentParams) (api.PlanDocumentResult, error) {
	st, s, err := m.load(p.ID)
	if err != nil {
		return api.PlanDocumentResult{}, err
	}
	res := api.PlanDocumentResult{Path: s.DocPath(st.Doc.ID), Document: st.Doc, History: s.History(st.Doc.ID)}
	switch {
	case p.Section == "":
		res.Markdown = orchestrate.Render(st.Doc)
	case orchestrate.ValidSection(p.Section):
		res.Markdown = "## " + orchestrate.SectionTitle(p.Section) + "\n\n" + orchestrate.SectionMarkdown(st.Doc, p.Section)
	default:
		return res, api.Errorf(api.CodeInvalidParams, "unknown section %q (%s)", p.Section, strings.Join(orchestrate.Sections, ", "))
	}
	return res, nil
}

func (m *Module) transcript(_ context.Context, p api.PlanTranscriptParams) ([]api.AgentEntry, error) {
	st, s, err := m.load(p.ID)
	if err != nil {
		return nil, err
	}
	if p.Role == "" {
		return nil, api.Errorf(api.CodeInvalidParams, "role is required")
	}
	es, err := s.LoadTranscript(st.Doc.ID, p.Role)
	if err != nil {
		// Without subagents all roles of a phase share one session.
		if es, err = s.LoadTranscript(st.Doc.ID, "shared"); err != nil {
			return []api.AgentEntry{}, nil
		}
	}
	return es, nil
}

// ---- running orchestrations ---------------------------------------------------------

// launch runs f with a new engine for st in the background (one run per
// plan). wait blocks until it ended.
func (m *Module) launch(ctx context.Context, st *orchestrate.State, s *orchestrate.Store, root string, execute bool, allowShell bool, wait bool,
	f func(ctx context.Context, e *orchestrate.Engine) error) (api.PlanStatus, error) {
	id := st.Doc.ID
	m.mu.Lock()
	if m.runs[id] != nil {
		m.mu.Unlock()
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "plan %s is running (plan.cancel stops it)", id)
	}
	rctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan struct{})}
	m.runs[id] = r
	m.mu.Unlock()
	finish := func() {
		m.mu.Lock()
		delete(m.runs, id)
		m.mu.Unlock()
		cancel()
	}
	cfg := m.config(st)
	var ws orchestrate.Workspace
	if execute {
		w, note, err := m.workspace(rctx, st, s, root)
		if err != nil {
			finish()
			close(r.done)
			return m.statusOf(*st, s), err
		}
		ws = w
		if note != "" {
			st.Note(note)
		}
	}
	agent, err := m.startAgent(rctx, root, st, s, execute, allowShell, ws)
	if err != nil {
		finish()
		close(r.done)
		st.Error = err.Error()
		_ = s.Save(st)
		return m.statusOf(*st, s), err
	}
	deps := orchestrate.Deps{Agent: agent, Store: s, Root: root, Workspace: ws, Tests: m.testRunner(root), Symbols: m.symbols,
		OnChange: func(x orchestrate.State) { m.publish(api.TopicPlanProgress, m.statusOf(x, s)) }}
	eng := orchestrate.New(cfg, deps, st)
	m.mu.Lock()
	r.eng = eng
	m.mu.Unlock()
	_ = s.Save(st)
	go func() {
		defer close(r.done)
		func() {
			defer finish()
			defer agent.Close()
			defer r.eng.Close()
			r.err = f(rctx, r.eng)
		}()
		// Published after finish: the event says the run ended.
		m.publish(api.TopicPlanProgress, m.statusOf(r.eng.State(), s))
	}()
	if !wait {
		return m.statusOf(r.eng.State(), s), nil
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		cancel()
		<-r.done
	}
	final := r.eng.State()
	ps := m.statusOf(final, s)
	if r.err != nil && !errors.Is(r.err, context.Canceled) {
		return ps, api.Errorf(api.CodeFailed, "%v", r.err)
	}
	return ps, nil
}

func (m *Module) start(ctx context.Context, p api.PlanStartParams) (api.PlanStatus, error) {
	goal := strings.TrimSpace(p.Goal)
	if goal == "" {
		return api.PlanStatus{}, api.Errorf(api.CodeInvalidParams, "goal is required")
	}
	s, root, err := m.store()
	if err != nil {
		return api.PlanStatus{}, err
	}
	switch p.Mediator {
	case "", api.MediatorOff, api.MediatorAuto, api.MediatorAlways:
	default:
		return api.PlanStatus{}, api.Errorf(api.CodeInvalidParams, "mediator must be off, auto or always")
	}
	now := time.Now()
	st := &orchestrate.State{Doc: orchestrate.NewDocument(orchestrate.NewID(now), goal, now)}
	cfg := m.config(nil)
	st.Subagents, st.Mediator = cfg.Subagents, cfg.Mediator
	if p.Subagents != nil {
		st.Subagents = *p.Subagents
	}
	if p.Mediator != "" {
		st.Mediator = p.Mediator
	}
	if err := s.Save(st); err != nil {
		return api.PlanStatus{}, api.Errorf(api.CodeFailed, "%v", err)
	}
	return m.launch(ctx, st, s, root, false, false, p.Wait, func(ctx context.Context, e *orchestrate.Engine) error { return e.Plan(ctx) })
}

// stored loads a plan that must not be running.
func (m *Module) stored(id string) (*orchestrate.State, *orchestrate.Store, string, error) {
	s, root, err := m.store()
	if err != nil {
		return nil, nil, "", err
	}
	st, _, err := m.load(id)
	if err != nil {
		return nil, nil, "", err
	}
	m.mu.Lock()
	busy := m.runs[st.Doc.ID] != nil
	m.mu.Unlock()
	if busy {
		return nil, nil, "", api.Errorf(api.CodeConflict, "plan %s is running (plan.cancel stops it)", st.Doc.ID)
	}
	return &st, s, root, nil
}

func (m *Module) accept(_ context.Context, p api.PlanCommentParams) (api.PlanStatus, error) {
	st, s, _, err := m.stored(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	if st.Doc.Status != api.PlanReview {
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "the plan is %s; only a plan awaiting acceptance can be accepted", st.Doc.Status)
	}
	st.Doc.Status = api.PlanAccepted
	text := "Plan accepted by the user."
	if c := strings.TrimSpace(p.Comment); c != "" {
		text += " " + c
	}
	orchestrate.AddDecision(&st.Doc, text, "user", time.Now())
	if err := s.Save(st); err != nil {
		return api.PlanStatus{}, api.Errorf(api.CodeFailed, "%v", err)
	}
	ps := m.statusOf(*st, s)
	m.publish(api.TopicPlanProgress, ps)
	return ps, nil
}

func (m *Module) requestChanges(ctx context.Context, p api.PlanCommentParams) (api.PlanStatus, error) {
	if strings.TrimSpace(p.Comment) == "" {
		return api.PlanStatus{}, api.Errorf(api.CodeInvalidParams, "comment is required")
	}
	st, s, root, err := m.stored(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	if st.Doc.Status != api.PlanReview && st.Doc.Status != api.PlanFailed && st.Doc.Status != api.PlanCancelled {
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "the plan is %s; changes can be requested before it is accepted", st.Doc.Status)
	}
	return m.launch(ctx, st, s, root, false, false, p.Wait, func(ctx context.Context, e *orchestrate.Engine) error { return e.Revise(ctx, p.Comment) })
}

func (m *Module) execute(ctx context.Context, p api.PlanExecuteParams) (api.PlanStatus, error) {
	st, s, root, err := m.stored(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	switch st.Doc.Status {
	case api.PlanAccepted, api.PlanExecuting, api.PlanNeedsUser, api.PlanIncomplete:
	case api.PlanCancelled, api.PlanFailed:
		if !accepted(st) {
			return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "the plan was not accepted yet (plan.resume continues the planning)")
		}
	default:
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "the plan is %s; accept it first (plan.accept)", st.Doc.Status)
	}
	if n := len(orchestrate.Disputes(&st.Doc)); n > 0 {
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "%d disputed item(s) wait for your decision (plan.resolve)", n)
	}
	if st.Doc.Status == api.PlanIncomplete {
		// Another round of iterations.
		st.Iteration = 0
	}
	return m.launch(ctx, st, s, root, true, p.AllowShell, p.Wait, func(ctx context.Context, e *orchestrate.Engine) error { return e.Execute(ctx) })
}

// accepted tells whether the user accepted the plan (a decision by the
// user, or execution started).
func accepted(st *orchestrate.State) bool {
	if st.Workspace != nil {
		return true
	}
	for _, d := range st.Doc.Decisions {
		if d.By == "user" && strings.HasPrefix(d.Text, "Plan accepted") {
			return true
		}
	}
	return false
}

func (m *Module) resume(ctx context.Context, p api.PlanExecuteParams) (api.PlanStatus, error) {
	st, s, root, err := m.stored(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	if accepted(st) {
		return m.execute(ctx, p)
	}
	switch st.Doc.Status {
	case api.PlanDrafting, api.PlanCancelled, api.PlanFailed:
	default:
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "nothing to resume: the plan is %s", st.Doc.Status)
	}
	return m.launch(ctx, st, s, root, false, false, p.Wait, func(ctx context.Context, e *orchestrate.Engine) error { return e.Plan(ctx) })
}

func (m *Module) resolve(_ context.Context, p api.PlanResolveParams) (api.PlanStatus, error) {
	if p.Item == "" {
		return api.PlanStatus{}, api.Errorf(api.CodeInvalidParams, "item is required")
	}
	st, s, err := m.load(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	eng := m.engine(st.Doc.ID)
	if eng == nil {
		eng = orchestrate.New(m.config(&st), orchestrate.Deps{Store: s}, &st)
	}
	if err := eng.Resolve(p.Item, p.Decision, p.Comment); err != nil {
		return m.statusOf(eng.State(), s), api.Errorf(api.CodeInvalidParams, "%v", err)
	}
	ps := m.statusOf(eng.State(), s)
	m.publish(api.TopicPlanProgress, ps)
	return ps, nil
}

func (m *Module) cancelPlan(_ context.Context, p api.PlanIDParams) (api.PlanStatus, error) {
	st, s, err := m.load(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	m.mu.Lock()
	r := m.runs[st.Doc.ID]
	m.mu.Unlock()
	if r != nil {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(15 * time.Second):
		}
		m.mu.Lock()
		eng := r.eng
		m.mu.Unlock()
		if eng != nil {
			st = eng.State()
		}
	}
	return m.statusOf(st, s), nil
}

func (m *Module) discard(ctx context.Context, p api.PlanIDParams) (api.PlanStatus, error) {
	_, _ = m.cancelPlan(ctx, p)
	st, s, root, err := m.stored(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	if st.Workspace != nil && st.Workspace.Mode == "worktree" {
		if w, err := orchestrate.OpenWorktree(ctx, root, st.Doc.ID, orchestrate.DirtyHead); err == nil {
			if err := w.Remove(ctx, false); err != nil {
				st.Note("remove worktree: " + err.Error())
			}
		}
	}
	st.Doc.Status = api.PlanDiscarded
	_ = s.Save(st)
	ps := m.statusOf(*st, s)
	m.publish(api.TopicPlanProgress, ps)
	return ps, nil
}

// ---- workspace -------------------------------------------------------------------------

// workspace prepares (or reopens) where the plan executes. Fallbacks to
// in place are explained in the note.
func (m *Module) workspace(ctx context.Context, st *orchestrate.State, s *orchestrate.Store, root string) (orchestrate.Workspace, string, error) {
	inPlace := func(note string) (orchestrate.Workspace, string, error) {
		return orchestrate.NewInPlace(root, filepath.Join(s.PlanDir(st.Doc.ID), "orig.json"), note), note, nil
	}
	mode := setting(m, SettingWorkspace, "worktree")
	if st.Workspace != nil {
		mode = st.Workspace.Mode
	}
	if mode == "in-place" {
		return inPlace("")
	}
	if ok, why := m.containerSees(ctx, filepath.Join(root, filepath.FromSlash(orchestrate.WorktreeDirName))); !ok {
		return inPlace("executing in place: " + why)
	}
	w, err := orchestrate.OpenWorktree(ctx, root, st.Doc.ID, orchestrate.DirtyPolicy(setting(m, SettingDirtyTree, string(orchestrate.DirtySnapshot))))
	switch {
	case errors.Is(err, orchestrate.ErrDirty):
		return nil, "", api.Errorf(api.CodeConflict, "the working tree has uncommitted changes and %s is refuse: commit or stash them, or change the setting", SettingDirtyTree)
	case err != nil:
		return inPlace("executing in place (no git worktree: " + err.Error() + ")")
	}
	return w, "", nil
}

// containerSees checks that a container interpreter mounts dir (the
// worktrees live inside the project, so a mount of the project is
// enough).
func (m *Module) containerSees(ctx context.Context, dir string) (bool, string) {
	app := m.application()
	var res struct {
		Mappings []api.MappingInfo `json:"mappings"`
		Source   string            `json:"source"`
	}
	if err := app.CallLocal(ctx, "runtime.mappings.get", struct{}{}, &res); err != nil || res.Source == "none" || len(res.Mappings) == 0 {
		return true, ""
	}
	return orchestrate.MappingCovers(res.Mappings, dir)
}

// openWorkspace reopens the workspace of an executed plan.
func (m *Module) openWorkspace(ctx context.Context, st *orchestrate.State, s *orchestrate.Store, root string) (orchestrate.Workspace, error) {
	if st.Workspace == nil {
		return nil, api.Errorf(api.CodeConflict, "the plan has not been executed yet")
	}
	if st.Workspace.Mode == "worktree" {
		w, err := orchestrate.OpenWorktree(ctx, root, st.Doc.ID, orchestrate.DirtyHead)
		if err != nil {
			return nil, api.Errorf(api.CodeFailed, "%v", err)
		}
		return w, nil
	}
	return orchestrate.NewInPlace(root, filepath.Join(s.PlanDir(st.Doc.ID), "orig.json"), ""), nil
}

func (m *Module) changes(ctx context.Context, p api.PlanIDParams) ([]api.PlanChange, error) {
	st, s, err := m.load(p.ID)
	if err != nil {
		return nil, err
	}
	_, root, _ := m.store()
	ws, err := m.openWorkspace(ctx, &st, s, root)
	if err != nil {
		return nil, err
	}
	cs, err := ws.Changes(ctx)
	if err != nil {
		return nil, api.Errorf(api.CodeFailed, "%v", err)
	}
	out := []api.PlanChange{}
	for _, c := range cs {
		out = append(out, c.Info())
	}
	return out, nil
}

func (m *Module) diff(ctx context.Context, p api.PlanDiffParams) (api.PlanDiff, error) {
	st, s, err := m.load(p.ID)
	if err != nil {
		return api.PlanDiff{}, err
	}
	_, root, _ := m.store()
	ws, err := m.openWorkspace(ctx, &st, s, root)
	if err != nil {
		return api.PlanDiff{}, err
	}
	cs, err := ws.Changes(ctx)
	if err != nil {
		return api.PlanDiff{}, api.Errorf(api.CodeFailed, "%v", err)
	}
	for _, c := range cs {
		if c.Path == filepath.ToSlash(filepath.Clean(p.Path)) {
			return api.PlanDiff{PlanChange: c.Info(), Original: string(c.Old), Current: string(c.New), Unified: c.Unified()}, nil
		}
	}
	return api.PlanDiff{}, api.Errorf(api.CodeNotFound, "the plan did not change %s", p.Path)
}

func (m *Module) revert(ctx context.Context, p api.PlanDiffParams) ([]api.PlanChange, error) {
	st, s, root, err := m.stored(p.ID)
	if err != nil {
		return nil, err
	}
	ws, err := m.openWorkspace(ctx, st, s, root)
	if err != nil {
		return nil, err
	}
	if err := ws.Revert(ctx, p.Path); err != nil {
		return nil, api.Errorf(api.CodeFailed, "%v", err)
	}
	return m.changes(ctx, api.PlanIDParams{ID: st.Doc.ID})
}

func (m *Module) finishWorktree(ctx context.Context, p api.PlanIDParams, apply, keep bool) (api.PlanStatus, error) {
	st, s, root, err := m.stored(p.ID)
	if err != nil {
		return api.PlanStatus{}, err
	}
	if st.Workspace == nil {
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "the plan has not been executed yet")
	}
	switch st.Doc.Status {
	case api.PlanDone, api.PlanIncomplete, api.PlanNeedsUser, api.PlanCancelled, api.PlanFailed:
	default:
		return m.statusOf(*st, s), api.Errorf(api.CodeConflict, "the plan is %s", st.Doc.Status)
	}
	if st.Workspace.Mode == "worktree" {
		w, err := orchestrate.OpenWorktree(ctx, root, st.Doc.ID, orchestrate.DirtyHead)
		if err != nil {
			return m.statusOf(*st, s), api.Errorf(api.CodeFailed, "%v", err)
		}
		if apply {
			if err := w.Apply(ctx); err != nil {
				return m.statusOf(*st, s), api.Errorf(api.CodeFailed, "apply: %v (the worktree is kept)", err)
			}
			if app := m.application(); app != nil {
				_ = app.CallLocal(ctx, "watch.refresh", struct{}{}, nil)
			}
		}
		if err := w.Remove(ctx, keep); err != nil {
			st.Note("remove worktree: " + err.Error())
		}
	}
	text := "Changes applied to the working copy (not committed)."
	switch {
	case !apply && keep:
		text = "Plan branch " + st.Workspace.Branch + " kept, worktree removed."
	case !apply:
		text = "Worktree and branch removed."
	}
	orchestrate.AddDecision(&st.Doc, text, "user", time.Now())
	st.Doc.Status = api.PlanApplied
	if !apply {
		st.Doc.Status = api.PlanDiscarded
	}
	_ = s.Save(st)
	ps := m.statusOf(*st, s)
	m.publish(api.TopicPlanProgress, ps)
	return ps, nil
}

func (m *Module) apply(ctx context.Context, p api.PlanIDParams) (api.PlanStatus, error) {
	return m.finishWorktree(ctx, p, true, false)
}

func (m *Module) keepBranch(ctx context.Context, p api.PlanIDParams) (api.PlanStatus, error) {
	return m.finishWorktree(ctx, p, false, true)
}

func (m *Module) propose(_ context.Context, p api.PlanProposeParams) (struct{}, error) {
	s, _, err := m.store()
	if err != nil {
		return struct{}{}, err
	}
	switch p.Op {
	case "update", "finding", "comment":
	default:
		return struct{}{}, api.Errorf(api.CodeInvalidParams, "op must be update, finding or comment")
	}
	if err := s.Propose(p); err != nil {
		return struct{}{}, api.Errorf(api.CodeInvalidParams, "%v", err)
	}
	return struct{}{}, nil
}

// ---- agent -------------------------------------------------------------------------------

// agentCommand returns the agent plugin's command line.
func (m *Module) agentCommand() (string, []string) {
	line := strings.TrimSpace(setting(m, "agent.command", "opencode acp"))
	if line == "" {
		line = "opencode acp"
	}
	argv, err := run.SplitWords(line)
	if err != nil || len(argv) == 0 {
		argv = strings.Fields(line)
	}
	return line, argv
}

func (m *Module) searchPath() string {
	dirs := os.Getenv("PATH")
	if app := m.application(); app != nil {
		dirs = shellenv.Merge(app.InstallEnv().PathEnv, dirs)
	}
	if home, _ := os.UserHomeDir(); home != "" {
		dirs = shellenv.Merge(dirs, filepath.Join(home, ".opencode", "bin"))
	}
	return dirs
}

func (m *Module) lookPath(name string) (string, error) {
	if m.opts.LookPath != nil {
		return m.opts.LookPath(name)
	}
	if filepath.IsAbs(name) {
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			return name, nil
		}
		return "", os.ErrNotExist
	}
	return shellenv.LookPath(name, m.searchPath())
}

// policy is the agent plugin's permission policy (agent.status).
func (m *Module) policy(ctx context.Context) api.AgentPolicy {
	var st api.AgentStatus
	if app := m.application(); app != nil && app.CallLocal(ctx, "agent.status", struct{}{}, &st) == nil {
		return st.Policy
	}
	return api.AgentPolicy{Edit: api.PolicyAllow, Shell: api.PolicyAsk, GitCommit: api.PolicyDeny, GitPush: api.PolicyDeny, Web: api.PolicyAsk, Other: api.PolicyAsk}
}

// opencodeConfig is the inline opencode configuration of a run: planning
// denies edits, commands and the web in opencode itself; execution asks
// Tiegel for everything and denies history changes and push.
func opencodeConfig(execute bool) string {
	if !execute {
		return `{"permission":{"edit":"deny","bash":"deny","webfetch":"deny","websearch":"deny"}}`
	}
	bash := map[string]string{"*": "ask", "git push*": "deny", "git * push*": "deny"}
	for v := range agentpolicy.CommitVerbs {
		bash["git "+v+"*"] = "deny"
		bash["git * "+v+"*"] = "deny"
	}
	b, _ := json.Marshal(map[string]any{"permission": map[string]any{"edit": "ask", "bash": bash, "webfetch": "ask", "websearch": "ask"}})
	return string(b)
}

// startAgent starts the agent process of a run.
func (m *Module) startAgent(ctx context.Context, root string, st *orchestrate.State, s *orchestrate.Store, execute, allowShell bool, ws orchestrate.Workspace) (*orchestrate.ACPAgent, error) {
	line, argv := m.agentCommand()
	path, err := m.lookPath(argv[0])
	if err != nil {
		return nil, api.Errorf(api.CodeNotFound, "agent %q not found (Settings › AI › Agent › Agent command; the Agent window can install opencode)", argv[0])
	}
	env := append(os.Environ(), "PATH="+m.searchPath())
	if strings.TrimSuffix(filepath.Base(argv[0]), ".exe") == "opencode" {
		env = append(env, "OPENCODE_CONFIG_CONTENT="+opencodeConfig(execute))
	}
	dir := root
	if ws != nil {
		dir = ws.Dir()
	}
	id := st.Doc.ID
	opts := orchestrate.ACPOptions{
		Start: m.opts.Start, Command: acp.Command{Path: path, Args: argv[1:], Dir: dir, Env: env},
		Policy: m.policy(ctx), AllowAsk: allowShell, ProjectRoot: root,
		MCPServers: func(spec orchestrate.SessionSpec) []acp.MCPServerStdio { return m.mcpServers(ctx, root, id, spec) },
		OnActivity: func(role string, e api.AgentEntry, delta string) {
			m.publish(api.TopicPlanActivity, api.PlanActivity{Plan: id, Role: role, Entry: e, Delta: delta})
		},
	}
	if ws != nil {
		opts.OnEdit = ws.BeforeEdit
	}
	a, err := orchestrate.StartACP(ctx, opts)
	if err != nil {
		return nil, api.Errorf(api.CodeFailed, "start %s: %v", line, err)
	}
	return a, nil
}

// mcpServers are the servers of a session: the hook's (project
// knowledge) and Tiegel's plan document tools.
func (m *Module) mcpServers(ctx context.Context, root, id string, spec orchestrate.SessionSpec) []acp.MCPServerStdio {
	app := m.application()
	var out []acp.MCPServerStdio
	list, _ := api.HookAgentMCPServers.Apply(ctx, app.Hooks(), nil)
	for _, s := range list {
		srv := acp.MCPServerStdio{Name: s.Name, Command: s.Command, Args: s.Args}
		for _, kv := range s.Env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				srv.Env = append(srv.Env, acp.EnvVariable{Name: k, Value: v})
			}
		}
		out = append(out, srv)
	}
	if spec.MCP {
		if exe, err := m.opts.Executable(); err == nil {
			args := []string{"--headless", "mcp", "plan", "--project", root, "--plan", id, "--role", spec.Role}
			if cd := app.ConfigDir(); cd != "" {
				args = append(args, "--config-dir", cd)
			}
			out = append(out, acp.MCPServerStdio{Name: "tiegel-plan", Command: exe, Args: args})
		}
	}
	return out
}

// symbols finds the files declaring a symbol in the PHP index.
func (m *Module) symbols(ctx context.Context, name string) []string {
	app := m.application()
	if app == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res struct {
		Symbols []struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"symbols"`
	}
	libs := false
	if err := app.CallLocal(ctx, "nav.symbols", map[string]any{"query": name, "limit": 5, "libraries": &libs, "wait": true}, &res); err != nil {
		return nil
	}
	short := name
	if i := strings.LastIndex(short, `\`); i >= 0 {
		short = short[i+1:]
	}
	var out []string
	for _, s := range res.Symbols {
		if strings.EqualFold(s.Name, short) && !slices.Contains(out, s.Path) {
			out = append(out, s.Path)
		}
	}
	return out
}

// ---- estimate and eval ------------------------------------------------------------------

// PlanEstimate is the result of plan.estimate.
type PlanEstimate struct {
	Agent     string   `json:"agent"`
	Subagents bool     `json:"subagents"`
	Mediator  string   `json:"mediator"`
	Roles     []string `json:"roles"`
	// PlanningCalls and ExecutionCalls are the agent calls without
	// repairs (execution: per iteration).
	PlanningCalls  int    `json:"planningCalls"`
	IterationCalls int    `json:"iterationCalls"`
	MaxIterations  int    `json:"maxIterations"`
	BudgetTokens   int    `json:"budgetTokens"`
	Tokens         int    `json:"tokens"`
	Note           string `json:"note"`
}

func (m *Module) estimate(context.Context, struct{}) (PlanEstimate, error) {
	cfg := m.config(nil)
	line, _ := m.agentCommand()
	// Planning: 3 planners, validator, QA review, summary. Per
	// iteration: DEV, QA, 2 critics, 2 ratings (+ mediator).
	e := PlanEstimate{Agent: line, Subagents: cfg.Subagents, Mediator: cfg.Mediator, MaxIterations: cfg.MaxIterations, BudgetTokens: cfg.MaxContextTokens,
		Roles:         []string{api.RolePlannerArch, api.RolePlannerCode, api.RolePlannerTests, api.RoleValidator, "qa-review", api.RoleSummarizer, api.RoleDev, api.RoleQA, api.RoleCritic, api.RoleMediator},
		PlanningCalls: 6, IterationCalls: 6}
	// Prompts are typically a fifth of the budget; agents add their own
	// tool calls on top.
	e.Tokens = (e.PlanningCalls + e.IterationCalls*2) * cfg.MaxContextTokens / 5
	e.Note = "rough: prompts only, a typical run uses 2 iterations; the agent's own file reads come on top. Billed by your agent's provider."
	return e, nil
}

// PlanEvalParams are the params of plan.eval.
type PlanEvalParams struct {
	Goal string `json:"goal"`
	// Modes are the mediator modes to compare (default off, auto, always).
	Modes []string `json:"modes,omitempty"`
	// Yes must be set: the evaluation runs the agents several times.
	Yes bool `json:"yes"`
}

// PlanEvalRow is one run of plan.eval.
type PlanEvalRow struct {
	Mediator string          `json:"mediator"`
	Plan     string          `json:"plan"`
	State    string          `json:"state"`
	Metrics  api.PlanMetrics `json:"metrics"`
	Error    string          `json:"error,omitempty"`
}

// eval runs the goal once per mediator mode: plan, auto-accept,
// execute (the user's disputes count as unresolved), discard the
// workspace; the metrics are compared.
func (m *Module) eval(ctx context.Context, p PlanEvalParams) ([]PlanEvalRow, error) {
	if !p.Yes {
		return nil, api.Errorf(api.CodeInvalidParams, "plan.eval runs the agents several times and costs tokens: pass yes")
	}
	modes := p.Modes
	if len(modes) == 0 {
		modes = []string{api.MediatorOff, api.MediatorAuto, api.MediatorAlways}
	}
	var rows []PlanEvalRow
	for _, mode := range modes {
		row := PlanEvalRow{Mediator: mode}
		st, err := m.start(ctx, api.PlanStartParams{Goal: p.Goal, Mediator: mode, Wait: true})
		row.Plan = st.ID
		if err == nil && st.State == api.PlanReview {
			if _, err = m.accept(ctx, api.PlanCommentParams{ID: st.ID, Comment: "(plan eval)"}); err == nil {
				st, err = m.execute(ctx, api.PlanExecuteParams{ID: st.ID, Wait: true})
			}
		}
		if err != nil {
			row.Error = err.Error()
		}
		if cur, _, lerr := m.load(st.ID); lerr == nil {
			row.State, row.Metrics = cur.Doc.Status, cur.Metrics
		}
		if row.Plan != "" {
			_, _ = m.discard(ctx, api.PlanIDParams{ID: row.Plan})
		}
		rows = append(rows, row)
		if ctx.Err() != nil {
			break
		}
	}
	return rows, nil
}
