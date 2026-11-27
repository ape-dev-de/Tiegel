// Package knowledge builds a project knowledge base (D560, D565): a
// top-down analysis in levels – overview (structure, stack, tooling;
// mostly mechanical), entry points (routes, commands, consumers;
// extracted mechanically, completed by an agent), processes (data flows
// per group of entry points) and components (data contracts of the
// classes the flows touch most) – plus open questions that later levels
// answer. Every page is described by one coding agent session, validated
// by a second one and checked mechanically against the code; Tiegel
// renders the Markdown itself (with Mermaid diagrams that are parsed
// before they are written), stores it outside the repository, chunks it
// and keeps a vector store for hybrid (embedding + keyword) search,
// which agents reach through an MCP server.
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

// Ref points into the code: a project relative file, a symbol and a
// line. Verified is set by Tiegel's mechanical check, not by agents.
type Ref struct {
	File     string `json:"file"`
	Symbol   string `json:"symbol,omitempty"`
	Line     int    `json:"line,omitempty"`
	Verified *bool  `json:"verified,omitempty"`
}

// Unverified reports a reference the mechanical check could not confirm.
func (r Ref) Unverified() bool { return r.Verified != nil && !*r.Verified }

// Question is an open question an agent asks (with the code that would
// settle it).
type Question struct {
	Question string `json:"question"`
	Refs     []Ref  `json:"refs,omitempty"`
}

// UnmarshalJSON also accepts a plain string.
func (q *Question) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*q = Question{Question: s}
		return nil
	}
	type plain Question
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*q = Question(p)
	return nil
}

// Answer answers an open question given in the prompt (by its ID).
type Answer struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
	Refs   []Ref  `json:"refs,omitempty"`
}

// QA are the question fields every level answers with.
type QA struct {
	// OpenQuestions are new questions the agent could not settle.
	OpenQuestions []Question `json:"openQuestions,omitempty"`
	// Answered are answers to questions listed in the prompt.
	Answered []Answer `json:"answeredQuestions,omitempty"`
}

func (q *QA) qa() *QA { return q }

func (q *QA) qaRefs() []*Ref {
	var out []*Ref
	for i := range q.OpenQuestions {
		for j := range q.OpenQuestions[i].Refs {
			out = append(out, &q.OpenQuestions[i].Refs[j])
		}
	}
	for i := range q.Answered {
		for j := range q.Answered[i].Refs {
			out = append(out, &q.Answered[i].Refs[j])
		}
	}
	return out
}

func (q *QA) checkQA(errs *[]string) {
	for i, a := range q.Answered {
		if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.Answer) == "" {
			*errs = append(*errs, fmt.Sprintf("answeredQuestions[%d] needs id and answer", i))
		}
	}
	for i, o := range q.OpenQuestions {
		if strings.TrimSpace(o.Question) == "" {
			*errs = append(*errs, fmt.Sprintf("openQuestions[%d].question is empty", i))
		}
	}
}

// Doc is the validated content of a page of one level.
type Doc interface {
	// Check reports schema violations.
	Check() error
	// refs are the code references (verified and fixed in place).
	refs() []*Ref
	qa() *QA
	// summary is the short text for index.md.
	summary() string
}

// OverviewDoc is level 0: what the system is, its containers and how
// data moves between them.
type OverviewDoc struct {
	// Summary says what data the system takes in, keeps and puts out.
	Summary string `json:"summary"`
	// Architecture is the style (MVC web app, headless API, CLI, worker
	// …) with the evidence.
	Architecture string      `json:"architecture"`
	Containers   []Container `json:"containers"`
	Relations    []Relation  `json:"relations,omitempty"`
	Folders      []Folder    `json:"folders,omitempty"`
	QA
}

// Container is a C4 container (or person / external system).
type Container struct {
	Name string `json:"name"`
	// Kind: person, webapp, api, worker, cli, database, cache, queue,
	// search, storage, mail, external.
	Kind        string `json:"kind"`
	Tech        string `json:"tech,omitempty"`
	Description string `json:"description,omitempty"`
	Refs        []Ref  `json:"refs,omitempty"`
}

// Relation is a data flow between containers.
type Relation struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

// Folder is the role of a folder in the data flows.
type Folder struct {
	Path string `json:"path"`
	Role string `json:"role"`
}

// Check implements Doc.
func (d *OverviewDoc) Check() error {
	var errs []string
	if strings.TrimSpace(d.Summary) == "" {
		errs = append(errs, "summary is empty")
	}
	if len(d.Containers) == 0 {
		errs = append(errs, "containers is empty (at least the application itself)")
	}
	for i, c := range d.Containers {
		if strings.TrimSpace(c.Name) == "" {
			errs = append(errs, fmt.Sprintf("containers[%d].name is empty", i))
		}
	}
	for i, r := range d.Relations {
		if strings.TrimSpace(r.From) == "" || strings.TrimSpace(r.To) == "" {
			errs = append(errs, fmt.Sprintf("relations[%d] needs from and to", i))
		}
	}
	d.checkQA(&errs)
	return joinErrs(errs)
}

func (d *OverviewDoc) refs() []*Ref {
	var out []*Ref
	for i := range d.Containers {
		for j := range d.Containers[i].Refs {
			out = append(out, &d.Containers[i].Refs[j])
		}
	}
	return append(out, d.qaRefs()...)
}

func (d *OverviewDoc) summary() string { return d.Summary }

// EntryPointsDoc is level 1: the entry points of one kind with the data
// they take and give.
type EntryPointsDoc struct {
	Summary string      `json:"summary"`
	Entries []EntryDesc `json:"entries"`
	QA
}

// EntryDesc describes an entry point.
type EntryDesc struct {
	// Name as in the inventory ("GET /orders/{id}", "app:import").
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`
	Handler Ref    `json:"handler"`
	// Input is what comes in (parameters, body, arguments, message).
	Input string `json:"input,omitempty"`
	// Output is what goes out (response, written data, events).
	Output string `json:"output,omitempty"`
	// Purpose is one line about the data it handles.
	Purpose string `json:"purpose"`
	// Process groups related entry points ("Checkout").
	Process string `json:"process,omitempty"`
	// Found tells whether Tiegel's inventory had it (set by Tiegel).
	Found bool `json:"found,omitempty"`
}

// Check implements Doc.
func (d *EntryPointsDoc) Check() error {
	var errs []string
	if strings.TrimSpace(d.Summary) == "" {
		errs = append(errs, "summary is empty")
	}
	for i, e := range d.Entries {
		if strings.TrimSpace(e.Name) == "" {
			errs = append(errs, fmt.Sprintf("entries[%d].name is empty", i))
		}
		if strings.TrimSpace(e.Purpose) == "" {
			errs = append(errs, fmt.Sprintf("entries[%d].purpose is empty", i))
		}
	}
	d.checkQA(&errs)
	return joinErrs(errs)
}

func (d *EntryPointsDoc) refs() []*Ref {
	var out []*Ref
	for i := range d.Entries {
		if d.Entries[i].Handler.File != "" {
			out = append(out, &d.Entries[i].Handler)
		}
	}
	return append(out, d.qaRefs()...)
}

func (d *EntryPointsDoc) summary() string { return d.Summary }

// ProcessDoc is level 2: the data flow of one process from its entry
// points to its effects.
type ProcessDoc struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Triggers are the entry points that start it.
	Triggers []string `json:"triggers"`
	Inputs   []Data   `json:"inputs,omitempty"`
	Steps    []Step   `json:"steps"`
	// Reads and Writes are data stores (tables, files, caches, queues).
	Reads   []Data `json:"reads,omitempty"`
	Writes  []Data `json:"writes,omitempty"`
	Outputs []Data `json:"outputs,omitempty"`
	// External are calls to systems outside (HTTP APIs, mail, payment).
	External []Data `json:"external,omitempty"`
	// Diagram is an optional Mermaid sequenceDiagram or flowchart.
	Diagram string `json:"diagram,omitempty"`
	QA
}

// Step is one step of a data flow in the code.
type Step struct {
	Ref
	// What happens to the data here.
	What string `json:"what"`
	// Data is the data at this step (shape, variables).
	Data string `json:"data,omitempty"`
}

// Data is a piece of data with where it lives.
type Data struct {
	Name string `json:"name"`
	// Kind: request, argument, message, database, file, cache, queue,
	// event, response, mail, http, other.
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
	Ref         *Ref   `json:"ref,omitempty"`
}

// UnmarshalJSON also accepts a plain string (the name).
func (d *Data) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*d = Data{Name: s}
		return nil
	}
	type plain Data
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*d = Data(p)
	return nil
}

// Check implements Doc.
func (d *ProcessDoc) Check() error {
	var errs []string
	if strings.TrimSpace(d.Name) == "" {
		errs = append(errs, "name is empty")
	}
	if strings.TrimSpace(d.Summary) == "" {
		errs = append(errs, "summary is empty")
	}
	if len(d.Steps) == 0 {
		errs = append(errs, "steps is empty")
	}
	for i, s := range d.Steps {
		if strings.TrimSpace(s.File) == "" {
			errs = append(errs, fmt.Sprintf("steps[%d].file is empty", i))
		}
		if strings.TrimSpace(s.What) == "" {
			errs = append(errs, fmt.Sprintf("steps[%d].what is empty", i))
		}
	}
	for _, l := range [][]Data{d.Inputs, d.Reads, d.Writes, d.Outputs, d.External} {
		for _, x := range l {
			if strings.TrimSpace(x.Name) == "" {
				errs = append(errs, "a data item has no name")
				break
			}
		}
	}
	d.checkQA(&errs)
	return joinErrs(errs)
}

func (d *ProcessDoc) refs() []*Ref {
	var out []*Ref
	for i := range d.Steps {
		out = append(out, &d.Steps[i].Ref)
	}
	for _, l := range [][]Data{d.Inputs, d.Reads, d.Writes, d.Outputs, d.External} {
		for i := range l {
			if l[i].Ref != nil {
				out = append(out, l[i].Ref)
			}
		}
	}
	return append(out, d.qaRefs()...)
}

func (d *ProcessDoc) summary() string { return d.Summary }

// ComponentDoc is level 3: the data contract of a class the processes
// touch.
type ComponentDoc struct {
	// Name is the class (FQN).
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Methods are the methods the flows use with their contracts.
	Methods []Method `json:"methods"`
	// State is what the class keeps or owns (tables, properties,
	// caches).
	State         string   `json:"state,omitempty"`
	Collaborators []string `json:"collaborators,omitempty"`
	QA
}

// Method is the data contract of a method.
type Method struct {
	Ref
	Inputs      string   `json:"inputs,omitempty"`
	Outputs     string   `json:"outputs,omitempty"`
	Reads       []string `json:"reads,omitempty"`
	Writes      []string `json:"writes,omitempty"`
	SideEffects []string `json:"sideEffects,omitempty"`
	Throws      []string `json:"throws,omitempty"`
}

// Check implements Doc.
func (d *ComponentDoc) Check() error {
	var errs []string
	if strings.TrimSpace(d.Name) == "" {
		errs = append(errs, "name is empty")
	}
	if strings.TrimSpace(d.Summary) == "" {
		errs = append(errs, "summary is empty")
	}
	for i, m := range d.Methods {
		if strings.TrimSpace(m.Symbol) == "" {
			errs = append(errs, fmt.Sprintf("methods[%d].symbol is empty", i))
		}
	}
	d.checkQA(&errs)
	return joinErrs(errs)
}

func (d *ComponentDoc) refs() []*Ref {
	var out []*Ref
	for i := range d.Methods {
		out = append(out, &d.Methods[i].Ref)
	}
	return append(out, d.qaRefs()...)
}

func (d *ComponentDoc) summary() string { return d.Summary }

// AnswersDoc is the "answer open questions" iteration: answers for the
// questions given, the IDs still open, and new questions.
type AnswersDoc struct {
	// StillOpen are the IDs that remain unknown after reading the code.
	StillOpen []string `json:"stillOpen,omitempty"`
	QA
}

// Check implements Doc.
func (d *AnswersDoc) Check() error {
	var errs []string
	if len(d.Answered) == 0 && len(d.StillOpen) == 0 {
		errs = append(errs, "neither answeredQuestions nor stillOpen")
	}
	d.checkQA(&errs)
	return joinErrs(errs)
}

func (d *AnswersDoc) refs() []*Ref { return d.qaRefs() }

func (d *AnswersDoc) summary() string {
	return fmt.Sprintf("%d answered, %d still open", len(d.Answered), len(d.StillOpen))
}

// NewDoc returns an empty document of a level.
func NewDoc(level string) Doc {
	switch level {
	case LevelOverview:
		return &OverviewDoc{}
	case LevelEntryPoints:
		return &EntryPointsDoc{}
	case LevelProcesses:
		return &ProcessDoc{}
	case LevelComponents:
		return &ComponentDoc{}
	case LevelQuestions:
		return &AnswersDoc{}
	}
	return nil
}

func joinErrs(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) > 8 {
		errs = append(errs[:8], fmt.Sprintf("… %d more", len(errs)-8))
	}
	return errors.New(strings.Join(errs, "; "))
}

// Validation is what the validating agent returns: the corrected
// document of the same shape.
type Validation struct {
	// Confidence is 0..1: how well the document matches the code.
	Confidence float64 `json:"confidence"`
	// Issues are the corrections made, in words.
	Issues []string `json:"issues,omitempty"`
	// Result is the corrected document.
	Result json.RawMessage `json:"result"`
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

// ParseDoc extracts the JSON of an answer into a document of the level
// and checks it.
func ParseDoc(level, answer string) (Doc, error) {
	d := NewDoc(level)
	if d == nil {
		return nil, fmt.Errorf("unknown level %q", level)
	}
	b, err := ExtractJSON(answer)
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, d); err != nil {
		return d, fmt.Errorf("does not match the schema: %w", err)
	}
	return d, d.Check()
}

// ParseValidation extracts a validation and its corrected document.
func ParseValidation(level, answer string) (Validation, Doc, error) {
	var v Validation
	b, err := ExtractJSON(answer)
	if err != nil {
		return v, nil, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, nil, fmt.Errorf("does not match the schema: %w", err)
	}
	if len(v.Result) == 0 || string(v.Result) == "null" {
		return v, nil, errors.New("result is missing")
	}
	if v.Confidence < 0 || v.Confidence > 1 {
		return v, nil, fmt.Errorf("confidence %v is not between 0 and 1", v.Confidence)
	}
	d := NewDoc(level)
	if d == nil {
		return v, nil, fmt.Errorf("unknown level %q", level)
	}
	if err := json.Unmarshal(v.Result, d); err != nil {
		return v, d, fmt.Errorf("result does not match the schema: %w", err)
	}
	return v, d, d.Check()
}

// Schema returns the JSON shape shown to the agents for a level.
func Schema(level string) string {
	switch level {
	case LevelOverview:
		return overviewSchema
	case LevelEntryPoints:
		return entryPointsSchema
	case LevelProcesses:
		return processSchema
	case LevelComponents:
		return componentSchema
	case LevelQuestions:
		return answersSchema
	}
	return "{}"
}

const qaSchema = `  "openQuestions": [{"question": "what the code does not settle", "refs": [{"file": "path.php", "symbol": "Class::method", "line": 12}]}],
  "answeredQuestions": [{"id": "q-… from the list of open questions", "answer": "the answer, concrete", "refs": [{"file": "path.php", "symbol": "Class::method", "line": 12}]}]`

const overviewSchema = `{
  "summary": "2-4 sentences: which data this system takes in, keeps and puts out – concrete names, no framework explanations",
  "architecture": "style (MVC web app, headless API, CLI tool, worker, library, mixed) and the evidence in this code",
  "containers": [{"name": "Shop web app", "kind": "person|webapp|api|worker|cli|database|cache|queue|search|storage|mail|external", "tech": "Symfony 7 / MySQL 8 / …", "description": "its role for the data, one line", "refs": [{"file": "config/packages/doctrine.yaml"}]}],
  "relations": [{"from": "Shop web app", "to": "MySQL", "label": "reads/writes orders, customers"}],
  "folders": [{"path": "src/Order", "role": "order placement and fulfilment"}],
` + qaSchema + `
}`

const entryPointsSchema = `{
  "summary": "1-3 sentences: which data enters through these entry points",
  "entries": [{"name": "GET /orders/{id} (as in the inventory)", "handler": {"file": "src/Controller/OrderController.php", "symbol": "App\\Controller\\OrderController::show", "line": 31}, "input": "route id, query ?expand", "output": "JSON order with lines", "purpose": "one line: the data it handles", "process": "short name of the process it belongs to"}],
` + qaSchema + `
}`

const processSchema = `{
  "name": "short name of the process",
  "summary": "2-3 sentences: where the data comes from, what transforms it, where it goes",
  "triggers": ["POST /checkout", "app:retry-payments"],
  "inputs": [{"name": "cart id + payment token", "kind": "request|argument|message|file|event|other", "description": "shape and origin"}],
  "steps": [{"file": "src/Checkout/CheckoutService.php", "symbol": "App\\Checkout\\CheckoutService::place", "line": 42, "what": "what happens to the data here", "data": "the data at this step"}],
  "reads": [{"name": "orders (table)", "kind": "database|file|cache|queue|http|other", "description": "which columns/keys", "ref": {"file": "src/Repository/OrderRepository.php", "symbol": "OrderRepository::find"}}],
  "writes": [{"name": "orders (table)", "kind": "database", "description": "INSERT with status 'new'"}],
  "outputs": [{"name": "201 JSON order", "kind": "response|event|mail|file|message|other", "description": "shape"}],
  "external": [{"name": "Stripe API", "kind": "http", "description": "POST /v1/payment_intents"}],
  "diagram": "optional Mermaid sequenceDiagram (participants = classes, messages = data); leave empty when unsure",
` + qaSchema + `
}`

const componentSchema = `{
  "name": "App\\Checkout\\CheckoutService (the class)",
  "summary": "2-3 sentences: its role in the data flows listed above",
  "methods": [{"file": "src/Checkout/CheckoutService.php", "symbol": "App\\Checkout\\CheckoutService::place", "line": 42, "inputs": "Cart $cart, string $token", "outputs": "Order (status new)", "reads": ["carts table"], "writes": ["orders table", "order_lines table"], "sideEffects": ["dispatches OrderPlaced"], "throws": ["PaymentFailed"]}],
  "state": "what it owns or keeps (tables, properties, caches)",
  "collaborators": ["App\\Payment\\StripeGateway"],
` + qaSchema + `
}`

const answersSchema = `{
  "answeredQuestions": [{"id": "q-…", "answer": "the answer, concrete, with the evidence", "refs": [{"file": "path.php", "symbol": "Class::method", "line": 12}]}],
  "stillOpen": ["q-… ids you could not settle from the code"],
  "openQuestions": [{"question": "a new question that came up", "refs": [{"file": "path.php"}]}]
}`
