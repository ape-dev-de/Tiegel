package controller

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestQA(t *testing.T) {
	f := projectFake()
	tools := []api.QAToolInfo{
		{Plugin: "qa-a", Name: "Alpha", Formatter: true, QAToolStatus: api.QAToolStatus{Available: false, Reformat: true}, Install: true},
		{Plugin: "qa-b", Name: "Beta", Formatter: true, QAToolStatus: api.QAToolStatus{Available: true, Reformat: true}},
		{Plugin: "qa-c", Name: "Gamma", Analyzer: true, QAToolStatus: api.QAToolStatus{Available: false}, Install: true},
	}
	f.answer("qa.list", tools)
	c := started(t, f)
	c.Settle(time.Second)
	if len(c.QA.Tools) != 3 {
		t.Fatalf("tools %+v", c.QA.Tools)
	}
	if fm, ok := c.QA.Formatter(); !ok || fm.Plugin != "qa-b" {
		t.Fatalf("formatter %+v", fm)
	}

	// Findings per tool: Problems and squiggles, cleared by an empty list.
	file := filepath.Join(root, "index.php")
	v0 := c.DiagVersion
	f.emit(api.TopicQADiagnostics, api.QADiagnosticsEvent{Path: "index.php", File: file, Source: "qa-c", Diagnostics: []api.QADiagnostic{
		{Severity: "error", Message: "bad", Code: "x.y", Line: 2, Col: 0, Start: 6, End: 9},
	}})
	f.emit(api.TopicQADiagnostics, api.QADiagnosticsEvent{Path: "index.php", File: file, Source: "qa-b", Diagnostics: []api.QADiagnostic{
		{Severity: "warning", Message: "style", Line: 1, Col: 1, Start: 0, End: 5},
	}})
	c.Settle(time.Second)
	ds := c.Diagnostics(file)
	if c.DiagVersion == v0 || len(ds) != 2 || ds[0].Source != "Beta" || ds[1].Source != "Gamma · x.y" || ds[1].Col != 1 {
		t.Fatalf("diags %+v", ds)
	}
	if e, w := c.ProblemCounts(); e != 1 || w != 1 || len(c.Problems()) != 2 {
		t.Fatalf("counts %d %d", e, w)
	}
	f.emit(api.TopicQADiagnostics, api.QADiagnosticsEvent{Path: "index.php", File: file, Source: "qa-c", Diagnostics: []api.QADiagnostic{}})
	f.emit(api.TopicQADiagnostics, api.QADiagnosticsEvent{Path: "other.php", Source: "qa-c", Diagnostics: []api.QADiagnostic{}})
	c.Settle(time.Second)
	if ds := c.Diagnostics(file); len(ds) != 1 {
		t.Fatalf("after clear %+v", ds)
	}

	// Inspect without an available analyzer offers the installation.
	var offered []api.QAToolInfo
	c.Inspect(nil, func(l []api.QAToolInfo) { offered = l })
	if len(offered) != 1 || offered[0].Plugin != "qa-c" {
		t.Fatalf("offered %+v", offered)
	}
	tools[2].Available = true
	f.answer("qa.list", tools)
	f.answer("qa.run", api.QARunResult{Count: 3, Tools: []api.QAToolRun{{Tool: "qa-c", Count: 3}}})
	c.RefreshQA()
	c.Settle(time.Second)
	c.Inspect([]string{file}, nil)
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "Inspected index.php: 3 problem(s)") {
		t.Fatalf("notice %q", c.Status.Notice)
	}

	// Reformat with the formatter plugin.
	f.answer("qa.format", api.QAFormatResult{Tool: "qa-b", Changed: true, Edits: 2})
	if !c.FormatWithQA(file, nil) {
		t.Fatal("no formatter used")
	}
	c.Settle(time.Second)
	if c.Status.Notice != "Reformatted with Beta (2 changes)" {
		t.Fatalf("notice %q", c.Status.Notice)
	}
	var p api.QAFormatParams
	f.mu.Lock()
	_ = json.Unmarshal(f.params["qa.format"][0], &p)
	f.mu.Unlock()
	if p.Tool != "qa-b" || p.Path != file {
		t.Fatalf("params %+v", p)
	}
	// The tool vanished: the installation is offered.
	f.on("qa.format", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeUnavailable, "Beta: gone").WithData(map[string]string{"reason": "missing", "plugin": "qa-b"})
	})
	missing := ""
	c.FormatWithQA(file, func(p string) { missing = p })
	c.Settle(time.Second)
	if missing != "qa-b" || !strings.Contains(c.Status.Notice, "gone") {
		t.Fatalf("missing %q %q", missing, c.Status.Notice)
	}
	// Run events: errors become notices.
	f.emit(api.TopicQAStarted, api.QARunEvent{Tool: "qa-b"})
	f.emit(api.TopicQAFinished, api.QARunEvent{Tool: "qa-b", Error: "exit 3"})
	c.Settle(time.Second)
	if c.QA.Running != 0 || c.Status.Notice != "Beta: exit 3" {
		t.Fatalf("finished %d %q", c.QA.Running, c.Status.Notice)
	}
	// Without formatter plugins Reformat stays with the language server.
	f.answer("qa.list", []api.QAToolInfo{})
	c.RefreshQA()
	c.Settle(time.Second)
	if c.FormatWithQA(file, nil) {
		t.Fatal("formatter without plugins")
	}
	if r, _ := qaReason(nil); r != "" {
		t.Fatal("nil error reason")
	}
}
