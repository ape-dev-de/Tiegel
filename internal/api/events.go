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
//
// All patterns of a session share one bus subscription, so every event is
// delivered at most once (even if several patterns match) and all events
// reach the client in publish order.
type Session struct {
	app    *App
	notify func(Event)

	mu         sync.Mutex
	patterns   map[string]bool
	unsub      func()
	closed     bool
	maxQueue   int
	onOverflow func()
}

type sessionKey struct{}

// NewSession creates a session whose subscribed events are passed to notify.
// notify is called from a bus goroutine, one event at a time in publish
// order.
func (a *App) NewSession(notify func(Event)) *Session {
	if notify == nil {
		notify = func(Event) {}
	}
	return &Session{app: a, notify: notify, patterns: make(map[string]bool)}
}

// SessionFrom returns the session a method is called in, if any.
func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(*Session)
	return s, ok
}

// Call invokes method within the session.
func (s *Session) Call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return s.app.Call(s.With(ctx), method, params)
}

// With returns ctx carrying the session, as seen by handlers (SessionFrom).
func (s *Session) With(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

// LimitQueue bounds the number of events waiting for delivery to this
// session (see bus.SubscribeBounded). When a client stops reading and the
// limit is reached, the session's subscriptions end and onOverflow is
// called once (e.g. to close the connection). It applies to subscriptions
// made afterwards; max <= 0 means unbounded (the default).
func (s *Session) LimitQueue(max int, onOverflow func()) {
	s.mu.Lock()
	s.maxQueue, s.onOverflow = max, onOverflow
	s.mu.Unlock()
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
		if p = strings.TrimSpace(p); p != "" {
			s.patterns[p] = true
		}
	}
	if len(s.patterns) > 0 && s.unsub == nil {
		s.unsub = s.app.eventBus().SubscribeBounded("*", s.deliver, s.maxQueue, s.overflow)
	}
	return s.topicsLocked()
}

// overflow ends the subscriptions after the event queue limit was hit.
func (s *Session) overflow() {
	s.mu.Lock()
	clear(s.patterns)
	s.unsub = nil // already removed by the bus
	fn := s.onOverflow
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// deliver forwards e if one of the session's patterns matches.
func (s *Session) deliver(e bus.Event) {
	s.mu.Lock()
	ok := matchAny(s.patterns, e.Topic)
	s.mu.Unlock()
	if ok {
		s.notify(Event{Topic: e.Topic, Payload: encodePayload(e.Payload)})
	}
}

func matchAny(patterns map[string]bool, topic string) bool {
	for p := range patterns {
		if bus.Match(p, topic) {
			return true
		}
	}
	return false
}

// Unsubscribe stops forwarding the given patterns; without patterns all
// subscriptions end. It returns the remaining patterns.
func (s *Session) Unsubscribe(patterns ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(patterns) == 0 {
		clear(s.patterns)
	}
	for _, p := range patterns {
		delete(s.patterns, strings.TrimSpace(p))
	}
	if len(s.patterns) == 0 && s.unsub != nil {
		s.unsub()
		s.unsub = nil
	}
	return s.topicsLocked()
}

func (s *Session) topicsLocked() []string {
	out := make([]string, 0, len(s.patterns))
	for p := range s.patterns {
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
	// TopicDocRenamed: an open document follows its renamed or moved file
	// (DocRenamedEvent); doc.closed (old path) and doc.opened follow.
	TopicDocRenamed = "doc.renamed"
	// TopicFilesChanged: files were created, modified, renamed/moved or
	// deleted, through the API (files.*, source "tiegel") or found by the
	// project watcher (source "watcher"); FilesChangedEvent.
	TopicFilesChanged  = "files.changed"
	TopicModuleChanged = "modules.changed"
)
