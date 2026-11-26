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

// planned is a computed plan: the units of the scope with their file
// hashes and staleness.
type planned struct {
	units    []kn.Unit
	hashes   map[string]map[string]string
	stale    map[string]string
	manifest *kn.Manifest
	dir      string
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

// psr4 reads the PSR-4 map of the PHP root, relative to the project.
func (m *Module) psr4() map[string][]string {
	app := m.application()
	root, phpRoot := app.ProjectRoot(), app.PHPRoot()
	if phpRoot == "" {
		phpRoot = root
	}
	raw := kn.ReadPSR4(phpRoot)
	rel, err := filepath.Rel(root, phpRoot)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return raw
	}
	out := map[string][]string{}
	for ns, dirs := range raw {
		for _, d := range dirs {
			out[ns] = append(out[ns], path.Join(filepath.ToSlash(rel), d))
		}
	}
	return out
}

// computePlan plans the units of scope (nil: the whole project; the
// unscoped plan is cached until files change).
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
	units := kn.PlanUnits(files, kn.PlanOptions{
		PSR4: m.psr4(), Scope: rel,
		Exclude:  setting(m, SettingExclude, kn.DefaultExclude),
		MaxFiles: setting(m, SettingMaxFiles, 40),
	})
	man, merr := kn.LoadManifest(dir)
	if merr != nil {
		m.application().Logger().Warn("knowledge", "err", merr)
	}
	p := &planned{units: units, hashes: map[string]map[string]string{}, stale: map[string]string{}, manifest: man, dir: dir}
	for i := range p.units {
		u := &p.units[i]
		h := kn.HashFiles(root, u)
		p.hashes[u.ID] = h
		if s := man.Stale(dir, *u, h); s != "" {
			p.stale[u.ID] = s
		}
	}
	if len(scope) == 0 {
		m.mu.Lock()
		m.planCache = p
		m.mu.Unlock()
	}
	return p, nil
}

// unitInfo describes a unit for the API.
func unitInfo(dir string, u kn.Unit, stale string, st *kn.UnitState) api.KnowledgeUnit {
	ku := api.KnowledgeUnit{ID: u.ID, Title: u.Title, Dir: u.Dir, Files: len(u.Files), Stale: stale}
	if st != nil {
		if st.MD != "" {
			ku.MD = filepath.Join(dir, filepath.FromSlash(st.MD))
		}
		ku.Confidence, ku.GeneratedAt, ku.Commit, ku.Unverified = st.Confidence, st.GeneratedAt, st.Commit, st.Unverified
		if st.Status == kn.StatusFailed {
			ku.Error = st.Error
		}
	}
	return ku
}

// selectUnits returns the units a build runs.
func selectUnits(p *planned, all bool, ids []string) []kn.Unit {
	var out []kn.Unit
	for _, u := range p.units {
		if len(ids) > 0 && !slices.Contains(ids, u.ID) {
			continue
		}
		if all || len(ids) > 0 || p.stale[u.ID] != "" {
			out = append(out, u)
		}
	}
	return out
}

func (m *Module) estimate(units []kn.Unit) api.KnowledgeEstimate {
	line, _ := m.agentCommand()
	e := api.KnowledgeEstimate{Units: len(units), Calls: 3 * len(units), Agent: line, Model: m.agentModel()}
	var bytes int64
	for _, u := range units {
		e.Files += len(u.Files)
		bytes += u.Bytes
	}
	// The describing and the validating agent read the sources; prompts
	// and answers add a few thousand tokens per unit.
	e.InputTokens = int(bytes/4)*2 + len(units)*4000
	model := e.Model
	if model == "" {
		model = "the agent's default model"
	}
	e.Note = fmt.Sprintf("Runs %s with %s: about %d agent calls reading ~%dk tokens of source. The tokens are billed by your agent's provider. The project is only read, never changed.",
		line, model, e.Calls, (e.InputTokens+999)/1000)
	return e
}

// plan is knowledge.plan.
func (m *Module) plan(ctx context.Context, p api.KnowledgeScopeParams) (api.KnowledgePlan, error) {
	pl, err := m.computePlan(ctx, p.Scope)
	if err != nil {
		return api.KnowledgePlan{}, err
	}
	units := selectUnits(pl, p.All, nil)
	res := api.KnowledgePlan{Units: []api.KnowledgeUnit{}, Estimate: m.estimate(units), Total: len(pl.units)}
	for _, u := range units {
		res.Units = append(res.Units, unitInfo(pl.dir, u, pl.stale[u.ID], pl.manifest.Units[u.ID]))
	}
	return res, nil
}

// list is knowledge.list: all units of the project (planned and built).
func (m *Module) list(ctx context.Context, _ struct{}) ([]api.KnowledgeUnit, error) {
	pl, err := m.computePlan(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := []api.KnowledgeUnit{}
	seen := map[string]bool{}
	for _, u := range pl.units {
		seen[u.ID] = true
		out = append(out, unitInfo(pl.dir, u, pl.stale[u.ID], pl.manifest.Units[u.ID]))
	}
	// Units built earlier that are no longer planned (scope, folders
	// removed) stay searchable until rebuilt.
	for _, st := range pl.manifest.Sorted() {
		if !seen[st.ID] {
			files := make([]string, 0, len(st.Files))
			for f := range st.Files {
				files = append(files, f)
			}
			out = append(out, unitInfo(pl.dir, kn.Unit{ID: st.ID, Title: st.Title, Dir: st.Dir, Files: files}, "", st))
		}
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
	st.GiveAgent = setting(m, SettingGiveAgent, true)
	st.Embedding = api.KnowledgeEmbedding{Provider: cfg.Provider, Endpoint: cfg.Endpoint, Model: cfg.Model, Local: cfg.Provider == kn.ProviderOllama && localEndpoint(cfg.Endpoint)}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err == nil {
		st.Exists = true
	}
	if pl, err := m.computePlan(ctx, nil); err == nil {
		st.LastRun = pl.manifest.LastRun
		for _, s := range pl.manifest.Units {
			if s.MD != "" {
				st.Units++
			}
			if s.Status == kn.StatusFailed {
				st.Failed++
			}
		}
		st.Stale = len(pl.stale)
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
	pl, err := m.computePlan(ctx, p.Scope)
	if err != nil {
		return api.KnowledgeRun{}, err
	}
	units := selectUnits(pl, p.All, p.Units)
	line, argv := m.agentCommand()
	var agentPath string
	if len(units) > 0 {
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
	m.run = api.KnowledgeRun{Running: true, Phase: "agents", Started: time.Now()}
	for i, u := range units {
		m.run.Units = append(m.run.Units, api.KnowledgeProgress{Unit: u.ID, Title: u.Title, Index: i, Total: len(units), State: kn.RunQueued})
	}
	m.cancel, m.done = cancel, done
	m.mu.Unlock()
	m.publishStatus()
	go func() {
		defer close(done)
		defer cancel()
		m.runBuild(bctx, root, line, argv, agentPath, units, pl, !p.NoEmbed)
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

// runBuild runs the pipeline and the embedding (in the background).
func (m *Module) runBuild(ctx context.Context, root, line string, argv []string, agentPath string, units []kn.Unit, pl *planned, embed bool) {
	app := m.application()
	finish := func(sum kn.Summary, errMsg string) {
		m.mu.Lock()
		m.run.Running, m.run.Phase, m.run.Ended = false, "", time.Now()
		m.run.Done, m.run.Failed, m.run.Cancelled = sum.Done, sum.Failed, sum.Cancelled || m.run.Cancelled
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
	// The pipeline owns its own copy of the manifest (the cached plan's
	// copy is read concurrently by status calls).
	man, _ := kn.LoadManifest(pl.dir)
	if len(units) > 0 {
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
		chk := &kn.Checker{Root: root, Lookup: m.lookupSymbol}
		pipe := &kn.Pipeline{Root: root, Dir: pl.dir, Project: filepath.Base(root), Agent: agent, Checker: chk,
			Commit: gitHead(ctx, root), Model: m.agentModel(), Progress: m.progress}
		sum = pipe.Run(ctx, units, man)
		sum.Errors = append(sum.Errors, agent.Notes()...)
	} else if err := kn.WriteIndex(pl.dir, filepath.Base(root), man); err != nil {
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
	p := api.KnowledgeProgress{Unit: e.Unit, Title: e.Title, Index: e.Index, Total: e.Total, Step: e.Step, State: e.State, Error: e.Error, Confidence: e.Confidence, Attempt: e.Attempt}
	m.mu.Lock()
	if e.Index >= 0 && e.Index < len(m.run.Units) {
		m.run.Units[e.Index] = p
	}
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
