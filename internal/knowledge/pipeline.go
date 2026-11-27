package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Agent starts sessions of a coding agent. Each pipeline step runs in a
// fresh session so that the steps do not share context.
type Agent interface {
	NewSession(ctx context.Context) (Session, error)
}

// Session is one agent session.
type Session interface {
	// Prompt sends a user turn and returns the agent's answer text.
	Prompt(ctx context.Context, text string) (string, error)
	Close()
}

// Pipeline steps (Event.Step).
const (
	StepDescribe = "describe"
	StepValidate = "validate"
	StepWrite    = "write"
)

// Item run states (Event.State).
const (
	RunQueued  = "queued"
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
)

// Event reports the progress of an item.
type Event struct {
	Item  string `json:"item"`
	Level string `json:"level"`
	Title string `json:"title"`
	// Index is the position of the item in the run (0-based), Total the
	// number of items known so far (later levels are planned when they
	// start).
	Index int `json:"index"`
	Total int `json:"total"`
	// Step is describe, validate or write; State queued, running, done
	// or failed.
	Step       string  `json:"step,omitempty"`
	State      string  `json:"state"`
	Error      string  `json:"error,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	// Attempt counts repair prompts of the step (0: first answer).
	Attempt int `json:"attempt,omitempty"`
}

// Planner plans the items of the levels from what Tiegel knows about
// the project.
type Planner struct {
	// Root is the project root, Dir the knowledge directory.
	Root, Dir string
	Facts     Facts
	// Files are the PHP files in scope; Entries the mechanical entry
	// point inventory.
	Files                       []string
	Entries                     []EntryPoint
	MaxProcesses, MaxComponents int
}

func (pl Planner) input(m *Manifest) PlanInput {
	return PlanInput{Files: pl.Files, Facts: pl.Facts, Entries: MergeEntries(pl.Entries, LoadEntryDocs(pl.Dir, m)), MaxProcesses: pl.MaxProcesses}
}

// MaxQuestionBatches bounds the "answer questions" iteration of a run.
const MaxQuestionBatches = 5

// Items plans the items of a level. Processes use the entry points the
// entry-point pages added; components rank what the process pages cite;
// questions batch the open questions (all: also those tried
// MaxAttempts times). skipped counts processes left out by the limit.
func (pl Planner) Items(level string, m *Manifest, qs *QuestionStore, all bool) (items []Item, skipped int) {
	switch level {
	case LevelOverview:
		return []Item{PlanOverview(pl.input(m))}, 0
	case LevelEntryPoints:
		return PlanEntryPoints(PlanInput{Files: pl.Files, Facts: pl.Facts, Entries: pl.Entries}), 0
	case LevelProcesses:
		return PlanProcesses(pl.input(m))
	case LevelComponents:
		return PlanComponents(pl.Dir, m, pl.MaxComponents), 0
	case LevelQuestions:
		if qs == nil {
			return nil, 0
		}
		return PlanQuestions(qs, MaxQuestionBatches, all), 0
	}
	return nil, 0
}

// Stale tells why an item must be built ("" = current). Question
// batches are always due.
func (pl Planner) Stale(it *Item, m *Manifest) (string, map[string]string) {
	h := ItemHashes(pl.Root, it)
	if it.Level == LevelQuestions {
		return StaleNew, h
	}
	return m.Stale(pl.Dir, pl.Root, *it, h), h
}

// Pipeline runs describe → validate → write for the items of the
// levels, top-down.
type Pipeline struct {
	Planner
	// Project is the project's name.
	Project string
	Agent   Agent
	// Checker verifies references (Root is used when nil).
	Checker *Checker
	// Prompts renders the prompts (nil: the embedded templates).
	Prompts *Prompts
	// Commit is the git HEAD recorded in the pages; Model the agent
	// model.
	Commit, Model string
	// Repairs is the number of repair prompts for unusable answers
	// (default 2).
	Repairs int
	// Progress receives events (may be nil).
	Progress func(Event)
	// OnItem is called after an item finished: its page, the manifest,
	// questions.md and index.md are written (readers see it at once).
	OnItem func(*ItemState)
	// Now replaces time.Now (tests).
	Now func() time.Time

	questions *QuestionStore
	man       *Manifest
	total     int
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pipeline) emit(e Event) {
	if p.Progress != nil {
		p.Progress(e)
	}
}

func (p *Pipeline) prompts() *Prompts {
	if p.Prompts == nil {
		p.Prompts = defaultPrompts()
	}
	return p.Prompts
}

// Work says what a run builds.
type Work struct {
	// Levels to run (in run order).
	Levels []string
	// All rebuilds current items too; Only restricts to these item IDs.
	All  bool
	Only []string
}

// Summary is the outcome of a run.
type Summary struct {
	Done      int      `json:"done"`
	Failed    int      `json:"failed"`
	Cancelled bool     `json:"cancelled,omitempty"`
	Errors    []string `json:"errors,omitempty"`
	// Answered counts questions answered in this run.
	Answered int `json:"answered,omitempty"`
}

// Run builds the levels of w top-down. Each level is planned when it
// starts (processes see the entry points the entry-point pages found,
// components the classes the process pages cite). The manifest, the
// page, questions.md and index.md are written after every item, so an
// interrupted run resumes with what is not done yet and readers see the
// pages as they appear.
func (p *Pipeline) Run(ctx context.Context, w Work, m *Manifest) Summary {
	var sum Summary
	qs, err := LoadQuestions(p.Dir)
	if err != nil {
		sum.Errors = append(sum.Errors, err.Error())
	}
	p.questions, p.man = qs, m
	_, answeredBefore := qs.Counts()
	p.total = 0
	for _, level := range w.Levels {
		if ctx.Err() != nil {
			sum.Cancelled = true
			break
		}
		items, _ := p.Items(level, m, qs, w.All)
		type job struct {
			it     Item
			hashes map[string]string
		}
		var jobs []job
		for _, it := range items {
			if len(w.Only) > 0 && !slices.Contains(w.Only, it.ID) {
				continue
			}
			why, h := p.Stale(&it, m)
			if why == "" && !w.All && len(w.Only) == 0 {
				continue
			}
			jobs = append(jobs, job{it, h})
		}
		start := p.total
		p.total += len(jobs)
		for i, j := range jobs {
			p.emit(Event{Item: j.it.ID, Level: level, Title: j.it.Title, Index: start + i, Total: p.total, State: RunQueued})
		}
		for i, j := range jobs {
			if ctx.Err() != nil {
				sum.Cancelled = true
				break
			}
			ev := Event{Item: j.it.ID, Level: level, Title: j.it.Title, Index: start + i, Total: p.total}
			st := p.RunItem(ctx, j.it, j.hashes, ev)
			if st.Status == StatusDone {
				sum.Done++
			} else {
				if ctx.Err() != nil {
					sum.Cancelled = true
					break // a cancelled item keeps its previous state
				}
				sum.Failed++
				sum.Errors = append(sum.Errors, j.it.ID+": "+st.Error)
				if old := m.Items[j.it.ID]; old != nil && old.Status == StatusDone && old.MD != "" {
					// Keep the old page, remember the failure.
					old.Error = st.Error
					st = old
					st.Status = StatusFailed
				}
			}
			if level != LevelQuestions {
				m.Items[j.it.ID] = st
			}
			m.LastRun = p.now()
			p.afterItem(m, st, &sum)
		}
		if sum.Cancelled {
			break
		}
	}
	if m.Legacy && !sum.Cancelled {
		// The pages of the old unit pipeline (D560) are replaced.
		_ = os.RemoveAll(filepath.Join(p.Dir, "units"))
		m.Legacy = false
	}
	_, answeredAfter := qs.Counts()
	sum.Answered = answeredAfter - answeredBefore
	if err := m.Save(p.Dir); err != nil {
		sum.Errors = append(sum.Errors, "manifest: "+err.Error())
	}
	if err := WriteIndex(p.Dir, p.Project, m, qs, p.Root); err != nil {
		sum.Errors = append(sum.Errors, "index.md: "+err.Error())
	}
	return sum
}

func (p *Pipeline) afterItem(m *Manifest, st *ItemState, sum *Summary) {
	if err := m.Save(p.Dir); err != nil {
		sum.Errors = append(sum.Errors, "manifest: "+err.Error())
	}
	if err := p.questions.Save(p.Dir); err != nil {
		sum.Errors = append(sum.Errors, "questions: "+err.Error())
	}
	if err := WriteIndex(p.Dir, p.Project, m, p.questions, p.Root); err != nil {
		sum.Errors = append(sum.Errors, "index.md: "+err.Error())
	}
	if p.OnItem != nil {
		p.OnItem(st)
	}
}

// loadData reads the validated JSON of a page into v.
func loadData(dir string, st *ItemState, v any) bool {
	if st == nil || st.Data == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(st.Data)))
	return err == nil && json.Unmarshal(b, v) == nil
}

// promptData assembles the context of an item's prompt (token budgeted
// in the templates).
func (p *Pipeline) promptData(it Item, m *Manifest) PromptData {
	d := PromptData{Project: p.Project, Level: it.Level, LevelTitle: LevelTitle(it.Level), Title: it.Title, ID: it.ID, Group: it.Group,
		Facts: p.Facts.Compact(), Schema: Schema(it.Level), Entries: CompactEntries(it.Entries)}
	if it.Level != LevelOverview {
		var ov OverviewDoc
		if loadData(p.Dir, m.Items["overview"], &ov) {
			d.Overview = CompactOverview(&ov)
		}
	}
	files := it.Files
	switch it.Level {
	case LevelOverview:
		files = slices.Clone(p.Facts.ConfigFiles())
		for _, ns := range sortedKeys(p.Facts.PSR4) {
			files = append(files, p.Facts.PSR4[ns]...)
		}
	case LevelProcesses:
		d.Context = p.entryContext(it, m)
	case LevelComponents:
		d.Context = p.componentContext(it, m)
	}
	d.listFiles(uniqueStrings(files))
	var qs []*QuestionRecord
	if it.Level == LevelQuestions {
		for _, id := range it.Questions {
			if q := p.questions.Get(id); q != nil {
				qs = append(qs, q)
			}
		}
	} else {
		qs = p.questions.Relevant(it, 8)
	}
	d.Questions = promptQuestions(qs)
	return d
}

// entryContext is what the entry-point pages say about a process's
// entry points.
func (p *Pipeline) entryContext(it Item, m *Manifest) string {
	names := map[string]bool{}
	for _, e := range it.Entries {
		names[e.Name] = true
	}
	var b strings.Builder
	for _, st := range m.Level(LevelEntryPoints) {
		var d EntryPointsDoc
		if !loadData(p.Dir, st, &d) {
			continue
		}
		for _, e := range d.Entries {
			if !names[e.Name] {
				continue
			}
			fmt.Fprintf(&b, "- %s: %s", e.Name, e.Purpose)
			if e.Input != "" {
				fmt.Fprintf(&b, "; in: %s", e.Input)
			}
			if e.Output != "" {
				fmt.Fprintf(&b, "; out: %s", e.Output)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// componentContext lists the steps of the processes through a class.
func (p *Pipeline) componentContext(it Item, m *Manifest) string {
	var b strings.Builder
	short := shortClass(it.Group)
	for _, st := range m.Level(LevelProcesses) {
		if !slices.Contains(it.UsedBy, st.Title) {
			continue
		}
		var d ProcessDoc
		if !loadData(p.Dir, st, &d) {
			continue
		}
		fmt.Fprintf(&b, "Process %s (%s):\n", d.Name, st.MD)
		for _, s := range d.Steps {
			c := SymbolClass(s.Symbol)
			if c != it.Group && shortClass(c) != short {
				continue
			}
			fmt.Fprintf(&b, "- %s – %s", s.Symbol, s.What)
			if s.Data != "" {
				fmt.Fprintf(&b, " (data: %s)", s.Data)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// RunItem builds the page of one item and returns its state (also on
// failure: Status failed with the error).
func (p *Pipeline) RunItem(ctx context.Context, it Item, hashes map[string]string, ev Event) *ItemState {
	if p.questions == nil {
		p.questions, _ = LoadQuestions(p.Dir)
	}
	man := p.man
	if man == nil {
		man, _ = LoadManifest(p.Dir)
	}
	st := &ItemState{ID: it.ID, Level: it.Level, Title: it.Title, Group: it.Group, Files: hashes, Commit: p.Commit, Model: p.Model, GeneratedAt: p.now()}
	l := Linker{Root: p.Root}
	fail := func(step string, err error) *ItemState {
		st.Status, st.Error = StatusFailed, step+": "+err.Error()
		ev.Step, ev.State, ev.Error = step, RunFailed, st.Error
		p.emit(ev)
		if ctx.Err() == nil {
			p.writeMechanical(it, st, man, l)
		}
		return st
	}
	chk := p.Checker
	if chk == nil {
		chk = &Checker{Root: p.Root}
	}
	pr := p.prompts()
	data := p.promptData(it, man)
	// 1. describe
	ev.Step, ev.State = StepDescribe, RunRunning
	p.emit(ev)
	prompt, err := pr.Render(TemplateFor(it.Level), data)
	if err != nil {
		return fail(StepDescribe, err)
	}
	var doc Doc
	err = p.ask(ctx, ev, prompt, func(answer string) error {
		var err error
		doc, err = ParseDoc(it.Level, answer)
		return err
	})
	if err != nil {
		return fail(StepDescribe, err)
	}
	_, notes := chk.Verify(doc)
	// 2. validate
	ev.Step = StepValidate
	p.emit(ev)
	b, _ := json.MarshalIndent(doc, "", "  ")
	data.Doc = string(b)
	data.Notes = notes
	if len(notes) > 30 {
		data.Notes, data.MoreNotes = notes[:30], len(notes)-30
	}
	prompt, err = pr.Render("validate.tmpl", data)
	if err != nil {
		return fail(StepValidate, err)
	}
	var val Validation
	err = p.ask(ctx, ev, prompt, func(answer string) error {
		var err error
		var d Doc
		val, d, err = ParseValidation(it.Level, answer)
		if err == nil {
			doc = d
		}
		return err
	})
	if err != nil {
		return fail(StepValidate, err)
	}
	unverified, _ := chk.Verify(doc)
	p.complete(it, doc)
	st.Unverified = unverified
	st.Confidence = confidence(val.Confidence, len(doc.refs()), unverified)
	ev.Confidence = st.Confidence
	// 3. write (rendered by Tiegel; diagrams are parsed first)
	ev.Step = StepWrite
	p.emit(ev)
	stillOpen := []string(nil)
	if a, ok := doc.(*AnswersDoc); ok {
		stillOpen = a.StillOpen
	}
	p.questions.Apply(it, doc.qa(), stillOpen, st.GeneratedAt)
	st.Summary = firstSentences(doc.summary(), 300)
	if it.MD != "" {
		body := p.render(it, doc, man, l)
		meta := Meta{ID: it.ID, Level: it.Level, Title: it.Title, Sources: sortedKeysNoInputs(hashes), Commit: p.Commit, GeneratedAt: st.GeneratedAt, Confidence: st.Confidence, Model: p.Model}
		if err := WriteFileAtomic(filepath.Join(p.Dir, filepath.FromSlash(it.MD)), []byte(meta.FrontMatter()+"\n"+body)); err != nil {
			return fail(StepWrite, err)
		}
		st.MD = it.MD
		b, _ := json.MarshalIndent(doc, "", "  ")
		if err := WriteFileAtomic(filepath.Join(p.Dir, filepath.FromSlash(it.Data())), b); err != nil {
			return fail(StepWrite, err)
		}
		st.Data = it.Data()
	}
	// The cited files are sources too: a change makes the page stale.
	for _, r := range doc.refs() {
		if r.File == "" || r.Unverified() {
			continue
		}
		if _, ok := st.Files[r.File]; !ok {
			st.Files[r.File] = HashFile(filepath.Join(p.Root, filepath.FromSlash(r.File)))
		}
	}
	st.Status = StatusDone
	ev.State = RunDone
	p.emit(ev)
	return st
}

// complete fixes up a validated document: entry-point pages keep every
// inventory entry and mark what Tiegel found.
func (p *Pipeline) complete(it Item, doc Doc) {
	d, ok := doc.(*EntryPointsDoc)
	if !ok {
		return
	}
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	have := map[string]bool{}
	inv := map[string]bool{}
	for _, e := range it.Entries {
		inv[norm(e.Name)] = true
	}
	for i := range d.Entries {
		n := norm(d.Entries[i].Name)
		have[n] = true
		d.Entries[i].Found = inv[n]
		if d.Entries[i].Kind == "" {
			d.Entries[i].Kind = it.Group
		}
	}
	for _, e := range it.Entries {
		if have[norm(e.Name)] {
			continue
		}
		d.Entries = append(d.Entries, EntryDesc{Name: e.Name, Kind: e.Kind, Handler: Ref{File: e.File, Line: e.Line, Symbol: e.Handler}, Purpose: "(not described by the agent)", Found: true})
	}
}

func (p *Pipeline) render(it Item, doc Doc, m *Manifest, l Linker) string {
	switch d := doc.(type) {
	case *OverviewDoc:
		return RenderOverview(p.Project, d, p.Facts, l)
	case *EntryPointsDoc:
		return RenderEntryPoints(it, d, l)
	case *ProcessDoc:
		return RenderProcess(it, d, l)
	case *ComponentDoc:
		pages := map[string]string{}
		for _, st := range m.Level(LevelProcesses) {
			pages[st.Title] = st.MD
		}
		return RenderComponent(it, d, pages, l)
	}
	return ""
}

// writeMechanical writes the facts-only page of an overview or
// entry-point item that failed and has no page yet.
func (p *Pipeline) writeMechanical(it Item, st *ItemState, m *Manifest, l Linker) {
	if old := m.Items[it.ID]; old != nil && old.MD != "" && !old.Mechanical {
		return
	}
	var body string
	switch it.Level {
	case LevelOverview:
		body = RenderOverview(p.Project, nil, p.Facts, l)
		st.Summary = "Facts collected by Tiegel (the agent's analysis failed)."
	case LevelEntryPoints:
		if len(it.Entries) == 0 {
			return
		}
		body = RenderEntryPoints(it, nil, l)
		st.Summary = fmt.Sprintf("%d entry points found by Tiegel (the agent's description failed).", len(it.Entries))
	default:
		return
	}
	meta := Meta{ID: it.ID, Level: it.Level, Title: it.Title, Sources: sortedKeysNoInputs(st.Files), Commit: p.Commit, GeneratedAt: st.GeneratedAt}
	if WriteFileAtomic(filepath.Join(p.Dir, filepath.FromSlash(it.MD)), []byte(meta.FrontMatter()+"\n"+body)) == nil {
		st.MD, st.Mechanical = it.MD, true
	}
}

func sortedKeysNoInputs(m map[string]string) []string {
	var out []string
	for k := range m {
		if !strings.HasPrefix(k, "@") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// firstSentences cuts s to n runes at a sentence end where possible.
func firstSentences(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, ". "); i > n/3 {
		return cut[:i+1]
	}
	return cut + "…"
}

// confidence combines the validator's confidence with the share of
// mechanically verified references.
func confidence(agent float64, refs, unverified int) float64 {
	c := agent
	if refs > 0 {
		c = math.Min(c, 1-float64(min(unverified, refs))/float64(refs)*0.8)
	}
	return math.Round(c*100) / 100
}

// ask runs a prompt in a new session and repairs unusable answers in the
// same session.
func (p *Pipeline) ask(ctx context.Context, ev Event, prompt string, accept func(string) error) error {
	s, err := p.Agent.NewSession(WithStep(ctx, StepInfo{Item: ev.Item, Level: ev.Level, Title: ev.Title, Step: ev.Step, Index: ev.Index}))
	if err != nil {
		return err
	}
	defer s.Close()
	repairs := p.Repairs
	if repairs <= 0 {
		repairs = 2
	}
	text := prompt
	for attempt := 0; ; attempt++ {
		answer, err := s.Prompt(ctx, text)
		if err != nil {
			return err
		}
		perr := accept(answer)
		if perr == nil {
			return nil
		}
		if attempt >= repairs {
			return fmt.Errorf("unusable answer after %d attempts: %w", attempt+1, perr)
		}
		ev.State, ev.Attempt, ev.Error = RunRunning, attempt+1, perr.Error()
		p.emit(ev)
		if text, err = p.prompts().Render("repair.tmpl", PromptData{Error: perr.Error()}); err != nil {
			return err
		}
	}
}

// WriteIndex rewrites index.md and questions.md from the manifest and
// the question store (qs nil: loaded from dir). Without any page both
// are removed.
func WriteIndex(dir, project string, m *Manifest, qs *QuestionStore, root string) error {
	if qs == nil {
		qs, _ = LoadQuestions(dir)
	}
	pages := map[string]string{}
	n := 0
	for _, st := range m.Items {
		if st.MD == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(st.MD))); err != nil {
			continue
		}
		pages[st.ID] = st.MD
		n++
	}
	open, answered := qs.Counts()
	if n == 0 {
		for _, f := range []string{"index.md", "questions.md"} {
			if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if open+answered > 0 {
		if err := WriteFileAtomic(filepath.Join(dir, "questions.md"), []byte(RenderQuestions(qs, pages, Linker{Root: root}))); err != nil {
			return err
		}
	}
	gen := m.LastRun
	if gen.IsZero() {
		gen = time.Now()
	}
	shown := m.Clone()
	for id, st := range shown.Items {
		if pages[id] == "" {
			st.MD = ""
		}
	}
	return WriteFileAtomic(filepath.Join(dir, "index.md"), []byte(RenderIndex(project, gen, shown, open, answered)))
}
