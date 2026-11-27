package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// Limits of the transcript.
const (
	maxInput  = 2000
	maxOutput = 4000
)

// handler serves the calls of one agent process (gen).
type handler struct {
	m   *Module
	gen int
}

func (h *handler) current() bool {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	return h.m.gen == h.gen
}

// ---- transcript -----------------------------------------------------------

// addEntry appends e (callers hold mu) and returns its index.
func (m *Module) addEntry(e api.AgentEntry) int {
	e.Index = len(m.entries)
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	m.entries = append(m.entries, &e)
	return e.Index
}

// entryEvent copies entry i for an event (callers hold mu).
func (m *Module) entryEvent(i int, delta string) api.AgentUpdateEvent {
	e := *m.entries[i]
	if e.Tool != nil {
		t := *e.Tool
		e.Tool = &t
	}
	if e.Permission != nil {
		p := *e.Permission
		e.Permission = &p
	}
	e.Plan = append([]api.AgentPlanEntry(nil), e.Plan...)
	e.Context = slices.Clone(e.Context)
	e.Instructions = slices.Clone(e.Instructions)
	return api.AgentUpdateEvent{Entry: e, Delta: delta}
}

// notice adds a notice entry.
func (m *Module) notice(level, text string) {
	m.mu.Lock()
	i := m.addEntry(api.AgentEntry{Kind: api.EntryNotice, Level: level, Text: text})
	m.open = -1
	ev := m.entryEvent(i, "")
	m.mu.Unlock()
	m.publish(api.TopicAgentUpdate, ev)
}

// transcript is agent.transcript.
func (m *Module) transcript(context.Context, struct{}) ([]api.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.AgentEntry, len(m.entries))
	for i := range m.entries {
		out[i] = m.entryEvent(i, "").Entry
	}
	return out, nil
}

// SessionUpdate implements acp.Handler.
func (h *handler) SessionUpdate(n acp.SessionNotification) {
	if !h.current() {
		return
	}
	m := h.m
	u := n.Update
	switch u.Kind {
	case acp.UpdateAgentMessage, acp.UpdateAgentThought:
		c := u.Chunk()
		if c.Type != "text" || c.Text == "" {
			return
		}
		kind := api.EntryAgent
		if u.Kind == acp.UpdateAgentThought {
			kind = api.EntryThought
		}
		m.mu.Lock()
		if m.open < 0 || m.entries[m.open].Kind != kind {
			m.open = m.addEntry(api.AgentEntry{Kind: kind})
		}
		m.entries[m.open].Text += c.Text
		ev := m.entryEvent(m.open, c.Text)
		m.mu.Unlock()
		m.publish(api.TopicAgentUpdate, ev)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		m.toolUpdate(u.ToolCall())
	case acp.UpdatePlan:
		var plan []api.AgentPlanEntry
		for _, p := range u.Plan() {
			plan = append(plan, api.AgentPlanEntry{Content: p.Content, Status: p.Status})
		}
		m.mu.Lock()
		if m.plan < 0 {
			m.plan = m.addEntry(api.AgentEntry{Kind: api.EntryPlan})
			m.open = -1
		}
		m.entries[m.plan].Plan = plan
		ev := m.entryEvent(m.plan, "")
		m.mu.Unlock()
		m.publish(api.TopicAgentUpdate, ev)
	case acp.UpdateCommands:
		var names []string
		for _, c := range u.Commands() {
			names = append(names, c.Name)
		}
		m.mu.Lock()
		m.commands = names
		m.mu.Unlock()
		m.publishStatus()
	case acp.UpdateConfigOptions:
		if opts := u.ConfigOptions(); len(opts) > 0 {
			m.mu.Lock()
			m.options = opts
			m.mu.Unlock()
			m.publishStatus()
		}
	}
}

// toolUpdate merges a tool call report into its transcript entry and
// finishes the change tracking of completed edits.
func (m *Module) toolUpdate(tc acp.ToolCall) {
	if tc.ToolCallID == "" {
		return
	}
	// Edits the agent made without asking (its own configuration allowed
	// them): the diff's old text is the original.
	m.syncChanges()
	for _, c := range tc.Content {
		if c.Type == "diff" && c.Path != "" {
			abs := m.abs(c.Path)
			m.mu.Lock()
			if !m.changes.tracking(abs) {
				orig, existed := []byte(nil), c.OldText != nil
				if existed {
					orig = []byte(*c.OldText)
				}
				m.changes.before(abs, orig, existed)
			}
			m.addToolPath(tc.ToolCallID, abs)
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	i, ok := m.tools[tc.ToolCallID]
	if !ok {
		i = m.addEntry(api.AgentEntry{Kind: api.EntryTool, Tool: &api.AgentToolCall{ID: tc.ToolCallID}})
		m.tools[tc.ToolCallID] = i
		m.open = -1
	}
	m.mergeTool(m.entries[i].Tool, tc)
	done := tc.Status == acp.StatusCompleted || tc.Status == acp.StatusFailed
	var paths []string
	if done {
		paths = m.toolPaths[tc.ToolCallID]
		delete(m.toolPaths, tc.ToolCallID)
	}
	title := m.entries[i].Tool.Title
	ev := m.entryEvent(i, "")
	m.mu.Unlock()
	m.publish(api.TopicAgentUpdate, ev)
	if len(paths) > 0 {
		m.finishEdits(paths, title)
	}
}

// addToolPath remembers that tool call id touches abs (callers hold mu).
func (m *Module) addToolPath(id, abs string) {
	for _, p := range m.toolPaths[id] {
		if p == abs {
			return
		}
	}
	m.toolPaths[id] = append(m.toolPaths[id], abs)
}

// mergeTool applies the reported fields of tc to t (callers hold mu).
func (m *Module) mergeTool(t *api.AgentToolCall, tc acp.ToolCall) {
	if tc.Name != "" {
		t.Name = tc.Name
	}
	if tc.Title != "" {
		t.Title = tc.Title
	}
	if tc.Kind != "" {
		t.Kind = tc.Kind
	}
	if tc.Status != "" {
		t.Status = tc.Status
	}
	if len(tc.Locations) > 0 {
		t.Paths = nil
		for _, l := range tc.Locations {
			t.Paths = appendUnique(t.Paths, m.display(l.Path))
		}
	}
	if len(tc.RawInput) > 0 && string(tc.RawInput) != "{}" {
		t.Input = shorten(string(tc.RawInput), maxInput)
		if c := commandOf(tc); c != "" {
			t.Command = c
		}
	}
	if tc.Content != nil {
		var out []string
		t.Diffs = nil
		for _, c := range tc.Content {
			switch {
			case c.Type == "content" && c.Content != nil && c.Content.Text != "":
				out = append(out, c.Content.Text)
			case c.Type == "diff":
				var old, cur []byte
				if c.OldText != nil {
					old = []byte(*c.OldText)
				}
				if c.NewText != nil {
					cur = []byte(*c.NewText)
				}
				a, r := diffStat(old, cur)
				t.Diffs = append(t.Diffs, api.AgentDiffStat{Path: m.display(c.Path), Added: a, Removed: r})
				t.Paths = appendUnique(t.Paths, m.display(c.Path))
			}
		}
		if len(out) > 0 {
			t.Output = shorten(strings.Join(out, "\n"), maxOutput)
		}
	}
	if t.Title == "" {
		t.Title = t.Name
	}
}

func appendUnique(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + " …"
}

// abs resolves a path of the agent (absolute, or relative to the
// project).
func (m *Module) abs(p string) string {
	app := m.application()
	if app == nil {
		return filepath.Clean(p)
	}
	a, err := app.ResolvePath(p)
	if err != nil {
		return filepath.Clean(p)
	}
	// The agent may name the project through a symlink (macOS /var →
	// /private/var): use the real path when it lies in the project.
	if root := app.ProjectRoot(); root != "" && !inside(root, a) {
		if real := realPath(a); inside(root, real) {
			return real
		}
	}
	return a
}

// inside reports whether p lies in dir.
func inside(dir, p string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// realPath resolves symlinks of p, or of its directory when p does not
// exist (yet).
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if d, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(d, filepath.Base(p))
	}
	return p
}

func (m *Module) display(p string) string {
	if p == "" {
		return ""
	}
	if app := m.application(); app != nil {
		return app.DisplayPath(m.abs(p))
	}
	return p
}

// ---- prompt ---------------------------------------------------------------

// prompt is agent.prompt.
func (m *Module) prompt(ctx context.Context, p api.AgentPromptParams) (api.AgentTurnResult, error) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return api.AgentTurnResult{}, api.Errorf(api.CodeInvalidParams, "text is required")
	}
	switch p.Ask {
	case "", api.PolicyAllow, api.PolicyDeny:
	default:
		return api.AgentTurnResult{}, api.Errorf(api.CodeInvalidParams, "ask must be allow, deny or empty")
	}
	m.mu.Lock()
	busy := m.turnCancel != nil
	m.mu.Unlock()
	if busy {
		return api.AgentTurnResult{}, api.Errorf(api.CodeConflict, "the agent is still working on the previous message (agent.cancel ends it)")
	}
	if err := m.ensure(ctx); err != nil {
		return api.AgentTurnResult{}, err
	}
	blocks, pc, err := m.promptBlocks(ctx, text, p.Files)
	if err != nil {
		return api.AgentTurnResult{}, err
	}
	tctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	if m.turnCancel != nil {
		m.mu.Unlock()
		cancel()
		return api.AgentTurnResult{}, api.Errorf(api.CodeConflict, "the agent is still working on the previous message")
	}
	proc, sid := m.proc, m.sessionID
	m.turnCancel, m.turnDone, m.turnAsk = cancel, done, p.Ask
	m.state = api.AgentPrompting
	i := m.addEntry(api.AgentEntry{Kind: api.EntryUser, Text: text, Context: pc.Blocks, Instructions: pc.Instructions})
	m.open, m.plan = -1, -1
	ev := m.entryEvent(i, "")
	m.mu.Unlock()
	m.publish(api.TopicAgentUpdate, ev)
	m.publishStatus()

	var res api.AgentTurnResult
	go func() {
		r, err := proc.Prompt(tctx, sid, blocks)
		res = api.AgentTurnResult{StopReason: r.StopReason}
		if err != nil {
			res.Error = err.Error()
			if tctx.Err() != nil {
				res = api.AgentTurnResult{StopReason: acp.StopCancelled}
			}
		}
		res.Context = pc.Blocks
		m.mu.Lock()
		userCancel := m.userCancel
		m.turnCancel, m.turnDone, m.turnAsk, m.userCancel = nil, nil, "", false
		res.ByUser = userCancel || tctx.Err() != nil
		m.lastStop = res
		if m.state == api.AgentPrompting {
			m.state = api.AgentReady
		}
		m.mu.Unlock()
		cancel()
		if res.Error != "" {
			m.notice(api.NoticeError, "The agent failed: "+res.Error)
		} else if r.StopReason == acp.StopCancelled && !userCancel {
			m.notice(api.NoticeInfo, "The agent ended its turn (e.g. after a rejected request).")
		} else if r.StopReason == acp.StopMaxTokens || r.StopReason == acp.StopRefusal {
			m.notice(api.NoticeInfo, "The agent stopped: "+strings.ReplaceAll(r.StopReason, "_", " ")+".")
		}
		m.publishStatus()
		m.publish(api.TopicAgentTurnEnded, res)
		close(done)
	}()
	if !p.Wait {
		return api.AgentTurnResult{Started: true}, nil
	}
	select {
	case <-done:
	case <-ctx.Done():
		m.cancelTurn()
		<-done
	}
	return res, nil
}

// promptBlocks builds the prompt: the rules preamble with the first
// message of a session, the instructions and context blocks of other
// modules (HookAgentPromptContext, D571), the attached files and the
// text.
func (m *Module) promptBlocks(ctx context.Context, text string, files []string) ([]acp.ContentBlock, api.AgentPromptContext, error) {
	var blocks []acp.ContentBlock
	app := m.application()
	var data [][]byte
	for _, f := range files {
		d, _, err := app.ReadText(f)
		if err != nil {
			return nil, api.AgentPromptContext{}, api.Errorf(api.CodeNotFound, "attach %s: %v", f, err)
		}
		data = append(data, d)
	}
	m.mu.Lock()
	first := !m.rulesOut
	m.rulesOut = true
	m.mu.Unlock()
	if first {
		blocks = append(blocks, acp.TextBlock(rulesPreamble(m.policy())))
	}
	pc := m.promptContext(ctx, text, first)
	for _, in := range pc.Instructions {
		blocks = append(blocks, acp.TextBlock(in))
	}
	for _, b := range pc.Blocks {
		blocks = append(blocks, acp.TextBlock(b.Text))
	}
	for i, f := range files {
		abs, _ := app.ResolvePath(f)
		blocks = append(blocks, acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
			URI: "file://" + filepath.ToSlash(abs), Text: string(data[i]), MimeType: "text/plain"}})
	}
	return append(blocks, acp.TextBlock(text)), pc, nil
}

// promptContext asks other modules for context of a prompt; failures
// leave the prompt without it.
func (m *Module) promptContext(ctx context.Context, text string, first bool) api.AgentPromptContext {
	app := m.application()
	in := api.AgentPromptContext{Prompt: text, First: first}
	if app == nil {
		return in
	}
	pc, err := api.HookAgentPromptContext.Apply(m.hookCtx(ctx), app.Hooks(), in)
	if err != nil {
		app.Logger().Warn("agent prompt context", "err", err)
	}
	out := api.AgentPromptContext{Prompt: text, First: first}
	for _, s := range pc.Instructions {
		if strings.TrimSpace(s) != "" {
			out.Instructions = append(out.Instructions, s)
		}
	}
	for _, b := range pc.Blocks {
		if strings.TrimSpace(b.Text) != "" {
			out.Blocks = append(out.Blocks, b)
		}
	}
	return out
}

// ---- permissions ----------------------------------------------------------

// RequestPermission implements acp.Handler: the settings decide, "ask"
// waits for the user (agent.permission).
func (h *handler) RequestPermission(ctx context.Context, r acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if !h.current() {
		return acp.Cancelled(), nil
	}
	m := h.m
	tc := r.ToolCall
	m.mu.Lock()
	if i, ok := m.tools[tc.ToolCallID]; ok && tc.Name == "" {
		tc.Name = m.entries[i].Tool.Name
	}
	ask := m.turnAsk
	m.mu.Unlock()
	if m.readOnlyTool(tc) {
		// Tools of read-only MCP servers (the project knowledge) are local
		// and only read: allowed without asking (D571).
		if id := allowOption(r.Options); id != "" {
			return acp.Selected(id), nil
		}
		return acp.Cancelled(), nil
	}
	cat, verb := category(tc)
	pol := decide(m.policy(), cat)
	if m.evalPol.Load() != nil {
		pol = evalDecision(cat, tc)
	}
	key := alwaysKey(cat, tc)
	m.mu.Lock()
	if key != "" && m.always[key] && pol == api.PolicyAsk {
		pol = api.PolicyAllow
	}
	m.mu.Unlock()
	reason := "disabled in settings"
	if m.evalPol.Load() != nil {
		reason = "read-only evaluation"
	}
	if pol == api.PolicyAsk && ask != "" {
		pol, reason = ask, "needs confirmation, nobody asked"
	}
	tool := api.AgentToolCall{ID: tc.ToolCallID}
	m.mergeToolLocked(&tool, tc)
	summary := permSummary(cat, verb, tool)
	switch pol {
	case api.PolicyAllow:
		id := allowOption(r.Options)
		if id == "" {
			return acp.Cancelled(), nil
		}
		if cat == api.AgentCatEdit {
			m.prepareEdit(ctx, tc)
		}
		return acp.Selected(id), nil
	case api.PolicyDeny:
		what := verb
		if what == "" {
			what = label(cat)
		}
		m.notice(api.NoticeBlocked, fmt.Sprintf("Blocked: %s (%s) – %s", what, reason, summary))
		if id := rejectOption(r.Options); id != "" {
			return acp.Selected(id), nil
		}
		return acp.Cancelled(), nil
	}
	// Ask the user.
	var opts []api.AgentPermOption
	for _, o := range r.Options {
		opts = append(opts, api.AgentPermOption{ID: o.OptionID, Name: o.Name, Kind: o.Kind})
	}
	m.mu.Lock()
	m.permSeq++
	reqID := "p" + strconv.Itoa(m.permSeq)
	perm := &api.AgentPermission{RequestID: reqID, Category: cat, Summary: summary, Tool: tool, Options: opts}
	i := m.addEntry(api.AgentEntry{Kind: api.EntryPermission, Permission: perm})
	m.open = -1
	pp := &pendingPerm{entry: i, req: r, cat: cat, ch: make(chan string, 1)}
	m.perms[reqID] = pp
	ev := m.entryEvent(i, "")
	m.mu.Unlock()
	m.publish(api.TopicAgentUpdate, ev)
	m.publish(api.TopicAgentPermission, *ev.Entry.Permission)
	m.publishStatus()

	var answer string
	select {
	case answer = <-pp.ch:
	case <-ctx.Done():
		answer = acp.OutcomeCancelled
	}
	m.mu.Lock()
	delete(m.perms, reqID)
	kind := optionKind(r.Options, answer)
	allowed := kind == acp.AllowOnce || kind == acp.AllowAlways
	if kind == acp.AllowAlways && key != "" {
		m.always[key] = true
	}
	e := m.entries[i].Permission
	e.Answer, e.By, e.Allowed = answer, "user", allowed
	ev = m.entryEvent(i, "")
	m.mu.Unlock()
	m.publish(api.TopicAgentUpdate, ev)
	m.publishStatus()
	if kind == "" {
		return acp.Cancelled(), nil
	}
	if allowed {
		if cat == api.AgentCatEdit {
			m.prepareEdit(ctx, tc)
		}
		// Tiegel keeps "always" itself: the agent keeps asking.
		if id := allowOption(r.Options); id != "" {
			return acp.Selected(id), nil
		}
	}
	if !allowed {
		if id := rejectOption(r.Options); id != "" {
			return acp.Selected(id), nil
		}
	}
	return acp.Selected(answer), nil
}

// mergeToolLocked is mergeTool taking mu.
func (m *Module) mergeToolLocked(t *api.AgentToolCall, tc acp.ToolCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mergeTool(t, tc)
}

// permSummary says in words what the agent wants.
func permSummary(cat, verb string, t api.AgentToolCall) string {
	paths := strings.Join(t.Paths, ", ")
	switch cat {
	case api.AgentCatShell, api.AgentCatGitCommit, api.AgentCatGitPush:
		if t.Command != "" {
			return "Run: " + t.Command
		}
		if verb != "" {
			return "Run: " + verb
		}
		return "Run a command"
	case api.AgentCatEdit:
		if paths != "" {
			return "Change " + paths
		}
		return "Change files"
	case api.AgentCatWeb:
		if u := inputField(json.RawMessage(t.Input), "url", "query"); u != "" {
			return "Web: " + u
		}
		return "Access the web"
	}
	s := t.Title
	if s == "" {
		s = t.Name
	}
	if paths != "" && !strings.Contains(s, paths) {
		s += " (" + paths + ")"
	}
	return "Use " + s
}

// permission is agent.permission.
func (m *Module) permission(_ context.Context, p api.AgentPermissionParams) (api.AgentStatus, error) {
	m.mu.Lock()
	pp := m.perms[p.RequestID]
	if pp == nil {
		m.mu.Unlock()
		return m.statusNow(), api.Errorf(api.CodeNotFound, "no pending permission request %q", p.RequestID)
	}
	answer := p.OptionID
	switch {
	case answer != "":
		if optionKind(pp.req.Options, answer) == "" {
			m.mu.Unlock()
			return m.statusNow(), api.Errorf(api.CodeInvalidParams, "unknown option %q", answer)
		}
	case p.Allow:
		answer = allowOption(pp.req.Options)
	case p.Deny:
		answer = rejectOption(pp.req.Options)
	}
	if answer == "" {
		m.mu.Unlock()
		return m.statusNow(), api.Errorf(api.CodeInvalidParams, "optionId, allow or deny is required")
	}
	delete(m.perms, p.RequestID)
	m.mu.Unlock()
	pp.ch <- answer
	return m.statusNow(), nil
}

// ---- change tracking ------------------------------------------------------

// editPaths returns the files an edit tool call changes.
func editPaths(tc acp.ToolCall) []string {
	var out []string
	for _, c := range tc.Content {
		if c.Type == "diff" && c.Path != "" {
			out = appendUnique(out, c.Path)
		}
	}
	for _, l := range tc.Locations {
		if l.Path != "" {
			out = appendUnique(out, l.Path)
		}
	}
	for _, f := range []string{"path", "filePath", "file_path", "file"} {
		if p := inputField(tc.RawInput, f); p != "" {
			out = appendUnique(out, p)
		}
	}
	return out
}

// prepareEdit runs before an allowed edit: unsaved changes of the files
// in the editor are saved (so the agent edits what the user sees and no
// external conflict arises) and their content is recorded for revert.
func (m *Module) prepareEdit(ctx context.Context, tc acp.ToolCall) {
	m.syncChanges()
	app := m.application()
	for _, p := range editPaths(tc) {
		abs := m.abs(p)
		if app != nil {
			if d, err := app.Document(abs); err == nil && d.Snapshot().Modified {
				_ = app.CallLocal(ctx, "doc.save", api.DocSaveParams{Path: abs}, nil)
			}
		}
		data, err := os.ReadFile(abs)
		exists := err == nil
		m.mu.Lock()
		m.changes.before(abs, data, exists)
		m.addToolPath(tc.ToolCallID, abs)
		m.mu.Unlock()
		if exists && app != nil {
			_ = app.RecordHistory(abs, data, "before agent change")
		}
	}
}

// finishEdits records the state after a completed edit, reloads open
// documents and updates the change list.
func (m *Module) finishEdits(paths []string, title string) {
	app := m.application()
	for _, abs := range paths {
		data, err := os.ReadFile(abs)
		exists := err == nil
		if app != nil {
			if exists {
				_ = app.RecordHistory(abs, data, "agent: "+shorten(title, 60))
			}
			_ = app.Exclusive(func() error {
				app.ReloadDocument(abs)
				return nil
			})
		}
		m.mu.Lock()
		m.changes.after(abs, data, exists)
		m.saveChangesLocked()
		m.mu.Unlock()
	}
	m.publishChanges()
}

func (m *Module) publishChanges() {
	m.publish(api.TopicAgentChanges, m.changeList())
	m.publishStatus()
}

// current returns the content of abs as the user sees it: the open
// document's text, else the file.
func (m *Module) currentContent(abs string) ([]byte, bool) {
	if app := m.application(); app != nil {
		if d, err := app.Document(abs); err == nil {
			if _, serr := os.Stat(abs); serr == nil || d.Snapshot().Modified {
				return d.Snapshot().Text, true
			}
		}
	}
	data, err := os.ReadFile(abs)
	return data, err == nil
}

func (m *Module) changeList() []api.AgentChange {
	m.syncChanges()
	m.mu.Lock()
	files := m.changes.changed()
	m.mu.Unlock()
	out := []api.AgentChange{}
	for _, f := range files {
		out = append(out, m.describe(f))
	}
	return out
}

func (m *Module) describe(f *tracked) api.AgentChange {
	cur, exists := m.currentContent(f.abs)
	a, r := diffStat(f.orig, cur)
	if !exists {
		a, r = diffStat(f.orig, nil)
	}
	return api.AgentChange{Path: m.display(f.abs), File: f.abs, Kind: f.kind(), Added: a, Removed: r, Conflict: f.conflict(cur, exists)}
}

// changesList is agent.changes.
func (m *Module) changesList(context.Context, struct{}) ([]api.AgentChange, error) {
	return m.changeList(), nil
}

// find returns the tracked file of path.
func (m *Module) find(path string) (*tracked, error) {
	if path == "" {
		return nil, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	abs := m.abs(path)
	m.syncChanges()
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.changes.files[abs]
	if f == nil || !f.seen {
		return nil, api.Errorf(api.CodeNotFound, "the agent did not change %s", path)
	}
	return f, nil
}

// diff is agent.diff.
func (m *Module) diff(_ context.Context, p api.AgentPathParams) (api.AgentDiff, error) {
	f, err := m.find(p.Path)
	if err != nil {
		return api.AgentDiff{}, err
	}
	cur, exists := m.currentContent(f.abs)
	d := api.AgentDiff{AgentChange: m.describe(f), Original: string(f.orig), Current: string(cur)}
	d.Unified = unified(d.Path, f.orig, cur, f.existed, exists)
	return d, nil
}

// accept is agent.acceptChanges.
func (m *Module) accept(context.Context, struct{}) ([]api.AgentChange, error) {
	m.syncChanges()
	m.mu.Lock()
	m.changes.clear()
	m.saveChangesLocked()
	m.mu.Unlock()
	m.publishChanges()
	return []api.AgentChange{}, nil
}

// revert is agent.revert.
func (m *Module) revert(ctx context.Context, p api.AgentRevertParams) (api.AgentRevertResult, error) {
	var files []*tracked
	m.syncChanges()
	switch {
	case p.All:
		m.mu.Lock()
		files = m.changes.changed()
		m.mu.Unlock()
	default:
		f, err := m.find(p.Path)
		if err != nil {
			return api.AgentRevertResult{}, err
		}
		files = []*tracked{f}
	}
	res := api.AgentRevertResult{Reverted: []string{}}
	for _, f := range files {
		path := m.display(f.abs)
		cur, exists := m.currentContent(f.abs)
		if !p.Force && f.conflict(cur, exists) {
			res.Conflicts = append(res.Conflicts, api.AgentConflict{Path: path, Reason: "changed after the agent's last change (revert with force to overwrite)"})
			continue
		}
		if err := m.restore(ctx, f); err != nil {
			res.Conflicts = append(res.Conflicts, api.AgentConflict{Path: path, Reason: err.Error()})
			continue
		}
		m.mu.Lock()
		m.changes.drop(f.abs)
		m.saveChangesLocked()
		m.mu.Unlock()
		res.Reverted = append(res.Reverted, path)
	}
	if len(res.Reverted) > 0 {
		m.notice(api.NoticeInfo, "Reverted: "+strings.Join(res.Reverted, ", "))
	}
	m.publishChanges()
	return res, nil
}

// restore writes the original state of f through the document layer:
// open documents change as one undoable edit and are saved, closed files
// get Local History revisions; created files are deleted.
func (m *Module) restore(ctx context.Context, f *tracked) error {
	app := m.application()
	if app == nil {
		return fmt.Errorf("module not started")
	}
	const label = "agent revert"
	if !f.existed {
		_, err := app.ApplyFileOps(ctx, label, []api.FileOp{{Kind: api.FileOpDelete, Path: f.abs, IgnoreIfNotExists: true}})
		return err
	}
	var ops []api.FileOp
	if _, err := os.Stat(f.abs); err != nil {
		ops = append(ops, api.FileOp{Kind: api.FileOpCreate, Path: f.abs, Overwrite: true})
	}
	orig := string(f.orig)
	ops = append(ops, api.FileOp{Kind: api.FileOpEdit, Path: f.abs, Changes: func(text []byte) ([]api.TextChange, error) {
		return []api.TextChange{{Start: 0, End: len(text), Text: orig}}, nil
	}})
	steps, err := app.ApplyFileOps(ctx, label, ops)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if s.Open {
			if err := app.CallLocal(ctx, "doc.save", api.DocSaveParams{Path: f.abs, Force: true}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- file access ----------------------------------------------------------

// ReadTextFile implements acp.Handler: open documents are read with their
// unsaved text.
func (h *handler) ReadTextFile(_ context.Context, r acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	app := h.m.application()
	if app == nil {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeInternalError, Message: "not ready"}
	}
	data, _, err := app.ReadText(h.m.abs(r.Path))
	if err != nil {
		return acp.ReadTextFileResponse{}, &acp.Error{Code: acp.CodeResourceNotFound, Message: err.Error()}
	}
	text := string(data)
	if r.Line != nil || r.Limit != nil {
		lines := strings.SplitAfter(text, "\n")
		from := 0
		if r.Line != nil && *r.Line > 1 {
			from = min(*r.Line-1, len(lines))
		}
		to := len(lines)
		if r.Limit != nil && *r.Limit >= 0 {
			to = min(from+*r.Limit, len(lines))
		}
		text = strings.Join(lines[from:to], "")
	}
	return acp.ReadTextFileResponse{Content: text}, nil
}

// WriteTextFile implements acp.Handler: the write goes through the
// document layer (open documents change visibly and are saved, Local
// History records it) and is tracked for revert.
func (h *handler) WriteTextFile(ctx context.Context, r acp.WriteTextFileRequest) error {
	m := h.m
	app := m.application()
	if app == nil {
		return &acp.Error{Code: acp.CodeInternalError, Message: "not ready"}
	}
	if m.evalPol.Load() != nil || decide(m.policy(), api.AgentCatEdit) == api.PolicyDeny {
		return &acp.Error{Code: acp.CodeInternalError, Message: "file changes are disabled in the IDE settings"}
	}
	abs := m.abs(r.Path)
	m.syncChanges()
	before, existed := m.currentContent(abs)
	m.mu.Lock()
	m.changes.before(abs, before, existed)
	m.mu.Unlock()
	var ops []api.FileOp
	if _, err := os.Stat(abs); err != nil {
		ops = append(ops, api.FileOp{Kind: api.FileOpCreate, Path: abs, Overwrite: true})
	}
	content := r.Content
	ops = append(ops, api.FileOp{Kind: api.FileOpEdit, Path: abs, Changes: func(text []byte) ([]api.TextChange, error) {
		return []api.TextChange{{Start: 0, End: len(text), Text: content}}, nil
	}})
	steps, err := app.ApplyFileOps(ctx, "agent write", ops)
	if err != nil {
		return &acp.Error{Code: acp.CodeInternalError, Message: err.Error()}
	}
	for _, s := range steps {
		if s.Open {
			_ = app.CallLocal(ctx, "doc.save", api.DocSaveParams{Path: abs, Force: true}, nil)
		}
	}
	data, rerr := os.ReadFile(abs)
	m.mu.Lock()
	m.changes.after(abs, data, rerr == nil)
	m.saveChangesLocked()
	m.mu.Unlock()
	m.publishChanges()
	return nil
}
