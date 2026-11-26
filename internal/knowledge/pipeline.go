package knowledge

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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

// Unit run states (Event.State).
const (
	RunQueued  = "queued"
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
)

// Event reports the progress of a unit.
type Event struct {
	Unit  string `json:"unit"`
	Title string `json:"title"`
	// Index is the position of the unit in the run (0-based), Total the
	// number of units.
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

// Pipeline runs describe → validate → write for units.
type Pipeline struct {
	// Root is the project root, Dir the knowledge directory, Project the
	// project's name.
	Root, Dir, Project string
	Agent              Agent
	// Checker verifies claims (Root is used when nil).
	Checker *Checker
	// Commit is the git HEAD recorded in the documents; Model the agent
	// model.
	Commit, Model string
	// Repairs is the number of repair prompts for unusable answers
	// (default 2).
	Repairs int
	// Progress receives events (may be nil).
	Progress func(Event)
	// Now replaces time.Now (tests).
	Now func() time.Time
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

// RunUnit generates the document of one unit and returns its state
// (also on failure: Status failed with the error). The document is
// written to Dir/units/<id>.md.
func (p *Pipeline) RunUnit(ctx context.Context, u Unit, hashes map[string]string, ev Event) *UnitState {
	st := &UnitState{ID: u.ID, Title: u.Title, Dir: u.Dir, Files: hashes, Commit: p.Commit, Model: p.Model, GeneratedAt: p.now()}
	fail := func(step string, err error) *UnitState {
		st.Status, st.Error = StatusFailed, step+": "+err.Error()
		ev.Step, ev.State, ev.Error = step, RunFailed, st.Error
		p.emit(ev)
		return st
	}
	chk := p.Checker
	if chk == nil {
		chk = &Checker{Root: p.Root}
	}
	// 1. describe
	ev.Step, ev.State = StepDescribe, RunRunning
	p.emit(ev)
	var desc Description
	err := p.ask(ctx, ev, DescribePrompt(p.Project, u), func(answer string) error {
		var err error
		desc, err = ParseDescription(answer)
		return err
	}, RepairPrompt)
	if err != nil {
		return fail(StepDescribe, err)
	}
	_, notes := chk.Verify(&desc)
	// 2. validate
	ev.Step = StepValidate
	p.emit(ev)
	var val Validation
	err = p.ask(ctx, ev, ValidatePrompt(p.Project, u, desc, notes), func(answer string) error {
		var err error
		val, err = ParseValidation(answer)
		return err
	}, RepairPrompt)
	if err != nil {
		return fail(StepValidate, err)
	}
	final := *val.Description
	unverified, _ := chk.Verify(&final)
	st.Unverified = unverified
	st.Confidence = confidence(val.Confidence, final, unverified)
	ev.Confidence = st.Confidence
	// 3. write
	ev.Step = StepWrite
	p.emit(ev)
	l := Linker{Root: p.Root}
	var body string
	err = p.ask(ctx, ev, WritePrompt(p.Project, u, final), func(answer string) error {
		var err error
		body, err = NormalizeBody(u, answer, l)
		return err
	}, RepairMarkdownPrompt)
	if err != nil {
		if ctx.Err() != nil {
			return fail(StepWrite, err)
		}
		// The validated facts are worth keeping: render them ourselves.
		body = RenderBody(u, final, l)
	}
	meta := Meta{Unit: u.ID, Title: u.Title, Sources: u.Files, Commit: p.Commit, GeneratedAt: st.GeneratedAt, Confidence: st.Confidence, Model: p.Model}
	st.MD = "units/" + u.ID + ".md"
	if err := WriteFileAtomic(filepath.Join(p.Dir, filepath.FromSlash(st.MD)), []byte(meta.FrontMatter()+"\n"+body)); err != nil {
		return fail(StepWrite, err)
	}
	st.Status = StatusDone
	ev.State = RunDone
	p.emit(ev)
	return st
}

// confidence combines the validator's confidence with the share of
// mechanically verified steps.
func confidence(agent float64, d Description, unverified int) float64 {
	steps := 0
	for _, f := range d.Flows {
		steps += len(f.Steps)
	}
	c := agent
	if steps > 0 {
		c = math.Min(c, 1-float64(min(unverified, steps))/float64(steps)*0.8)
	}
	return math.Round(c*100) / 100
}

// ask runs a prompt in a new session and repairs unusable answers in the
// same session.
func (p *Pipeline) ask(ctx context.Context, ev Event, prompt string, accept func(string) error, repair func(error) string) error {
	s, err := p.Agent.NewSession(ctx)
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
		text = repair(perr)
	}
}

// Summary is the outcome of a run.
type Summary struct {
	Done      int      `json:"done"`
	Failed    int      `json:"failed"`
	Cancelled bool     `json:"cancelled,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

// Run generates the given units one after the other; the manifest is
// saved after every unit (an interrupted run resumes with the units not
// done yet) and index.md is rewritten at the end.
func (p *Pipeline) Run(ctx context.Context, units []Unit, m *Manifest) Summary {
	var sum Summary
	for i, u := range units {
		p.emit(Event{Unit: u.ID, Title: u.Title, Index: i, Total: len(units), State: RunQueued})
	}
	for i, u := range units {
		if ctx.Err() != nil {
			sum.Cancelled = true
			break
		}
		hashes := HashFiles(p.Root, &u)
		st := p.RunUnit(ctx, u, hashes, Event{Unit: u.ID, Title: u.Title, Index: i, Total: len(units)})
		if st.Status == StatusDone {
			sum.Done++
		} else {
			if ctx.Err() != nil {
				sum.Cancelled = true
				break // a cancelled unit keeps its previous state
			}
			sum.Failed++
			sum.Errors = append(sum.Errors, u.ID+": "+st.Error)
			if old := m.Units[u.ID]; old != nil && old.Status == StatusDone {
				// Keep the old document, remember the failure.
				old.Error = st.Error
				st = old
				st.Status = StatusFailed
			}
		}
		m.Units[u.ID] = st
		m.LastRun = p.now()
		_ = m.Save(p.Dir)
	}
	if err := WriteIndex(p.Dir, p.Project, m); err != nil {
		sum.Errors = append(sum.Errors, "index.md: "+err.Error())
	}
	return sum
}

// WriteIndex rewrites index.md from the manifest and the documents.
func WriteIndex(dir, project string, m *Manifest) error {
	var entries []IndexEntry
	for _, st := range m.Sorted() {
		if st.MD == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(st.MD)))
		if err != nil {
			continue
		}
		_, body := SplitFrontMatter(string(b))
		entries = append(entries, IndexEntry{ID: st.ID, Title: st.Title, Summary: OverviewOf(body), MD: st.MD, Confidence: st.Confidence, Files: len(st.Files)})
	}
	if len(entries) == 0 {
		if err := os.Remove(filepath.Join(dir, "index.md")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	gen := m.LastRun
	if gen.IsZero() {
		gen = time.Now()
	}
	return WriteFileAtomic(filepath.Join(dir, "index.md"), []byte(RenderIndex(project, gen, entries)))
}
