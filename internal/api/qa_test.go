package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
)

// fakeQA contributes two synthetic tools: "qa-up" formats (upper-cases
// "x") and "qa-find" reports "TODO" lines.
func fakeQA(a *App, available *bool) {
	HookQATools.On(a.Hooks(), 0, func(_ context.Context, l []QATool) ([]QATool, error) {
		up := QATool{Plugin: "qa-up", Name: "Up", Order: 2, Formatter: true,
			Status: func(context.Context) QAToolStatus {
				return QAToolStatus{Available: *available, Reformat: true, Reason: "gone"}
			},
			Format: func(_ context.Context, r QAFormatRequest) ([]byte, error) {
				if strings.Contains(string(r.Content), "fail") {
					return nil, errors.New("cannot format")
				}
				return []byte(strings.ReplaceAll(string(r.Content), "x", "X")), nil
			}}
		find := QATool{Plugin: "qa-find", Name: "Find", Order: 1, Analyzer: true,
			Analyze: func(_ context.Context, r QAAnalyzeRequest) (QAReport, error) {
				var rep QAReport
				files := r.Paths
				if len(files) == 0 {
					files = []string{filepath.Join(a.ProjectRoot(), "a.php"), filepath.Join(a.ProjectRoot(), "b.php")}
				}
				for _, f := range files {
					data, _ := os.ReadFile(f)
					fd := QAFileDiagnostics{File: f}
					for i, line := range strings.Split(string(data), "\n") {
						if c := strings.Index(line, "TODO"); c >= 0 {
							fd.Diagnostics = append(fd.Diagnostics, QADiagnostic{Severity: "warning", Message: "todo", Line: i + 1, Col: c + 1})
						}
					}
					rep.Files = append(rep.Files, fd)
				}
				rep.Messages = []string{"done"}
				return rep, nil
			}}
		return append(slices.Clone(l), up, find), nil
	})
}

func TestQAService(t *testing.T) {
	a := newTestApp(t)
	avail := true
	fakeQA(a, &avail)
	wantCode(t, a, "qa.run", QARunParams{}, CodeNoProject)
	root := openProject(t, a, map[string]string{"a.php": "<?php\n// TODO x\n", "b.php": "<?php\n  echo 1; // TODO\n", "c.php": "fail"})

	var list []QAToolInfo
	call(t, a, "qa.list", nil, &list)
	if len(list) != 2 || list[0].Plugin != "qa-find" || list[1].Plugin != "qa-up" || !list[1].Reformat || list[1].Install {
		t.Fatalf("list %+v", list)
	}
	rec := make(chan QADiagnosticsEvent, 32)
	unsub := a.Bus().Subscribe(TopicQADiagnostics, func(e bus.Event) { rec <- e.Payload.(QADiagnosticsEvent) })
	defer unsub()

	var res QARunResult
	call(t, a, "qa.run", QARunParams{}, &res)
	if res.Count != 2 || len(res.Files) != 2 || len(res.Tools) != 1 || res.Tools[0].Messages[0] != "done" {
		t.Fatalf("run %+v", res)
	}
	d := res.Files[0].Diagnostics[0]
	// Column 4 ("TODO"): the word there.
	if res.Files[0].Path != "a.php" || d.Source != "qa-find" || d.Start != len("<?php\n// ") || d.End != len("<?php\n// TODO") {
		t.Fatalf("offsets %+v", res.Files[0])
	}
	// Fix b.php and inspect it alone: only its findings are cleared.
	writeFile(t, filepath.Join(root, "b.php"), "<?php\n")
	call(t, a, "qa.run", QARunParams{Tool: "qa-find", Paths: []string{"b.php"}}, &res)
	var stored []QADiagnosticsResult
	call(t, a, "qa.diagnostics", nil, &stored)
	if len(stored) != 1 || stored[0].Path != "a.php" || stored[0].Tool != "qa-find" {
		t.Fatalf("stored %+v", stored)
	}
	// Events are delivered asynchronously.
	for cleared := false; !cleared; {
		select {
		case ev := <-rec:
			cleared = ev.Path == "b.php" && len(ev.Diagnostics) == 0
		case <-time.After(10 * time.Second):
			t.Fatal("no clearing event for b.php")
		}
	}
	wantCode(t, a, "qa.run", QARunParams{Tool: "qa-up"}, CodeInvalidParams)
	wantCode(t, a, "qa.run", QARunParams{Tool: "qa-find", Paths: []string{filepath.Join(t.TempDir(), "elsewhere.php")}}, CodeInvalidParams)

	// qa.clear by path, then all on project close.
	var gone []string
	call(t, a, "qa.clear", QADiagnosticsParams{Path: "a.php"}, &gone)
	if len(gone) != 1 {
		t.Fatalf("clear %v", gone)
	}
	call(t, a, "qa.run", QARunParams{}, &res)
	call(t, a, "project.close", nil, nil)
	call(t, a, "qa.diagnostics", nil, &stored)
	if len(stored) != 0 {
		t.Fatalf("after close %+v", stored)
	}
}

func TestQAFormat(t *testing.T) {
	a := newTestApp(t)
	avail := true
	fakeQA(a, &avail)
	root := openProject(t, a, map[string]string{"a.php": "<?php x;\nkeep\nx\n", "b.php": "x", "c.php": "fail"})
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	var r QAFormatResult
	call(t, a, "qa.format", QAFormatParams{Path: "a.php", Apply: new(bool)}, &r)
	if r.Text != "<?php X;\nkeep\nX\n" || r.Edits != 2 || r.Applied {
		t.Fatalf("preview %+v", r)
	}
	call(t, a, "qa.format", QAFormatParams{Path: "a.php"}, &r)
	if !r.Open || r.Edits != 2 || r.Tool != "qa-up" || docText(t, a, "a.php") != "<?php X;\nkeep\nX\n" {
		t.Fatalf("open %+v", r)
	}
	call(t, a, "doc.undo", PathParams{Path: "a.php"}, nil)
	if docText(t, a, "a.php") != "<?php x;\nkeep\nx\n" {
		t.Fatal("one undo step expected")
	}
	call(t, a, "qa.format", QAFormatParams{Tool: "qa-up", Path: "b.php"}, &r)
	if b, _ := os.ReadFile(filepath.Join(root, "b.php")); string(b) != "X" || r.Open {
		t.Fatalf("closed %q %+v", b, r)
	}
	wantCode(t, a, "qa.format", QAFormatParams{Path: "c.php"}, CodeFailed)
	wantCode(t, a, "qa.format", QAFormatParams{}, CodeInvalidParams)
	wantCode(t, a, "qa.format", QAFormatParams{Tool: "qa-find", Path: "a.php"}, CodeInvalidParams)
	wantCode(t, a, "qa.format", QAFormatParams{Tool: "qa-nope", Path: "a.php"}, CodeNotFound)
	avail = false
	err := tryCall(a, "qa.format", QAFormatParams{Path: "a.php"}, nil)
	if ErrorCode(err) != CodeUnavailable || QAErrorReason(err) != qaNoFormatter {
		t.Fatalf("no formatter: %v", err)
	}
	err = tryCall(a, "qa.format", QAFormatParams{Tool: "qa-up", Path: "a.php"}, nil)
	if QAErrorReason(err) != "missing" || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("missing: %v", err)
	}
	if QAErrorReason(errors.New("x")) != "" {
		t.Fatal("plain error")
	}
}

func TestMinimalChangesAndOffsets(t *testing.T) {
	old, upd := []byte("a\nbcd\ne\n"), []byte("a\nbXd\ne\nf\n")
	ch := MinimalChanges(old, upd)
	if got, _ := ApplyChanges(old, ch); string(got) != string(upd) || len(ch) != 2 || ch[0].Text != "X" {
		t.Fatalf("changes %+v", ch)
	}
	if MinimalChanges(old, old) != nil {
		t.Fatal("no changes expected")
	}
	text := []byte("<?php\n    $abc = 1;   \r\nä x\n")
	cases := []struct {
		in         QADiagnostic
		start, end int
	}{
		{QADiagnostic{Line: 2}, 10, 19},          // first non-blank → end without blanks
		{QADiagnostic{Line: 2, Col: 5}, 10, 14},  // the word $abc
		{QADiagnostic{Line: 2, Col: 10}, 15, 16}, // "=": one character
		{QADiagnostic{Line: 3, Col: 3}, 27, 28},  // after "ä " (characters)
		{QADiagnostic{Line: 2, Col: 5, EndLine: 3, EndCol: 2}, 10, 26},
		{QADiagnostic{Line: 9}, len(text), len(text)},
		{QADiagnostic{Line: 0}, 0, 5},
	}
	for i, c := range cases {
		d := c.in
		fillQAOffsets(&d, text)
		if d.Start != c.start || d.End != c.end {
			t.Errorf("case %d: [%d,%d), want [%d,%d)", i, d.Start, d.End, c.start, c.end)
		}
	}
	d := QADiagnostic{Line: 3}
	fillQAOffsets(&d, nil)
	if d.Start != 0 || d.End != 0 {
		t.Fatal("no text")
	}
}

// A run in flight while the project closes drops its findings: they
// belong to the old project (review wave 7, #8).
func TestQARunAcrossProjectClose(t *testing.T) {
	a := newTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	HookQATools.On(a.Hooks(), 0, func(_ context.Context, l []QATool) ([]QATool, error) {
		slow := QATool{Plugin: "qa-slow", Name: "Slow", Analyzer: true,
			Analyze: func(_ context.Context, r QAAnalyzeRequest) (QAReport, error) {
				close(entered)
				<-release
				return QAReport{Files: []QAFileDiagnostics{{File: r.Paths[0],
					Diagnostics: []QADiagnostic{{Severity: "warning", Message: "late", Line: 1}}}}}, nil
			}}
		return append(slices.Clone(l), slow), nil
	})
	openProject(t, a, map[string]string{"a.php": "<?php\n"})
	rec := make(chan QADiagnosticsEvent, 32)
	unsub := a.Bus().Subscribe(TopicQADiagnostics, func(e bus.Event) { rec <- e.Payload.(QADiagnosticsEvent) })
	defer unsub()
	done := make(chan error, 1)
	go func() { done <- tryCall(a, "qa.run", QARunParams{Tool: "qa-slow", Paths: []string{"a.php"}}, nil) }()
	<-entered
	call(t, a, "project.close", nil, nil)
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "project was closed") {
		t.Fatalf("run across close: %v", err)
	}
	var stored []QADiagnosticsResult
	call(t, a, "qa.diagnostics", nil, &stored)
	if len(stored) != 0 {
		t.Fatalf("findings of the old project stored: %+v", stored)
	}
	// No event with findings of the old project (events are asynchronous).
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case ev := <-rec:
			if len(ev.Diagnostics) > 0 {
				t.Fatalf("published %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}
