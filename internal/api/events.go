package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

// NotifyMethod is the JSON-RPC notification method used for forwarded events.
const NotifyMethod = "event"

// Event is a bus event as delivered to clients.
type Event struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Session is one client of the App (a JSON-RPC connection, a CLI run, the
// GUI). It carries the client's event subscriptions; handlers find it in
// their context via SessionFrom.
type Session struct {
	app    *App
	notify func(Event)

	mu     sync.Mutex
	subs   map[string]func()
	closed bool
}

type sessionKey struct{}

// NewSession creates a session whose subscribed events are passed to notify.
// notify is called from bus goroutines, one subscription at a time in publish
// order; it must be safe for concurrent use across subscriptions.
func (a *App) NewSession(notify func(Event)) *Session {
	if notify == nil {
		notify = func(Event) {}
	}
	return &Session{app: a, notify: notify, subs: make(map[string]func())}
}

// SessionFrom returns the session a method is called in, if any.
func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(*Session)
	return s, ok
}

// Call invokes method within the session.
func (s *Session) Call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return s.app.Call(context.WithValue(ctx, sessionKey{}, s), method, params)
}

// Subscribe forwards events of the given topic patterns ("doc.saved",
// "debug.*", "*") to the session and returns all subscribed patterns.
func (s *Session) Subscribe(patterns ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := s.subs[p]; ok {
			continue
		}
		s.subs[p] = s.app.bus.Subscribe(p, func(e bus.Event) {
			s.notify(Event{Topic: e.Topic, Payload: encodePayload(e.Payload)})
		})
	}
	return s.topicsLocked()
}

// Unsubscribe stops forwarding the given patterns; without patterns all
// subscriptions end. It returns the remaining patterns.
func (s *Session) Unsubscribe(patterns ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(patterns) == 0 {
		for p := range s.subs {
			patterns = append(patterns, p)
		}
	}
	for _, p := range patterns {
		if un, ok := s.subs[p]; ok {
			un()
			delete(s.subs, p)
		}
	}
	return s.topicsLocked()
}

func (s *Session) topicsLocked() []string {
	out := make([]string, 0, len(s.subs))
	for p := range s.subs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Close ends all subscriptions. Further subscriptions are ignored.
func (s *Session) Close() {
	s.Unsubscribe()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func encodePayload(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(fmt.Sprint(v))
	}
	return b
}

// TopicsParams are the params of "events.subscribe" and "events.unsubscribe".
type TopicsParams struct {
	Topics []string `json:"topics"`
}

// TopicsResult lists the patterns a session is subscribed to.
type TopicsResult struct {
	Topics []string `json:"topics"`
}

func (a *App) eventsSubscribe(ctx context.Context, p TopicsParams) (TopicsResult, error) {
	s, ok := SessionFrom(ctx)
	if !ok {
		return TopicsResult{}, Errorf(CodeFailed, "events.subscribe needs a session (use serve)")
	}
	if len(p.Topics) == 0 {
		return TopicsResult{}, Errorf(CodeInvalidParams, "topics is required")
	}
	return TopicsResult{Topics: s.Subscribe(p.Topics...)}, nil
}

func (a *App) eventsUnsubscribe(ctx context.Context, p TopicsParams) (TopicsResult, error) {
	s, ok := SessionFrom(ctx)
	if !ok {
		return TopicsResult{}, Errorf(CodeFailed, "events.unsubscribe needs a session (use serve)")
	}
	return TopicsResult{Topics: s.Unsubscribe(p.Topics...)}, nil
}

// PublishParams are the params of "events.publish".
type PublishParams struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (a *App) eventsPublish(_ context.Context, p PublishParams) (struct{}, error) {
	if p.Topic == "" || strings.Contains(p.Topic, "*") {
		return struct{}{}, Errorf(CodeInvalidParams, "a concrete topic is required")
	}
	var payload any
	if len(p.Payload) > 0 {
		payload = p.Payload
	}
	a.bus.Publish(p.Topic, payload)
	return struct{}{}, nil
}

// Topics published by the kernel itself (modules add theirs, see
// contract.go).
const (
	TopicProjectOpened = "project.opened"
	TopicProjectClosed = "project.closed"
	TopicDocOpened     = "doc.opened"
	TopicDocChanged    = "doc.changed"
	TopicDocSaved      = "doc.saved"
	TopicDocClosed     = "doc.closed"
	TopicModuleChanged = "modules.changed"
)
