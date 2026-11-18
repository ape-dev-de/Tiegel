package teamcity

import (
	"regexp"
	"strconv"
	"strings"
)

// Status is the state of a test or suite.
type Status string

// Statuses. A suite's status is derived from its children.
const (
	StatusRunning Status = "running"
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	// StatusIgnored covers skipped and incomplete tests.
	StatusIgnored Status = "ignored"
	// StatusTerminated marks tests that were running when the process
	// ended (stopped, crashed).
	StatusTerminated Status = "terminated"
)

// Kind classifies a node.
type Kind string

// Node kinds.
const (
	// KindSuite is a suite without location (configuration, test suite
	// of the XML configuration, directory).
	KindSuite Kind = "suite"
	// KindClass is a test class.
	KindClass Kind = "class"
	// KindMethod is a test method with a data provider (its children are
	// the data sets).
	KindMethod Kind = "method"
	// KindTest is a test method without data sets.
	KindTest Kind = "test"
	// KindDataSet is one data set of a method.
	KindDataSet Kind = "dataset"
)

// IsTest reports whether nodes of kind k are single tests (leaves).
func (k Kind) IsTest() bool { return k == KindTest || k == KindDataSet }

// Node is a suite or test of the result tree.
type Node struct {
	// ID identifies the node within a run: the qualified name of the
	// location hint ("Ns\Cls::method with data set #0"), or the path of
	// suite names for suites without location.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// Location is the raw location hint.
	Location string `json:"location,omitempty"`
	// File is the test file (mapped by Tree.MapPath).
	File string `json:"file,omitempty"`
	// Class is the class FQN (without leading backslash), Method the
	// method name and DataSet the data set label ("#0", "\"name\"").
	Class   string `json:"class,omitempty"`
	Method  string `json:"method,omitempty"`
	DataSet string `json:"dataSet,omitempty"`
	Status  Status `json:"status"`
	// DurationMs is reported for tests and summed up for suites.
	DurationMs int64 `json:"durationMs"`
	// Message, Details (stack trace) and – for comparison failures –
	// Expected and Actual of a failed or ignored test.
	Message    string `json:"message,omitempty"`
	Details    string `json:"details,omitempty"`
	Expected   string `json:"expected,omitempty"`
	Actual     string `json:"actual,omitempty"`
	Comparison bool   `json:"comparison,omitempty"`
	// FailFile and FailLine are the failure location from Details (the
	// first frame in the test file, else the first frame), mapped.
	FailFile string `json:"failFile,omitempty"`
	FailLine int    `json:"failLine,omitempty"`
	// Output is what the test printed.
	Output   string  `json:"output,omitempty"`
	Children []*Node `json:"children,omitempty"`

	flow       string
	parent     *Node
	reportFile string // File before mapping
}

// Counts summarise the tests of a tree.
type Counts struct {
	Total      int `json:"total"`
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Ignored    int `json:"ignored"`
	Running    int `json:"running"`
	Terminated int `json:"terminated"`
	// Expected is the announced number of tests (testCount, 0 if none).
	Expected int `json:"expected,omitempty"`
}

// Tree builds the result tree of one run from service messages. It is
// not safe for concurrent use.
type Tree struct {
	// MapPath maps a reported path (e.g. inside a container) to a host
	// path; nil keeps paths as reported.
	MapPath func(string) string

	roots    []*Node
	expected int
	stacks   map[string][]*Node // open suites per flow
	running  map[string]*Node   // open tests per flow
	byID     map[string]*Node
	lastOpen *Node
}

// NewTree returns an empty tree.
func NewTree() *Tree {
	return &Tree{stacks: map[string][]*Node{}, running: map[string]*Node{}, byID: map[string]*Node{}}
}

func (t *Tree) mapPath(p string) string {
	if p == "" || t.MapPath == nil {
		return p
	}
	return t.MapPath(p)
}

// Feed processes one output line: its service messages, and text that is
// test output (attached to the running test). It reports whether the
// tree changed.
func (t *Tree) Feed(line string) bool {
	msgs, before := Find(line)
	changed := false
	if before != "" && t.lastOpen != nil {
		n := t.lastOpen
		if n.Output != "" && len(msgs) == 0 {
			n.Output += "\n"
		}
		n.Output += before
		changed = true
	}
	for _, m := range msgs {
		if t.Apply(m) {
			changed = true
		}
	}
	return changed
}

// Apply applies one message and reports whether the tree changed.
func (t *Tree) Apply(m Message) bool {
	flow := m.Attr("flowId")
	switch m.Name {
	case "testCount":
		n, err := strconv.Atoi(m.Attr("count"))
		if err != nil {
			return false
		}
		t.expected += n
		return true
	case "testSuiteStarted":
		parent := t.top(flow)
		n := t.node(parent, m.Attr("name"), m.Attr("locationHint"), false, flow)
		t.stacks[flow] = append(t.stacks[flow], n)
		return true
	case "testSuiteFinished":
		st := t.stacks[flow]
		// Pop up to and including the suite with this name.
		for i := len(st) - 1; i >= 0; i-- {
			if st[i].Name == m.Attr("name") {
				t.stacks[flow] = st[:i]
				return true
			}
		}
		return false
	case "testStarted":
		parent := t.top(flow)
		n := t.node(parent, m.Attr("name"), m.Attr("locationHint"), true, flow)
		n.Status, n.Message, n.Details, n.Expected, n.Actual, n.Comparison = StatusRunning, "", "", "", "", false
		n.FailFile, n.FailLine, n.Output, n.DurationMs = "", 0, "", 0
		t.running[flow] = n
		t.lastOpen = n
		return true
	case "testFailed", "testIgnored", "testFinished", "testStdOut", "testStdErr":
		n := t.test(flow, m.Attr("name"))
		if n == nil {
			return false
		}
		switch m.Name {
		case "testFailed":
			n.Status = StatusFailed
			t.failure(n, m)
		case "testIgnored":
			if n.Status != StatusFailed {
				n.Status = StatusIgnored
			}
			t.failure(n, m)
		case "testStdOut", "testStdErr":
			n.Output += m.Attr("out")
		case "testFinished":
			if n.Status == StatusRunning {
				n.Status = StatusPassed
			}
			if d, err := strconv.ParseInt(m.Attr("duration"), 10, 64); err == nil {
				n.DurationMs = d
			}
			delete(t.running, flow)
			if t.lastOpen == n {
				t.lastOpen = nil
			}
		}
		if d, err := strconv.ParseInt(m.Attr("duration"), 10, 64); err == nil && m.Name != "testFinished" && d > n.DurationMs {
			n.DurationMs = d
		}
		return true
	}
	return false
}

// failure records message, details and the comparison of a failed or
// ignored test.
func (t *Tree) failure(n *Node, m Message) {
	if v := m.Attr("message"); v != "" || n.Message == "" {
		n.Message = v
	}
	if v := m.Attr("details"); v != "" {
		n.Details = strings.TrimRight(v, "\n")
	}
	_, hasE := m.Attrs["expected"]
	_, hasA := m.Attrs["actual"]
	if hasE || hasA {
		n.Comparison = true
		n.Expected, n.Actual = m.Attr("expected"), m.Attr("actual")
	}
	if f, l := FailureLocation(n.Details, n.reportFile); f != "" {
		n.FailFile, n.FailLine = t.mapPath(f), l
	}
}

// top returns the innermost open suite of flow (nil: top level).
func (t *Tree) top(flow string) *Node {
	if st := t.stacks[flow]; len(st) > 0 {
		return st[len(st)-1]
	}
	return nil
}

// test returns the running test called name in flow (or any running
// test with that name when the flow is unknown).
func (t *Tree) test(flow, name string) *Node {
	if n := t.running[flow]; n != nil && (n.Name == name || name == "") {
		return n
	}
	for _, n := range t.running {
		if n.Name == name {
			return n
		}
	}
	// A message after testFinished (e.g. testFailed for a risky test
	// printed late): the last node with that name.
	if p := t.top(flow); p != nil {
		for i := len(p.Children) - 1; i >= 0; i-- {
			if c := p.Children[i]; c.Name == name && c.Kind.IsTest() {
				return c
			}
		}
	}
	return nil
}

// node finds or creates the child called name under parent.
func (t *Tree) node(parent *Node, name, hint string, test bool, flow string) *Node {
	id := ""
	file, class, method, dataSet := "", "", "", ""
	if hint != "" {
		file, id = SplitLocation(hint)
		class, method, dataSet = SplitName(id)
	}
	if id == "" {
		id = name
		if parent != nil {
			id = parent.ID + "/" + name
		}
	}
	siblings := &t.roots
	if parent != nil {
		siblings = &parent.Children
	}
	for _, c := range *siblings {
		if c.ID == id {
			return c
		}
	}
	n := &Node{ID: id, Name: name, Location: hint, Class: class, Method: method, DataSet: dataSet, flow: flow, parent: parent, reportFile: file}
	n.File = t.mapPath(file)
	switch {
	case test && dataSet != "":
		n.Kind = KindDataSet
	case test:
		n.Kind = KindTest
	case hint == "":
		n.Kind = KindSuite
	case method != "":
		n.Kind = KindMethod
	case class != "":
		n.Kind = KindClass
	default:
		n.Kind = KindSuite
	}
	if test {
		n.Status = StatusRunning
	}
	*siblings = append(*siblings, n)
	if _, dup := t.byID[id]; !dup {
		t.byID[id] = n
	}
	return n
}

// Finish ends the run: tests still running are terminated (the process
// ended without finishing them).
func (t *Tree) Finish() {
	var walk func([]*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			if n.Kind.IsTest() && n.Status == StatusRunning {
				n.Status = StatusTerminated
			}
			walk(n.Children)
		}
	}
	walk(t.roots)
	t.running = map[string]*Node{}
	t.stacks = map[string][]*Node{}
	t.lastOpen = nil
}

// Find returns the node with id (nil if none).
func (t *Tree) Find(id string) *Node { return t.byID[id] }

// Snapshot returns a deep copy of the tree with suite statuses and
// durations aggregated from their children.
func (t *Tree) Snapshot() []Node {
	out := make([]Node, 0, len(t.roots))
	for _, r := range t.roots {
		out = append(out, *copyNode(r))
	}
	return out
}

func copyNode(n *Node) *Node {
	c := *n
	c.parent = nil
	if len(n.Children) == 0 {
		c.Children = nil
		return &c
	}
	c.Children = make([]*Node, len(n.Children))
	var sum int64
	for i, ch := range n.Children {
		c.Children[i] = copyNode(ch)
		sum += c.Children[i].DurationMs
	}
	if !n.Kind.IsTest() {
		c.DurationMs = sum
		c.Status = aggregate(c.Children)
	}
	return &c
}

// aggregate derives a suite status: running before failed/terminated
// before passed; ignored only when all children are ignored.
func aggregate(ch []*Node) Status {
	if len(ch) == 0 {
		return StatusPassed
	}
	running, failed, terminated, ignored := false, false, false, 0
	for _, c := range ch {
		switch c.Status {
		case StatusRunning:
			running = true
		case StatusFailed:
			failed = true
		case StatusTerminated:
			terminated = true
		case StatusIgnored:
			ignored++
		}
	}
	switch {
	case running:
		return StatusRunning
	case failed:
		return StatusFailed
	case terminated:
		return StatusTerminated
	case ignored == len(ch):
		return StatusIgnored
	}
	return StatusPassed
}

// Counts counts the tests (leaves) of the tree.
func (t *Tree) Counts() Counts {
	c := Counts{Expected: t.expected}
	var walk func([]*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			if n.Kind.IsTest() {
				c.Total++
				switch n.Status {
				case StatusPassed:
					c.Passed++
				case StatusFailed:
					c.Failed++
				case StatusIgnored:
					c.Ignored++
				case StatusRunning:
					c.Running++
				case StatusTerminated:
					c.Terminated++
				}
			}
			walk(n.Children)
		}
	}
	walk(t.roots)
	return c
}

// Tests returns the tests (leaves) with status st ("" = all), in tree
// order. The nodes belong to the tree.
func (t *Tree) Tests(st Status) []*Node {
	var out []*Node
	var walk func([]*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			if n.Kind.IsTest() && (st == "" || n.Status == st) {
				out = append(out, n)
			}
			walk(n.Children)
		}
	}
	walk(t.roots)
	return out
}

// Walk calls fn for every node of a snapshot, depth first.
func Walk(ns []Node, fn func(n *Node)) {
	for i := range ns {
		walkNode(&ns[i], fn)
	}
}

func walkNode(n *Node, fn func(*Node)) {
	fn(n)
	for _, c := range n.Children {
		walkNode(c, fn)
	}
}

// SplitLocation splits a location hint "php_qn://PATH::\Ns\Cls::method"
// into the path and the qualified name without leading backslash
// ("Ns\Cls::method"). Hints without scheme or name give "", "".
func SplitLocation(hint string) (path, name string) {
	_, rest, ok := strings.Cut(hint, "://")
	if !ok {
		return "", ""
	}
	i := strings.Index(rest, `::\`)
	if i < 0 {
		// No class: a file (e.g. "php_qn://PATH").
		return rest, ""
	}
	return rest[:i], rest[i+3:]
}

// dataSetSep separates a method from its data set in test names.
const dataSetSep = " with data set "

// SplitName splits "Ns\Cls::method with data set #0" into class, method
// and data set.
func SplitName(name string) (class, method, dataSet string) {
	class, rest, _ := strings.Cut(name, "::")
	method, dataSet, _ = strings.Cut(rest, dataSetSep)
	return class, method, dataSet
}

// frameRe matches "path:line" frames of a stack trace.
var frameRe = regexp.MustCompile(`^(.+):(\d+)$`)

// FailureLocation returns the failure location from details (one frame
// per line): the first frame in testFile, else the first frame.
func FailureLocation(details, testFile string) (string, int) {
	first, firstLine := "", 0
	for _, l := range strings.Split(details, "\n") {
		l = strings.TrimSpace(l)
		m := frameRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		if testFile != "" && m[1] == testFile {
			return m[1], n
		}
		if first == "" {
			first, firstLine = m[1], n
		}
	}
	return first, firstLine
}
