package knowledge

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

// planned is what Tiegel knows about the project for planning (the
// expensive part: file list, facts, entry point inventory). Manifest and
// questions are always read fresh from disk, so readers see the pages a
// running build writes.
type planned struct {
	dir     string
	planner kn.Planner
}

// projectFiles lists the project's PHP files (project relative; the
// .gitignore is respected, vendor is not descended).
func (m *Module) projectFiles(ctx context.Context) ([]string, error) {
	app := m.application()
	var res api.FilesListResult
	if err := app.CallLocal(ctx, "files.list", api.FilesListParams{Limit: 200000}, &res); err != nil {
		return nil, err
	}
	var out []string
	var walk func(n *api.FileNode)
	walk = func(n *api.FileNode) {
		if n == nil {
			return
		}
		for _, c := range n.Children {
			if c.Dir {
				walk(c)
			} else if strings.EqualFold(path.Ext(c.Path), ".php") {
				out = append(out, c.Path)
			}
		}
	}
	walk(res.Tree)
	return out, nil
}

// phpRoot returns the absolute PHP root and its project relative path.
func (m *Module) phpRoot() (string, string) {
	app := m.application()
	root, phpRoot := app.ProjectRoot(), app.PHPRoot()
	if phpRoot == "" {
		return root, "."
	}
	rel, err := filepath.Rel(root, phpRoot)
	if err != nil || strings.HasPrefix(rel, "..") {
		return root, "."
	}
	return phpRoot, filepath.ToSlash(rel)
}

// computePlan collects files, facts and the entry point inventory of
// scope (nil: the whole project; that plan is cached until files
// change).
func (m *Module) computePlan(ctx context.Context, scope []string) (*planned, error) {
	dir, err := m.dir()
	if err != nil {
		return nil, err
	}
	if len(scope) == 0 {
		m.mu.Lock()
		c := m.planCache
		m.mu.Unlock()
		if c != nil && c.dir == dir {
			return c, nil
		}
	}
	files, err := m.projectFiles(ctx)
	if err != nil {
		return nil, err
	}
	root := m.application().ProjectRoot()
	var rel []string
	for _, s := range scope {
		if filepath.IsAbs(s) {
			r, err := filepath.Rel(root, s)
			if err != nil || strings.HasPrefix(r, "..") {
				return nil, api.Errorf(api.CodeInvalidParams, "scope %s is outside the project", s)
			}
			s = r
		}
		rel = append(rel, filepath.ToSlash(s))
	}
	files = kn.FilterFiles(files, rel, setting(m, SettingExclude, kn.DefaultExclude))
	phpAbs, phpRel := m.phpRoot()
	facts := kn.GatherFacts(root, phpAbs, files)
	entries := kn.ExtractEntryPoints(root, phpRel, files)
	facts.DeriveStyle(entries)
	p := &planned{dir: dir, planner: kn.Planner{Root: root, Dir: dir, Facts: facts, Files: files, Entries: entries,
		MaxProcesses: setting(m, SettingMaxProcesses, kn.DefaultMaxProcesses), MaxComponents: setting(m, SettingMaxComponents, kn.DefaultMaxComponents)}}
	if len(scope) == 0 {
		m.mu.Lock()
		m.planCache = p
		m.mu.Unlock()
	}
	return p, nil
}

// state is the knowledge directory as it is now: manifest and questions.
type state struct {
	man *kn.Manifest
	qs  *kn.QuestionStore
}

func (m *Module) loadState(dir string) state {
	man, err := kn.LoadManifest(dir)
	if err != nil {
		m.application().Logger().Warn("knowledge", "err", err)
	}
	qs, err := kn.LoadQuestions(dir)
	if err != nil {
		m.application().Logger().Warn("knowledge", "err", err)
	}
	return state{man, qs}
}

// levelItem is a planned item with its staleness.
type levelItem struct {
	it    kn.Item
	stale string
}

// levelPlan is the plan of one level.
type levelPlan struct {
	level   string
	items   []levelItem
	skipped int
}

// planLevels plans the given levels against the current state.
func planLevels(pl *planned, s state, levels []string, all bool) []levelPlan {
	var out []levelPlan
	for _, l := range levels {
		items, skipped := pl.planner.Items(l, s.man, s.qs, all)
		lp := levelPlan{level: l, skipped: skipped}
		for _, it := range items {
			why, _ := pl.planner.Stale(&it, s.man)
			lp.items = append(lp.items, levelItem{it, why})
		}
		out = append(out, lp)
	}
	return out
}

// unitInfo describes a page for the API.
func unitInfo(dir string, it kn.Item, stale string, st *kn.ItemState) api.KnowledgeUnit {
	ku := api.KnowledgeUnit{ID: it.ID, Title: it.Title, Level: it.Level, Group: it.Group, Dir: it.Group, Files: len(it.Files), Stale: stale}
	if st != nil {
		if st.MD != "" {
			ku.MD, ku.Path = filepath.Join(dir, filepath.FromSlash(st.MD)), st.MD
		}
		ku.Confidence, ku.GeneratedAt, ku.Commit, ku.Unverified = st.Confidence, st.GeneratedAt, st.Commit, st.Unverified
		ku.Summary, ku.Mechanical = st.Summary, st.Mechanical
		if n := countSources(st.Files); n > ku.Files {
			ku.Files = n
		}
		if st.Status == kn.StatusFailed {
			ku.Error = st.Error
		}
	}
	return ku
}

func countSources(files map[string]string) int {
	n := 0
	for f := range files {
		if !strings.HasPrefix(f, "@") {
			n++
		}
	}
	return n
}

// estimateLevel estimates the agent work of a level: two calls per page
// (describe, validate); the agents read the sources about twice plus a
// few thousand tokens of prompt and answer per page.
func estimateLevel(lp levelPlan, pages []kn.Item) api.KnowledgeLevelEstimate {
	e := api.KnowledgeLevelEstimate{Level: lp.level, Title: kn.LevelTitle(lp.level), Pages: len(pages), Total: len(lp.items), Calls: 2 * len(pages), Skipped: lp.skipped}
	var bytes int64
	for _, it := range pages {
		bytes += it.Bytes
	}
	e.InputTokens = int(bytes/4)*2 + len(pages)*4000
	return e
}

func (m *Module) estimate(levels []api.KnowledgeLevelEstimate) api.KnowledgeEstimate {
	line, _ := m.agentCommand()
	e := api.KnowledgeEstimate{Agent: line, Model: m.agentModel(), Levels: levels}
	for _, l := range levels {
		e.Units += l.Pages
		e.Calls += l.Calls
		e.InputTokens += l.InputTokens
	}
	model := e.Model
	if model == "" {
		model = "the agent's default model"
	}
	later := ""
	for _, l := range levels {
		if l.Later {
			later = " Components and questions are planned when the run reaches them; their numbers are an upper bound."
		}
	}
	e.Note = fmt.Sprintf("Runs %s with %s: about %d agent calls reading ~%dk tokens of source.%s The tokens are billed by your agent's provider. The project is only read, never changed.",
		line, model, e.Calls, (e.InputTokens+999)/1000, later)
	return e
}

// buildPlan computes what a build of params would run.
func (m *Module) buildPlan(ctx context.Context, p api.KnowledgeScopeParams, only []string) (*planned, []string, api.KnowledgePlan, error) {
	levels, err := kn.ParseLevels(p.Level)
	if err != nil {
		return nil, nil, api.KnowledgePlan{}, api.Errorf(api.CodeInvalidParams, "%v", err)
	}
	pl, err := m.computePlan(ctx, p.Scope)
	if err != nil {
		return nil, nil, api.KnowledgePlan{}, err
	}
	s := m.loadState(pl.dir)
	res := api.KnowledgePlan{Units: []api.KnowledgeUnit{}}
	var ests []api.KnowledgeLevelEstimate
	pending := map[string]int{} // level → pages to build before it
	for _, lp := range planLevels(pl, s, levels, p.All) {
		var pages []kn.Item
		for _, li := range lp.items {
			if len(only) > 0 && !slices.Contains(only, li.it.ID) {
				continue
			}
			if li.stale == "" && !p.All && len(only) == 0 {
				continue
			}
			pages = append(pages, li.it)
			res.Units = append(res.Units, unitInfo(pl.dir, li.it, li.stale, s.man.Items[li.it.ID]))
		}
		res.Total += len(lp.items)
		e := estimateLevel(lp, pages)
		switch lp.level {
		case kn.LevelComponents:
			if n := pending[kn.LevelProcesses]; n > 0 && len(only) == 0 {
				// Re-planned after the processes ran: at most one page per
				// class, bounded by the setting.
				e.Later = true
				upper := min(pl.planner.MaxComponents, max(len(lp.items), n))
				e.Pages, e.Calls, e.InputTokens = upper, 2*upper, upper*8000
			}
		case kn.LevelQuestions:
			if pending[kn.LevelOverview]+pending[kn.LevelEntryPoints]+pending[kn.LevelProcesses]+pending[kn.LevelComponents] > 0 && len(only) == 0 {
				e.Later = true
				upper := max(e.Pages, 1)
				e.Pages, e.Calls, e.InputTokens = upper, 2*upper, upper*6000
			}
		}
		pending[lp.level] = e.Pages
		ests = append(ests, e)
	}
	res.Estimate = m.estimate(ests)
	return pl, levels, res, nil
}

// plan is knowledge.plan.
func (m *Module) plan(ctx context.Context, p api.KnowledgeScopeParams) (api.KnowledgePlan, error) {
	_, _, res, err := m.buildPlan(ctx, p, nil)
	return res, err
}

// list is knowledge.list: the pages of all levels (planned and built),
// in level order; questions.md as the last entry when it exists.
func (m *Module) list(ctx context.Context, _ struct{}) ([]api.KnowledgeUnit, error) {
	pl, err := m.computePlan(ctx, nil)
	if err != nil {
		return nil, err
	}
	s := m.loadState(pl.dir)
	out := []api.KnowledgeUnit{}
	seen := map[string]bool{}
	for _, lp := range planLevels(pl, s, kn.Levels[:4], false) {
		for _, li := range lp.items {
			seen[li.it.ID] = true
			out = append(out, unitInfo(pl.dir, li.it, li.stale, s.man.Items[li.it.ID]))
		}
		// Pages built earlier that are no longer planned (scope, removed
		// code) stay readable until rebuilt.
		for _, st := range s.man.Level(lp.level) {
			if !seen[st.ID] && st.MD != "" {
				out = append(out, unitInfo(pl.dir, kn.Item{ID: st.ID, Level: st.Level, Title: st.Title, Group: st.Group}, "", st))
			}
		}
	}
	if _, err := os.Stat(filepath.Join(pl.dir, "questions.md")); err == nil {
		open, answered := s.qs.Counts()
		out = append(out, api.KnowledgeUnit{ID: "questions", Title: "Open questions", Level: kn.LevelQuestions, MD: filepath.Join(pl.dir, "questions.md"), Path: "questions.md",
			Summary: fmt.Sprintf("%d open, %d answered", open, answered), Confidence: 1})
	}
	return out, nil
}

// status is knowledge.status.
func (m *Module) status(ctx context.Context, _ struct{}) (api.KnowledgeStatus, error) {
	return m.statusNow(ctx)
}

func (m *Module) statusNow(ctx context.Context) (api.KnowledgeStatus, error) {
	var st api.KnowledgeStatus
	dir, err := m.dir()
	if err != nil {
		return st, err
	}
	line, _ := m.agentCommand()
	cfg := m.embedConfig("")
	st.Dir, st.Agent, st.Model = dir, line, m.agentModel()
	st.AgentMode = m.agentMode(ctx)
	st.GiveAgent = st.AgentMode != api.AgentContextOff
	st.Embedding = api.KnowledgeEmbedding{Provider: cfg.Provider, Endpoint: cfg.Endpoint, Model: cfg.Model, Local: cfg.Provider == kn.ProviderOllama && localEndpoint(cfg.Endpoint)}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err == nil {
		st.Exists = true
	}
	if pl, err := m.computePlan(ctx, nil); err == nil {
		s := m.loadState(pl.dir)
		st.LastRun, st.Legacy = s.man.LastRun, s.man.Legacy
		for _, lp := range planLevels(pl, s, kn.Levels[:4], false) {
			ls := api.KnowledgeLevelStatus{Level: lp.level, Title: kn.LevelTitle(lp.level), Planned: len(lp.items)}
			for _, li := range lp.items {
				if li.stale != "" {
					ls.Stale++
				}
			}
			for _, is := range s.man.Level(lp.level) {
				if is.MD != "" {
					ls.Pages++
				}
				if is.Status == kn.StatusFailed {
					ls.Failed++
				}
			}
			st.Units += ls.Pages
			st.Stale += ls.Stale
			st.Failed += ls.Failed
			st.Levels = append(st.Levels, ls)
		}
		st.Exists = st.Exists || st.Units > 0
		st.Questions.Open, st.Questions.Answered = s.qs.Counts()
		st.Questions.Answerable = len(s.qs.Answerable())
	}
	if st.Exists {
		if s, err := m.openStore(); err == nil {
			if ss, err := s.Stats(ctx); err == nil {
				st.Embedding.Chunks, st.Embedding.Embedded, st.Embedding.Bytes = ss.Chunks, ss.Embedded, ss.Bytes
				st.Embedding.StoreModel, st.Embedding.Recovered = ss.Model, ss.Recovered
			}
		}
	}
	m.mu.Lock()
	st.Run = cloneRun(m.run)
	m.mu.Unlock()
	return st, nil
}

func cloneRun(r api.KnowledgeRun) api.KnowledgeRun {
	r.Units = slices.Clone(r.Units)
	r.Errors = slices.Clone(r.Errors)
	return r
}

func (m *Module) publishStatus() {
	if m.application() == nil || m.application().ProjectRoot() == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if st, err := m.statusNow(ctx); err == nil {
			m.publish(api.TopicKnowledgeStatus, st)
		}
	}()
}

// build is knowledge.build.
func (m *Module) build(ctx context.Context, p api.KnowledgeBuildParams) (api.KnowledgeRun, error) {
	app := m.application()
	root, err := app.RequireProject()
	if err != nil {
		return api.KnowledgeRun{}, err
	}
	m.mu.Lock()
	if m.run.Running {
		m.mu.Unlock()
		return api.KnowledgeRun{}, api.Errorf(api.CodeConflict, "a knowledge build is running")
	}
	m.mu.Unlock()
	m.invalidate()
	pl, levels, plan, err := m.buildPlan(ctx, p.KnowledgeScopeParams, p.Units)
	if err != nil {
		return api.KnowledgeRun{}, err
	}
	line, argv := m.agentCommand()
	var agentPath string
	if plan.Estimate.Units > 0 {
		if agentPath, err = m.resolve(argv[0]); err != nil {
			return api.KnowledgeRun{}, api.Errorf(api.CodeNotFound, "agent %q not found (Settings › AI › Agent)", argv[0])
		}
	}
	bctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	if m.run.Running {
		m.mu.Unlock()
		cancel()
		return api.KnowledgeRun{}, api.Errorf(api.CodeConflict, "a knowledge build is running")
	}
	started := time.Now()
	m.run = api.KnowledgeRun{Running: true, Phase: "agents", Started: started, Level: p.Level, ID: started.UTC().Format("20060102-150405.000")}
	m.act = newActivityLog(pl.dir, m.run.ID, m.publish, m.counters)
	for i, u := range plan.Units {
		m.run.Units = append(m.run.Units, api.KnowledgeProgress{Unit: u.ID, Level: u.Level, Title: u.Title, Index: i, Total: len(plan.Units), State: kn.RunQueued})
	}
	m.cancel, m.done = cancel, done
	m.mu.Unlock()
	m.publishStatus()
	work := kn.Work{Levels: levels, All: p.All, Only: p.Units}
	go func() {
		defer close(done)
		defer cancel()
		m.runBuild(bctx, root, line, argv, agentPath, work, pl, plan.Estimate.Units > 0, !p.NoEmbed)
	}()
	if !p.Wait {
		m.mu.Lock()
		defer m.mu.Unlock()
		return cloneRun(m.run), nil
	}
	select {
	case <-done:
	case <-ctx.Done():
		cancel()
		<-done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneRun(m.run), nil
}

// promptDir is the user's prompt override directory.
func (m *Module) promptDir() string {
	if cd := m.application().ConfigDir(); cd != "" {
		return filepath.Join(cd, "knowledge-prompts")
	}
	return ""
}

// runBuild runs the pipeline and the embedding (in the background).
func (m *Module) runBuild(ctx context.Context, root, line string, argv []string, agentPath string, work kn.Work, pl *planned, useAgent, embed bool) {
	app := m.application()
	finish := func(sum kn.Summary, errMsg string) {
		m.mu.Lock()
		act := m.act
		m.mu.Unlock()
		if act != nil {
			act.close() // last activity before the final status
		}
		m.mu.Lock()
		m.run.Running, m.run.Phase, m.run.Ended = false, "", time.Now()
		m.run.Done, m.run.Failed, m.run.Cancelled = sum.Done, sum.Failed, sum.Cancelled || m.run.Cancelled
		m.run.Answered = sum.Answered
		m.run.Errors = append(m.run.Errors, sum.Errors...)
		if errMsg != "" {
			m.run.Errors = append(m.run.Errors, errMsg)
		}
		m.cancel = nil
		m.mu.Unlock()
		m.invalidate()
		m.publishStatus()
	}
	var sum kn.Summary
	// The pipeline owns its copy of the manifest; readers load theirs
	// from disk (written after every page).
	man, _ := kn.LoadManifest(pl.dir)
	if useAgent {
		env := append(os.Environ(), "PATH="+m.searchPath())
		if strings.TrimSuffix(filepath.Base(argv[0]), ".exe") == "opencode" {
			env = append(env, "OPENCODE_CONFIG_CONTENT="+readOnlyConfig)
		}
		agent, err := startAgent(ctx, m.opts.Start, acp.Command{Path: agentPath, Args: argv[1:], Dir: root, Env: env}, root, m.agentModel())
		if err != nil {
			finish(sum, "start "+line+": "+err.Error())
			return
		}
		defer agent.Close()
		m.mu.Lock()
		agent.act = m.act
		m.mu.Unlock()
		prompts, perr := kn.LoadPrompts(m.promptDir())
		if perr != nil {
			sum.Errors = append(sum.Errors, perr.Error()+" (using the built-in prompts)")
			prompts, _ = kn.LoadPrompts("")
		}
		chk := &kn.Checker{Root: root, Lookup: m.lookupSymbol}
		pipe := &kn.Pipeline{Planner: pl.planner, Project: filepath.Base(root), Agent: agent, Checker: chk, Prompts: prompts,
			Commit: gitHead(ctx, root), Model: m.agentModel(), Progress: m.progress, OnItem: func(*kn.ItemState) { m.publishStatus() }}
		s := pipe.Run(ctx, work, man)
		s.Errors = append(sum.Errors, s.Errors...)
		sum = s
		sum.Errors = append(sum.Errors, agent.Notes()...)
	} else if err := kn.WriteIndex(pl.dir, filepath.Base(root), man, nil, root); err != nil {
		sum.Errors = append(sum.Errors, err.Error())
	}
	if ctx.Err() == nil && embed {
		m.mu.Lock()
		m.run.Phase = "embedding"
		m.mu.Unlock()
		m.publishStatus()
		if _, err := m.syncVectors(ctx, ""); err != nil {
			m.mu.Lock()
			m.run.EmbedError = err.Error()
			m.mu.Unlock()
			app.Logger().Info("knowledge embedding", "err", err)
		}
	}
	finish(sum, "")
}

// progress records and publishes a pipeline event.
func (m *Module) progress(e kn.Event) {
	p := api.KnowledgeProgress{Unit: e.Item, Level: e.Level, Title: e.Title, Index: e.Index, Total: e.Total, Step: e.Step, State: e.State, Error: e.Error, Confidence: e.Confidence, Attempt: e.Attempt,
		StepIndex: kn.StepIndex(e.Step), Steps: kn.StepCount}
	m.mu.Lock()
	act := m.act
	if e.Index >= 0 && e.Index < len(m.run.Units) && m.run.Units[e.Index].Unit == e.Item && e.State != kn.RunQueued {
		// Keep the page's start and counters across its steps.
		old := m.run.Units[e.Index]
		p.Started, p.Tools, p.Reads, p.Chars = old.Started, old.Tools, old.Reads, old.Chars
	}
	if e.State == kn.RunRunning && p.Started.IsZero() {
		p.Started = time.Now()
	}
	if e.Index >= 0 {
		// The run's list is rebuilt from the pipeline's events (later
		// levels are planned when they start).
		for len(m.run.Units) <= e.Index {
			m.run.Units = append(m.run.Units, api.KnowledgeProgress{Index: len(m.run.Units), State: kn.RunQueued})
		}
		if e.Index == 0 && e.State == kn.RunQueued {
			m.run.Units = m.run.Units[:0]
			m.run.Units = append(m.run.Units, p)
		} else {
			m.run.Units[e.Index] = p
		}
		for i := range m.run.Units {
			m.run.Units[i].Total = max(m.run.Units[i].Total, e.Total)
		}
	}
	m.mu.Unlock()
	if act != nil && e.State != kn.RunQueued {
		if s := stepText(e); s != "" {
			act.step(kn.StepInfo{Item: e.Item, Level: e.Level, Title: e.Title, Step: e.Step, Index: e.Index}, s, e.State == kn.RunRunning)
		}
	}
	m.publish(api.TopicKnowledgeProgress, p)
}

// counters records the live counters of the page at index (from the
// activity log, at most ten times a second) and publishes its progress.
func (m *Module) counters(index, tools, reads, chars int) {
	m.mu.Lock()
	if index < 0 || index >= len(m.run.Units) {
		m.mu.Unlock()
		return
	}
	u := &m.run.Units[index]
	u.Tools, u.Reads, u.Chars = tools, reads, chars
	p := *u
	m.mu.Unlock()
	m.publish(api.TopicKnowledgeProgress, p)
}

// lookupSymbol asks the PHP index where a symbol is declared.
func (m *Module) lookupSymbol(sym string) []string {
	app := m.application()
	q := strings.TrimSpace(sym)
	if i := strings.IndexByte(q, '('); i >= 0 {
		q = q[:i]
	}
	q = strings.TrimPrefix(strings.ReplaceAll(q, "->", "::"), `\`)
	if q == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var res struct {
		Symbols []struct {
			Path string `json:"path"`
			Name string `json:"name"`
			FQN  string `json:"fqn"`
		} `json:"symbols"`
	}
	libs := false
	if err := app.CallLocal(ctx, "nav.symbols", map[string]any{"query": q, "limit": 5, "libraries": &libs, "wait": true}, &res); err != nil {
		return nil
	}
	short := kn.ShortSymbol(q)
	var out []string
	for _, s := range res.Symbols {
		if strings.EqualFold(s.Name, short) && !slices.Contains(out, s.Path) {
			out = append(out, s.Path)
		}
	}
	return out
}

// cancelBuild is knowledge.cancel.
func (m *Module) cancelBuild(context.Context, struct{}) (api.KnowledgeRun, error) {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	if cancel != nil {
		m.run.Cancelled = true
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneRun(m.run), nil
}

// questions is knowledge.questions.
func (m *Module) questions(_ context.Context, p api.KnowledgeQuestionsParams) (api.KnowledgeQuestionsResult, error) {
	res := api.KnowledgeQuestionsResult{Questions: []api.KnowledgeQuestion{}}
	dir, err := m.dir()
	if err != nil {
		return res, err
	}
	switch p.Status {
	case "", kn.QuestionOpen, kn.QuestionAnswered:
	default:
		return res, api.Errorf(api.CodeInvalidParams, "status is open, answered or empty")
	}
	qs, _ := kn.LoadQuestions(dir)
	refs := func(l []kn.Ref) []string {
		var out []string
		for _, r := range l {
			s := r.File
			if r.Line > 0 {
				s = fmt.Sprintf("%s:%d", r.File, r.Line)
			}
			if r.Symbol != "" {
				s = strings.TrimSpace(s + " " + r.Symbol)
			}
			out = append(out, s)
		}
		return out
	}
	for _, q := range qs.Questions {
		if p.Status != "" && q.Status != p.Status {
			continue
		}
		res.Questions = append(res.Questions, api.KnowledgeQuestion{ID: q.ID, Text: q.Text, Level: q.Level, Item: q.Item, Refs: refs(q.Refs), Status: q.Status,
			Answer: q.Answer, AnsweredBy: q.AnsweredBy, AnswerRefs: refs(q.AnswerRefs), Attempts: q.Attempts})
	}
	res.Counts.Open, res.Counts.Answered = qs.Counts()
	res.Counts.Answerable = len(qs.Answerable())
	return res, nil
}
