package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
)

// Transcript persistence (D573): the transcript of a project survives a
// hibernation (the project's App and with it the agent process end) and
// a restart of the IDE. It is stored next to the changes in the project's
// state directory, written atomically on every user message (throttled),
// at the end of each turn and when the module stops; agent.newSession
// removes it. The next prompt resumes the stored session with
// session/load when the agent supports it, else a new session starts and
// a notice says that the agent does not remember the history.

// Limits of the stored transcript: the last maxStoredEntries entries,
// fewer when the file would exceed maxStoredBytes.
const (
	maxStoredEntries = 500
	maxStoredBytes   = 2 << 20
)

// saveDelay throttles the save after a user message.
var saveDelay = time.Second

// transcriptFile is the file name in the project's state directory.
const transcriptFile = "agent-transcript.json"

// NoticePreviousSession is the notice added when the stored session could
// not be resumed.
const NoticePreviousSession = "Previous session ended (project hibernated); the conversation above is history – the agent does not remember it."

// storedTranscript is the persisted transcript.
type storedTranscript struct {
	SessionID string           `json:"sessionId,omitempty"`
	Model     string           `json:"model,omitempty"`
	Mode      string           `json:"mode,omitempty"`
	Saved     time.Time        `json:"saved"`
	Entries   []api.AgentEntry `json:"entries"`
}

// transcriptState is the persistence state; the module's mutex guards it
// (writeMu serialises the writes and is taken before mu).
type transcriptState struct {
	// file is the transcript file of the project ("" = no project or not
	// loaded yet).
	file string
	// resumeID is the session the transcript belongs to (loaded on the
	// next start when the agent supports it); model and mode its options.
	// restored is set while the entries came from the file and no session
	// followed yet.
	resumeID, model, mode string
	restored              bool
	// canLoad tells whether the running agent supports session/load.
	canLoad bool
	// replay suppresses the conversation the agent replays after
	// session/load (the restored entries show it already) until the next
	// user message.
	replay bool
	dirty  bool
	timer  *time.Timer

	writeMu sync.Mutex
}

// startTranscript registers the busy hook and follows the project (Start).
func (m *Module) startTranscript(ctx module.Context) {
	// A running turn (or a permission request waiting for the user) keeps
	// the project from hibernating; an idle session does not.
	ctx.OnStop(api.HookProjectBusy.On(ctx.Hooks(), 100, func(_ context.Context, l []string) ([]string, error) {
		if m.turnBusy() {
			l = append(l, "agent turn")
		}
		return l, nil
	}, hooks.WithOwner(ID)))
	ctx.OnStop(ctx.Bus().Subscribe("project.*", func(e bus.Event) {
		if e.Topic == api.TopicProjectOpened || e.Topic == api.TopicProjectClosed {
			m.projectChanged()
		}
	}))
}

// stopTranscript saves the transcript (Stop, after the agent ended).
func (m *Module) stopTranscript() {
	m.mu.Lock()
	if m.tr.timer != nil {
		m.tr.timer.Stop()
		m.tr.timer = nil
	}
	m.mu.Unlock()
	m.saveTranscript()
}

// turnBusy reports whether a turn runs or a permission request waits.
func (m *Module) turnBusy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turnCancel != nil || len(m.perms) > 0
}

// transcriptPath is the transcript file of the open project ("" = none).
func (m *Module) transcriptPath() string {
	if app := m.application(); app != nil {
		if ps := app.ProjectSettings(); ps != nil {
			return filepath.Join(ps.Dir(), transcriptFile)
		}
	}
	return ""
}

// projectChanged follows project.opened/closed: an agent still working
// in the previous project ends (its transcript is saved), then the
// transcript of the open project is loaded.
func (m *Module) projectChanged() {
	file := m.transcriptPath()
	m.mu.Lock()
	switched := m.tr.file != "" && m.tr.file != file && m.proc != nil
	m.mu.Unlock()
	if switched {
		m.saveTranscript()
		m.shutdown(api.AgentStopped)
	}
	if m.syncTranscript() {
		m.publish(api.TopicAgentUpdate, api.AgentUpdateEvent{Entry: api.AgentEntry{Index: -1}})
		m.publishStatus()
	}
}

// syncTranscript loads the stored transcript of the open project when the
// project changed since the last load and no session is live; the
// transcript of the previous project is saved first. It reports whether
// the entries were replaced. Callers do not hold mu.
func (m *Module) syncTranscript() bool {
	file := m.transcriptPath()
	m.mu.Lock()
	if file == m.tr.file {
		m.mu.Unlock()
		return false
	}
	if m.proc != nil || m.turnCancel != nil || m.state == api.AgentStarting {
		// A live session keeps its transcript (a project switch ends it
		// first, see projectChanged); it is stored in the project's file.
		if m.tr.file == "" {
			m.tr.file = file
		}
		m.mu.Unlock()
		return false
	}
	var old string
	var snap storedTranscript
	if m.tr.dirty && m.tr.file != "" {
		old, snap = m.tr.file, m.snapshotLocked()
	}
	m.tr.file = file
	st := storedTranscript{}
	if file != "" {
		st = readTranscript(file)
	}
	m.restoreLocked(st)
	m.mu.Unlock()
	if old != "" {
		m.writeTranscript(old, snap)
	}
	return true
}

// restoreLocked replaces the session state with st (callers hold mu).
func (m *Module) restoreLocked(st storedTranscript) {
	m.reset()
	for i := range st.Entries {
		e := st.Entries[i]
		e.Index = i
		if e.Permission != nil && e.Permission.Answer == "" {
			// Nobody can answer a request of an ended session.
			e.Permission.Answer = acp.OutcomeCancelled
		}
		m.entries = append(m.entries, &e)
	}
	m.tr.resumeID, m.tr.model, m.tr.mode = st.SessionID, st.Model, st.Mode
	m.tr.restored = len(m.entries) > 0
	m.tr.replay, m.tr.dirty = false, false
}

// readTranscript reads a stored transcript (empty when missing or
// unreadable).
func readTranscript(path string) storedTranscript {
	var st storedTranscript
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if json.Unmarshal(b, &st) != nil {
		return storedTranscript{}
	}
	return st
}

// snapshotLocked copies what is stored (callers hold mu).
func (m *Module) snapshotLocked() storedTranscript {
	st := storedTranscript{SessionID: m.sessionID, Model: m.tr.model, Mode: m.tr.mode, Saved: time.Now()}
	if st.SessionID == "" {
		st.SessionID = m.tr.resumeID
	}
	for _, o := range m.options {
		switch {
		case o.ID == "model" || o.Category == "model":
			st.Model = string(o.CurrentValue)
		case o.ID == "mode" || o.Category == "mode":
			st.Mode = string(o.CurrentValue)
		}
	}
	from := max(0, len(m.entries)-maxStoredEntries)
	for i := from; i < len(m.entries); i++ {
		st.Entries = append(st.Entries, m.entryEvent(i, "").Entry)
	}
	return st
}

// scheduleSave saves the transcript soon (user messages; throttled).
func (m *Module) scheduleSave() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tr.dirty = true
	if m.tr.timer == nil && m.tr.file != "" {
		m.tr.timer = time.AfterFunc(saveDelay, func() {
			m.mu.Lock()
			m.tr.timer = nil
			m.mu.Unlock()
			m.saveTranscript()
		})
	}
}

// saveTranscript writes the transcript now. Callers do not hold mu.
func (m *Module) saveTranscript() {
	m.tr.writeMu.Lock()
	defer m.tr.writeMu.Unlock()
	m.mu.Lock()
	file := m.tr.file
	if file == "" || m.evalPol.Load() != nil {
		// Evaluation runs are measured, not kept.
		m.mu.Unlock()
		return
	}
	snap := m.snapshotLocked()
	m.tr.dirty = false
	m.mu.Unlock()
	m.writeTranscript(file, snap)
}

// writeTranscript stores st at path atomically (removed when empty);
// failures are logged.
func (m *Module) writeTranscript(path string, st storedTranscript) {
	if err := storeTranscript(path, st); err != nil {
		if app := m.application(); app != nil {
			app.Logger().Warn("agent transcript", "err", err)
		}
	}
}

// storeTranscript writes st to path (temporary file and rename), dropping
// the oldest entries while the file would exceed maxStoredBytes.
func storeTranscript(path string, st storedTranscript) error {
	if len(st.Entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("agent transcript: %w", err)
		}
		return nil
	}
	var b []byte
	for {
		var err error
		if b, err = json.Marshal(st); err != nil {
			return fmt.Errorf("agent transcript: %w", err)
		}
		if len(b) <= maxStoredBytes || len(st.Entries) <= 1 {
			break
		}
		st.Entries = st.Entries[len(st.Entries)/4+1:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agent transcript: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("agent transcript: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("agent transcript: %w", err)
	}
	return nil
}

// forgetTranscript removes the stored transcript (agent.newSession).
func (m *Module) forgetTranscript() {
	m.mu.Lock()
	m.tr.resumeID, m.tr.model, m.tr.mode = "", "", ""
	m.tr.restored, m.tr.replay = false, false
	m.mu.Unlock()
	m.saveTranscript()
}

// loadPrevious resumes the session of the restored transcript with
// session/load when the agent supports it. The conversation the agent
// replays is not added again: the restored entries already show it, with
// Tiegel's own entries (permissions, notices, context) the agent does not
// know. It reports whether the session was loaded.
func (m *Module) loadPrevious(ctx context.Context, proc *acp.Process, root string, servers []acp.MCPServerStdio) (acp.NewSessionResponse, bool) {
	m.mu.Lock()
	id, can := m.tr.resumeID, m.tr.canLoad
	if id == "" || !can {
		m.mu.Unlock()
		return acp.NewSessionResponse{}, false
	}
	m.tr.replay = true
	m.mu.Unlock()
	res, err := proc.LoadSession(ctx, id, root, servers)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.tr.replay = false
		if app := m.application(); app != nil {
			app.Logger().Info("agent session/load failed; new session", "session", id, "err", err)
		}
		return acp.NewSessionResponse{}, false
	}
	// The rules went out with the first message of that session.
	m.rulesOut = true
	return acp.NewSessionResponse{SessionID: id, ConfigOptions: res.ConfigOptions}, true
}

// sessionOpened finishes a restored transcript once a session runs: when
// it is a new one, a notice says that the agent does not remember the
// history and the stored mode is chosen again.
func (m *Module) sessionOpened(ctx context.Context, proc *acp.Process, sid string, loaded bool) {
	m.mu.Lock()
	restored, mode := m.tr.restored, m.tr.mode
	m.tr.restored, m.tr.resumeID = false, sid
	m.mu.Unlock()
	if !restored || loaded {
		return
	}
	m.notice(api.NoticeInfo, NoticePreviousSession)
	if mode == "" {
		return
	}
	m.mu.Lock()
	var opt *acp.ConfigOption
	for i := range m.options {
		if o := m.options[i]; o.ID == "mode" || o.Category == "mode" {
			opt = &o
			break
		}
	}
	m.mu.Unlock()
	if opt == nil || string(opt.CurrentValue) == mode || !hasChoice(*opt, mode) {
		return
	}
	if opts, err := proc.SetConfigOption(ctx, sid, opt.ID, mode); err == nil && len(opts) > 0 {
		m.mu.Lock()
		m.options = opts
		m.mu.Unlock()
	}
}

// replaying reports whether a session update of kind belongs to the
// replay after session/load (callers hold mu).
func (m *Module) replayingLocked(kind string) bool {
	if !m.tr.replay {
		return false
	}
	switch kind {
	case acp.UpdateUserMessage, acp.UpdateAgentMessage, acp.UpdateAgentThought, acp.UpdateToolCall, acp.UpdateToolCallUpdate, acp.UpdatePlan:
		return true
	}
	return false
}
