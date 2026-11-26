package terminal

import (
	"strconv"
	"strings"
)

// Moving sessions between Managers (D547): a hibernated project releases
// its App and with it the App's Manager, but its terminals keep running.
// Detach takes the sessions out of a Manager without ending them (their
// output keeps going into the screen buffer, no events are sent); Adopt
// hands them to the Manager of the resumed project, which reports each
// with OnOpen.

// Detach removes the sessions match selects from m without ending them
// and returns them. Detached sessions keep reading their program's output
// but report nothing until a Manager adopts them; OnClose is not called.
func (m *Manager) Detach(match func(Info) bool) []*Session {
	m.mu.Lock()
	var out []*Session
	keep := m.order[:0]
	for _, id := range m.order {
		s := m.sessions[id]
		if match(s.Info()) {
			out = append(out, s)
			delete(m.sessions, id)
			continue
		}
		keep = append(keep, id)
	}
	m.order = keep
	m.mu.Unlock()
	for _, s := range out {
		s.m.Store(nil)
	}
	return out
}

// Adopt adds detached sessions to m (in order, keeping their IDs; IDs
// m already uses are renumbered) and reports each with OnOpen. The
// colors of m apply to them.
func (m *Manager) Adopt(ss []*Session) []Info {
	out := make([]Info, 0, len(ss))
	for _, s := range ss {
		m.mu.Lock()
		if m.sessions == nil {
			m.sessions = map[string]*Session{}
		}
		s.mu.Lock()
		id := s.info.ID
		if n, err := strconv.Atoi(strings.TrimPrefix(id, "t")); err == nil && strings.HasPrefix(id, "t") {
			m.next = max(m.next, n)
		}
		if _, taken := m.sessions[id]; taken {
			m.next++
			id = "t" + strconv.Itoa(m.next)
			s.info.ID = id
		}
		if m.colors != nil {
			s.term.Colors = *m.colors
		}
		info := s.infoLocked()
		s.mu.Unlock()
		m.sessions[id] = s
		m.order = append(m.order, id)
		m.mu.Unlock()
		s.m.Store(m)
		out = append(out, info)
		if m.OnOpen != nil {
			m.OnOpen(info)
		}
		// Output that arrived while detached is reported now.
		s.mu.Lock()
		s.changed = true
		s.mu.Unlock()
		s.signal()
	}
	return out
}

// Shutdown ends a session that belongs to no Manager (detached): hangup,
// then the process tree is killed, like Manager.Close.
func (s *Session) Shutdown() error { return s.shutdown() }

// WorkingDir returns the current working directory of a local shell:
// the directory of its process where the platform tells it (macOS,
// Linux), else the one the shell reported through OSC 7, else the start
// directory. Plugin sessions report OSC 7 or the start directory.
func (s *Session) WorkingDir() string {
	s.mu.Lock()
	kind, pid, running, osc, dir := s.info.Kind, s.info.PID, s.info.Running, s.term.Dir(), s.info.Dir
	s.mu.Unlock()
	if kind == KindShell && running {
		if d := procCwd(pid); d != "" {
			return d
		}
	}
	if osc != "" {
		return osc
	}
	return dir
}
