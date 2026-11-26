// Package knowledge builds a project knowledge base (D560): coding agents
// describe the data flows of a PHP project unit by unit, a second pass
// validates the claims against the code, a third writes compact Markdown.
// Tiegel stores the Markdown outside the repository, chunks it and keeps
// a vector store for hybrid (embedding + keyword) search, which the agent
// reaches through an MCP server.
//
// The package is backend only: no Gio, no API types. Agents are reached
// through the Agent interface, embeddings through Embedder, and vectors
// are kept behind Store, so another backend (e.g. Postgres with pgvector)
// can replace the file store later.
package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Description is what the "describe" agent returns for a unit (as a
// fenced JSON block) and what the "validate" agent corrects.
type Description struct {
	// Summary says in two or three sentences what the unit does.
	Summary string `json:"summary"`
	// Flows are the data flows of the unit.
	Flows []Flow `json:"flows"`
	// Entities are the data structures that flow (models, DTOs, tables).
	Entities []Entity `json:"entities,omitempty"`
	// External are systems outside the unit (databases, HTTP APIs,
	// queues, mail, file system).
	External []External `json:"external,omitempty"`
	// OpenQuestions are things the agent could not settle.
	OpenQuestions []string `json:"openQuestions,omitempty"`
}

// Flow is one data flow from a trigger to its effects.
type Flow struct {
	Name string `json:"name"`
	// Trigger is what starts the flow (route, command, event, call).
	Trigger string `json:"trigger"`
	Steps   []Step `json:"steps"`
	// Data describes the data that flows (shape, origin).
	Data string `json:"data,omitempty"`
	// SideEffects are writes, calls, events and other effects.
	SideEffects []string `json:"sideEffects,omitempty"`
}

// Step is one step of a flow in the code.
type Step struct {
	// File is project relative; Symbol is Class::method, a function or
	// a class; Line is optional (1-based).
	File   string `json:"file"`
	Symbol string `json:"symbol,omitempty"`
	Line   int    `json:"line,omitempty"`
	What   string `json:"what"`
	// Verified is set by Tiegel's mechanical check (not by agents).
	Verified *bool `json:"verified,omitempty"`
}

// Entity is a data structure of the unit.
type Entity struct {
	Name        string `json:"name"`
	File        string `json:"file,omitempty"`
	Description string `json:"description,omitempty"`
}

// UnmarshalJSON also accepts a plain string (the entity's name).
func (e *Entity) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*e = Entity{Name: s}
		return nil
	}
	type plain Entity
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*e = Entity(p)
	return nil
}

// External is a system outside the unit.
type External struct {
	Name string `json:"name"`
	// Kind: database, http, queue, mail, filesystem, cache, other.
	Kind        string `json:"kind,omitempty"`
	File        string `json:"file,omitempty"`
	Description string `json:"description,omitempty"`
}

// UnmarshalJSON also accepts a plain string (the system's name).
func (e *External) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*e = External{Name: s}
		return nil
	}
	type plain External
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*e = External(p)
	return nil
}

// Validation is what the "validate" agent returns.
type Validation struct {
	// Confidence is 0..1: how well the description matches the code.
	Confidence float64 `json:"confidence"`
	// Issues are the corrections made, in words.
	Issues []string `json:"issues,omitempty"`
	// Description is the corrected description.
	Description *Description `json:"description"`
}

// ErrNoJSON means the answer contained no JSON block.
var ErrNoJSON = errors.New("no JSON block in the answer")

var fenceRE = regexp.MustCompile("(?s)```(?:json|JSON)?[ \t]*\r?\n(.*?)\r?\n[ \t]*```")

// ExtractJSON returns the JSON object of an agent answer: the last fenced
// block that parses as an object, else the outermost {...} of the text.
func ExtractJSON(answer string) ([]byte, error) {
	ms := fenceRE.FindAllStringSubmatch(answer, -1)
	for i := len(ms) - 1; i >= 0; i-- {
		b := bytes.TrimSpace([]byte(ms[i][1]))
		if len(b) > 0 && b[0] == '{' && json.Valid(b) {
			return b, nil
		}
	}
	if i, j := strings.IndexByte(answer, '{'), strings.LastIndexByte(answer, '}'); i >= 0 && j > i {
		b := []byte(answer[i : j+1])
		if json.Valid(b) {
			return b, nil
		}
		return nil, fmt.Errorf("invalid JSON: %w", json.Unmarshal(b, new(any)))
	}
	return nil, ErrNoJSON
}

// ParseDescription extracts and checks a Description.
func ParseDescription(answer string) (Description, error) {
	var d Description
	b, err := ExtractJSON(answer)
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("does not match the schema: %w", err)
	}
	return d, d.Check()
}

// ParseValidation extracts and checks a Validation.
func ParseValidation(answer string) (Validation, error) {
	var v Validation
	b, err := ExtractJSON(answer)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("does not match the schema: %w", err)
	}
	if v.Description == nil {
		return v, errors.New("description is missing")
	}
	if v.Confidence < 0 || v.Confidence > 1 {
		return v, fmt.Errorf("confidence %v is not between 0 and 1", v.Confidence)
	}
	return v, v.Description.Check()
}

// Check reports schema violations: a summary, named flows with a
// trigger, steps with a file and a description.
func (d *Description) Check() error {
	var errs []string
	if strings.TrimSpace(d.Summary) == "" {
		errs = append(errs, "summary is empty")
	}
	for i, f := range d.Flows {
		if strings.TrimSpace(f.Name) == "" {
			errs = append(errs, fmt.Sprintf("flows[%d].name is empty", i))
		}
		if strings.TrimSpace(f.Trigger) == "" {
			errs = append(errs, fmt.Sprintf("flows[%d].trigger is empty", i))
		}
		if len(f.Steps) == 0 {
			errs = append(errs, fmt.Sprintf("flows[%d].steps is empty", i))
		}
		for j, s := range f.Steps {
			if strings.TrimSpace(s.File) == "" {
				errs = append(errs, fmt.Sprintf("flows[%d].steps[%d].file is empty", i, j))
			}
			if strings.TrimSpace(s.What) == "" {
				errs = append(errs, fmt.Sprintf("flows[%d].steps[%d].what is empty", i, j))
			}
		}
	}
	for i, e := range d.Entities {
		if strings.TrimSpace(e.Name) == "" {
			errs = append(errs, fmt.Sprintf("entities[%d].name is empty", i))
		}
	}
	if len(errs) > 0 {
		if len(errs) > 8 {
			errs = append(errs[:8], fmt.Sprintf("… %d more", len(errs)-8))
		}
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// DescriptionSchema is the JSON shape shown to the agents.
const DescriptionSchema = `{
  "summary": "2-3 sentences: what this part of the code does",
  "flows": [
    {
      "name": "short name of the flow",
      "trigger": "what starts it: HTTP route, CLI command, event, job, public method",
      "steps": [
        {"file": "project/relative/path.php", "symbol": "Class::method or function", "line": 42, "what": "what happens to the data here"}
      ],
      "data": "the data that flows (shape, origin)",
      "sideEffects": ["DB write to orders", "dispatches OrderPlaced", "HTTP call to the payment API"]
    }
  ],
  "entities": [{"name": "Order", "file": "src/Entity/Order.php", "description": "one line"}],
  "external": [{"name": "MySQL", "kind": "database|http|queue|mail|filesystem|cache|other", "file": "where it is called", "description": "one line"}],
  "openQuestions": ["what you could not settle from the code"]
}`
