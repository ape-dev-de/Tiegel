package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Question states (QuestionRecord.Status).
const (
	QuestionOpen     = "open"
	QuestionAnswered = "answered"
)

// MaxAttempts is how often the "answer questions" iteration tries a
// question before it stays open for good (until its code changes).
const MaxAttempts = 2

// QuestionRecord is a question of the store: asked by a page, answered
// by a later page or by the "answer questions" iteration.
type QuestionRecord struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level string `json:"level"`
	// Item is the page that asked it.
	Item   string `json:"item"`
	Refs   []Ref  `json:"refs,omitempty"`
	Status string `json:"status"`
	// Answer, AnswerRefs and AnsweredBy (the page or batch) are set once
	// answered.
	Answer     string    `json:"answer,omitempty"`
	AnswerRefs []Ref     `json:"answerRefs,omitempty"`
	AnsweredBy string    `json:"answeredBy,omitempty"`
	Asked      time.Time `json:"asked"`
	AnsweredAt time.Time `json:"answeredAt,omitzero"`
	// Attempts counts answer iterations that kept it open.
	Attempts int `json:"attempts,omitempty"`
}

// QuestionStore is questions.json.
type QuestionStore struct {
	Version   int               `json:"version"`
	Questions []*QuestionRecord `json:"questions"`
}

// LoadQuestions reads dir/questions.json (missing: empty store; broken:
// empty store and the error).
func LoadQuestions(dir string) (*QuestionStore, error) {
	s := &QuestionStore{Version: 1}
	b, err := os.ReadFile(filepath.Join(dir, "questions.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("knowledge questions: %w", err)
	}
	if err := json.Unmarshal(b, s); err != nil {
		return &QuestionStore{Version: 1}, fmt.Errorf("knowledge questions are broken (starting over): %w", err)
	}
	return s, nil
}

// Save writes dir/questions.json atomically.
func (s *QuestionStore) Save(dir string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(dir, "questions.json"), b)
}

// Counts returns the number of open and answered questions.
func (s *QuestionStore) Counts() (open, answered int) {
	for _, q := range s.Questions {
		if q.Status == QuestionAnswered {
			answered++
		} else {
			open++
		}
	}
	return open, answered
}

// Get returns a question by ID.
func (s *QuestionStore) Get(id string) *QuestionRecord {
	for _, q := range s.Questions {
		if q.ID == id {
			return q
		}
	}
	return nil
}

// QuestionID derives the stable ID of a question asked by a page.
func QuestionID(item, text string) string {
	return "q-" + HashString(item + "\x00" + strings.ToLower(strings.Join(strings.Fields(text), " ")))[:8]
}

// Apply records what a page found: answers to listed questions, new
// open questions; open questions the page asked before and did not ask
// again are dropped (the new analysis supersedes them). stillOpen are
// IDs a question batch tried and kept open.
func (s *QuestionStore) Apply(it Item, qa *QA, stillOpen []string, now time.Time) {
	for _, a := range qa.Answered {
		q := s.Get(strings.TrimSpace(a.ID))
		if q == nil || q.Status != QuestionOpen || strings.TrimSpace(a.Answer) == "" {
			continue // unknown or already answered
		}
		q.Status, q.Answer, q.AnswerRefs, q.AnsweredBy, q.AnsweredAt = QuestionAnswered, strings.TrimSpace(a.Answer), a.Refs, it.ID, now
	}
	asked := map[string]bool{}
	for _, q := range qa.OpenQuestions {
		text := strings.TrimSpace(q.Question)
		if text == "" {
			continue
		}
		id := QuestionID(it.ID, text)
		asked[id] = true
		if old := s.Get(id); old != nil {
			if old.Status == QuestionOpen {
				old.Refs = q.Refs
			}
			continue
		}
		s.Questions = append(s.Questions, &QuestionRecord{ID: id, Text: text, Level: it.Level, Item: it.ID, Refs: q.Refs, Status: QuestionOpen, Asked: now})
	}
	if it.Level != LevelQuestions {
		s.Questions = slices.DeleteFunc(s.Questions, func(q *QuestionRecord) bool {
			return q.Item == it.ID && q.Status == QuestionOpen && !asked[q.ID]
		})
	}
	for _, id := range stillOpen {
		if q := s.Get(id); q != nil && q.Status == QuestionOpen {
			q.Attempts++
		}
	}
}

// Relevant returns the open questions a page should see (at most n):
// its own earlier questions first, then those citing its files, then
// questions of earlier levels.
func (s *QuestionStore) Relevant(it Item, n int) []*QuestionRecord {
	files := map[string]bool{}
	for _, f := range it.Files {
		files[f] = true
	}
	type scored struct {
		q     *QuestionRecord
		score int
	}
	var l []scored
	for _, q := range s.Questions {
		if q.Status != QuestionOpen {
			continue
		}
		sc := 0
		switch {
		case q.Item == it.ID:
			sc = 100
		default:
			for _, r := range q.Refs {
				if files[r.File] {
					sc += 10
				}
			}
			if sc == 0 && LevelRank(q.Level) < LevelRank(it.Level) && len(q.Refs) == 0 {
				sc = 1
			}
		}
		if sc > 0 {
			l = append(l, scored{q, sc})
		}
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].score > l[j].score })
	var out []*QuestionRecord
	for i := 0; i < len(l) && i < n; i++ {
		out = append(out, l[i].q)
	}
	return out
}

// Answerable are the open questions the "answer questions" iteration
// still tries (fewer than MaxAttempts attempts).
func (s *QuestionStore) Answerable() []*QuestionRecord {
	var out []*QuestionRecord
	for _, q := range s.Questions {
		if q.Status == QuestionOpen && q.Attempts < MaxAttempts {
			out = append(out, q)
		}
	}
	return out
}

// QuestionBatch is the number of questions per "answer" call.
const QuestionBatch = 8

// PlanQuestions plans the "answer questions" iteration: batches of
// answerable questions (at most max batches; all: also questions that
// were tried MaxAttempts times).
func PlanQuestions(s *QuestionStore, max int, all bool) []Item {
	var qs []*QuestionRecord
	for _, q := range s.Questions {
		if q.Status == QuestionOpen && (all || q.Attempts < MaxAttempts) {
			qs = append(qs, q)
		}
	}
	var out []Item
	for i := 0; i < len(qs) && (max <= 0 || len(out) < max); i += QuestionBatch {
		b := qs[i:min(i+QuestionBatch, len(qs))]
		it := Item{ID: fmt.Sprintf("questions-%d", len(out)+1), Level: LevelQuestions, Title: fmt.Sprintf("Open questions %d–%d", i+1, i+len(b))}
		for _, q := range b {
			it.Questions = append(it.Questions, q.ID)
			for _, r := range q.Refs {
				if r.File != "" && !slices.Contains(it.Files, r.File) {
					it.Files = append(it.Files, r.File)
				}
			}
		}
		sort.Strings(it.Files)
		out = append(out, it)
	}
	return out
}
