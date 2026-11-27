package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

// Live activity of a build (D566): the agent's thoughts, message chunks
// and tool calls per page, kept as a capped transcript in memory (the
// last run) and as plain text logs in <knowledge>/runs/<run>/<item>.log,
// published coalesced as knowledge.activity events.

const (
	// maxTranscriptLines caps the transcript of a page in memory.
	maxTranscriptLines = 2000
	// activityTick is the coalescing interval of activity events.
	activityTick = 100 * time.Millisecond
	// keepRuns is the number of run logs kept on disk.
	keepRuns = 5
)

// transcript is the activity of one page.
type transcript struct {
	info    kn.StepInfo
	lines   []string
	dropped int
	// open is the kind of the line chunks are appended to ("" = none).
	open string
	// pend are the events of the next tick.
	pend                []api.KnowledgeActivity
	tools, reads, chars int
	counted             bool // counters changed since the last tick
	titles              map[string]string
	file                *os.File
	running             bool
}

// activityLog collects the activity of one run.
type activityLog struct {
	run, dir string
	publish  func(topic string, v any)
	// counters reports the counters of a page (outside the lock).
	counters func(index int, tools, reads, chars int)

	mu    sync.Mutex
	items map[string]*transcript
	stop  chan struct{}
	done  chan struct{}
}

func newActivityLog(knowledgeDir, run string, publish func(string, any), counters func(int, int, int, int)) *activityLog {
	a := &activityLog{run: run, publish: publish, counters: counters, items: map[string]*transcript{}, stop: make(chan struct{}), done: make(chan struct{})}
	if knowledgeDir != "" {
		base := filepath.Join(knowledgeDir, "runs")
		a.dir = filepath.Join(base, run)
		if os.MkdirAll(a.dir, 0o755) != nil {
			a.dir = ""
		}
		pruneRuns(base, keepRuns)
	}
	go a.loop()
	return a
}

// pruneRuns keeps the newest n run directories.
func pruneRuns(base string, n int) {
	es, err := os.ReadDir(base)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range es {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	for len(dirs) > n {
		_ = os.RemoveAll(filepath.Join(base, dirs[0]))
		dirs = dirs[1:]
	}
}

func (a *activityLog) loop() {
	defer close(a.done)
	t := time.NewTicker(activityTick)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			a.flush()
			return
		case <-t.C:
			a.flush()
		}
	}
}

// close stops the log after a last flush and closes the files.
func (a *activityLog) close() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	<-a.done
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.items {
		t.running = false
		if t.file != nil {
			_ = t.file.Close()
			t.file = nil
		}
	}
}

// flush publishes the pending events and counters.
func (a *activityLog) flush() {
	type count struct{ index, tools, reads, chars int }
	var evs []api.KnowledgeActivity
	var counts []count
	a.mu.Lock()
	for _, t := range a.items {
		evs = append(evs, t.pend...)
		t.pend = nil
		if t.counted {
			t.counted = false
			counts = append(counts, count{t.info.Index, t.tools, t.reads, t.chars})
		}
	}
	a.mu.Unlock()
	for _, e := range evs {
		if a.publish != nil {
			a.publish(api.TopicKnowledgeActivity, e)
		}
	}
	for _, c := range counts {
		if a.counters != nil {
			a.counters(c.index, c.tools, c.reads, c.chars)
		}
	}
}

// get returns the transcript of an item (callers hold mu).
func (a *activityLog) get(info kn.StepInfo) *transcript {
	t := a.items[info.Item]
	if t == nil {
		t = &transcript{info: info, titles: map[string]string{}}
		a.items[info.Item] = t
		if a.dir != "" {
			t.file, _ = os.OpenFile(filepath.Join(a.dir, kn.Slug(info.Item)+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		}
	}
	if info.Step != "" {
		t.info = info
	}
	return t
}

// addLine starts a new line.
func (t *transcript) addLine(s string) {
	t.lines = append(t.lines, s)
	if over := len(t.lines) - maxTranscriptLines; over > 0 {
		t.lines = append([]string(nil), t.lines[over:]...)
		t.dropped += over
	}
	if t.file != nil {
		_, _ = t.file.WriteString("\n" + s)
	}
}

// appendText continues the last line.
func (t *transcript) appendText(s string) {
	if len(t.lines) == 0 {
		t.addLine(s)
		return
	}
	t.lines[len(t.lines)-1] += s
	if t.file != nil {
		_, _ = t.file.WriteString(s)
	}
}

func (t *transcript) event(e api.KnowledgeActivity) {
	e.Item, e.Level = t.info.Item, t.info.Level
	if n := len(t.pend); n > 0 && e.Kind != api.KnowledgeActivityTool && e.Kind != api.KnowledgeActivityStep && t.pend[n-1].Kind == e.Kind {
		t.pend[n-1].Text += e.Text // coalesce streamed chunks
		return
	}
	t.pend = append(t.pend, e)
}

// step records a step of a page (from the pipeline's events).
func (a *activityLog) step(info kn.StepInfo, text string, running bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.get(info)
	t.running = running
	t.open = ""
	t.addLine("» " + text)
	t.event(api.KnowledgeActivity{Kind: api.KnowledgeActivityStep, Text: text})
}

// chunk records streamed text (kind thought or message).
func (a *activityLog) chunk(info kn.StepInfo, kind, text string) {
	if text == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.get(info)
	t.chars += len(text)
	t.counted = true
	parts := strings.Split(text, "\n")
	for i, p := range parts {
		switch {
		case i == 0 && t.open == kind:
			t.appendText(p)
		case kind == api.KnowledgeActivityThought && (i == 0 || t.open != kind):
			t.addLine("thought: " + p)
		case kind == api.KnowledgeActivityThought:
			t.addLine("  " + p)
		default:
			t.addLine(p)
		}
		t.open = kind
	}
	t.event(api.KnowledgeActivity{Kind: kind, Text: text})
}

// tool records a tool call or its update.
func (a *activityLog) tool(info kn.StepInfo, tc acp.ToolCall, update bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.get(info)
	title := tc.Title
	if title == "" {
		title = t.titles[tc.ToolCallID]
	}
	if title == "" {
		title = strings.TrimSpace(tc.Name + " " + tc.Kind)
	}
	t.open = ""
	if !update {
		t.titles[tc.ToolCallID] = title
		t.tools++
		if tc.Kind == acp.KindRead || tc.Kind == acp.KindSearch || readOnlyTool(tc) {
			t.reads++
		}
		t.counted = true
		line := "tool: " + title
		if tc.Status != "" {
			line += " [" + tc.Status + "]"
		}
		t.addLine(line)
	} else if tc.Status == "completed" || tc.Status == "failed" {
		t.addLine("tool: " + title + " → " + tc.Status)
	} else {
		return
	}
	t.event(api.KnowledgeActivity{Kind: api.KnowledgeActivityTool, Text: title, Tool: tc.Kind, Status: tc.Status})
}

// transcriptOf returns the transcript of an item (false: unknown).
func (a *activityLog) transcriptOf(item string) (api.KnowledgeTranscript, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.items[item]
	if t == nil {
		return api.KnowledgeTranscript{}, false
	}
	tr := api.KnowledgeTranscript{Item: item, Level: t.info.Level, Title: t.info.Title, Run: a.run, Lines: append([]string(nil), t.lines...), Truncated: t.dropped, Running: t.running}
	if a.dir != "" {
		tr.File = filepath.Join(a.dir, kn.Slug(item)+".log")
	}
	return tr, true
}

// stepText describes a pipeline event for the transcript ("" = none).
func stepText(e kn.Event) string {
	switch {
	case e.State == kn.RunRunning && e.Attempt > 0:
		return fmt.Sprintf("repair %d of %s: %s", e.Attempt, e.Step, e.Error)
	case e.State == kn.RunRunning:
		return fmt.Sprintf("%s (step %d of %d)", e.Step, kn.StepIndex(e.Step), kn.StepCount)
	case e.State == kn.RunDone:
		return fmt.Sprintf("done (confidence %.2f)", e.Confidence)
	case e.State == kn.RunFailed:
		return "failed: " + e.Error
	}
	return ""
}

// transcript is knowledge.transcript: the transcript of a page in the
// running or last build (from memory, else the newest log on disk).
func (m *Module) transcript(_ context.Context, p api.KnowledgeTranscriptParams) (api.KnowledgeTranscript, error) {
	item := strings.TrimSpace(p.Item)
	if item == "" {
		return api.KnowledgeTranscript{}, api.Errorf(api.CodeInvalidParams, "item is required")
	}
	m.mu.Lock()
	act := m.act
	m.mu.Unlock()
	if act != nil {
		if tr, ok := act.transcriptOf(item); ok {
			return tr, nil
		}
	}
	dir, err := m.dir()
	if err != nil {
		return api.KnowledgeTranscript{}, err
	}
	runs, _ := os.ReadDir(filepath.Join(dir, "runs"))
	for i := len(runs) - 1; i >= 0; i-- {
		f := filepath.Join(dir, "runs", runs[i].Name(), kn.Slug(item)+".log")
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimPrefix(string(b), "\n"), "\n")
		tr := api.KnowledgeTranscript{Item: item, Run: runs[i].Name(), File: f, Lines: lines}
		if over := len(lines) - maxTranscriptLines; over > 0 {
			tr.Lines, tr.Truncated = lines[over:], over
		}
		return tr, nil
	}
	return api.KnowledgeTranscript{}, api.Errorf(api.CodeNotFound, "no transcript of %q (it was not built in a recent run)", item)
}
