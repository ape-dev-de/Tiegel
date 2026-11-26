package phpstanresults

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/modules/agent"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// fixtures are the PHPStan outputs of internal/phpstan/testdata (PHPStan
// 1.12/2.1/2.2 on Greeter.php and Shouting.php with a baseline).
const fixtures = "../../phpstan/testdata"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type tenv struct {
	t    *testing.T
	app  *api.App
	root string
	rec  *modtest.Recorder
	fake *acptest.Agent
}

// newEnv opens the fixture project with the phpstan module (and, with
// fake, the agent plugin connected to it). The 2.2 result cache lies in
// build/phpstan-tmp like PHPStan writes it for tmpDir: build/phpstan-tmp.
func newEnv(t *testing.T, fake *acptest.Agent, opts ...func(a *api.App, root string)) *tenv {
	t.Helper()
	debounce = 10 * time.Millisecond
	mods := []module.Module{New()}
	if fake != nil {
		mods = append(mods, agent.New(agent.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/fake/bin/" + n, nil }}))
	}
	a := modtest.NewApp(t, mods, nil)
	e := &tenv{t: t, app: a, fake: fake, rec: modtest.Record(t, a, api.TopicAnalysisChanged)}
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	files := map[string]string{
		"composer.json":                     `{"require-dev": {"phpstan/phpstan": "^2.2"}}`,
		"src/Greeter.php":                   fixture(t, "Greeter.php.txt"),
		"src/Shouting.php":                  fixture(t, "Shouting.php.txt"),
		"phpstan-baseline.neon":             fixture(t, "phpstan-baseline.neon"),
		"phpstan.neon":                      "includes:\n\t- phpstan-baseline.neon\n\nparameters:\n\tlevel: 6\n\tpaths:\n\t\t- src\n\ttmpDir: build/phpstan-tmp\n",
		"build/phpstan-tmp/resultCache.php": fixture(t, "resultCache-2.2.php"),
	}
	for n, c := range files {
		modtest.WriteFile(t, filepath.Join(dir, n), c)
	}
	// The cache was written before the sources were last touched.
	old := time.Now().Add(-time.Hour)
	for _, f := range []string{"src/Greeter.php", "src/Shouting.php"} {
		_ = os.Chtimes(filepath.Join(dir, f), old, old)
	}
	for _, o := range opts {
		o(a, dir)
	}
	if _, err := a.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	e.root = dir
	return e
}

func (e *tenv) call(method string, params, out any) {
	e.t.Helper()
	modtest.Call(e.t, e.app, method, params, out)
}

func (e *tenv) status() api.AnalysisStatus {
	e.t.Helper()
	var st api.AnalysisStatus
	e.call("analysis.status", nil, &st)
	return st
}

func (e *tenv) findings(path string) api.AnalysisFindings {
	e.t.Helper()
	var f api.AnalysisFindings
	e.call("analysis.findings", api.AnalysisFindingsParams{Path: path}, &f)
	return f
}

func (e *tenv) set(key string, v any) {
	e.t.Helper()
	raw, _ := json.Marshal(v)
	e.call("settings.set", api.SettingsSetParams{Key: key, Scope: "project", Value: raw}, nil)
}

// settle waits until no load or refresh is pending (the version stays).
func (e *tenv) settle() api.AnalysisStatus {
	e.t.Helper()
	st := e.status()
	for i := 0; i < 500; i++ {
		time.Sleep(4 * debounce)
		next := e.status()
		if next.Version == st.Version {
			return next
		}
		st = next
	}
	e.t.Fatal("never settles")
	return st
}

// waitCount waits until the store holds n findings.
func (e *tenv) waitCount(n int) api.AnalysisStatus {
	e.t.Helper()
	var st api.AnalysisStatus
	modtest.Eventually(e.t, "findings", func() bool {
		st = e.status()
		return st.Count == n
	})
	return st
}

func TestResultCache(t *testing.T) {
	e := newEnv(t, nil)
	st := e.waitCount(4)
	if st.Used == nil || st.Used.Kind != api.AnalysisSourceCache || st.Used.Format != "resultCache" || st.Used.Version != "2.2.17" || st.Used.Origin != "config" ||
		st.Files != 2 || st.Stale != 0 || st.Config != filepath.Join(e.root, "phpstan.neon") || !st.Inline || st.FixWithAI {
		t.Fatalf("%+v", st)
	}
	if st.FixReason == "" {
		t.Fatal("no reason without the agent plugin")
	}
	e.rec.Wait(t, api.TopicAnalysisChanged, nil)
	f := e.findings("src/Greeter.php")
	if f.Count != 3 || len(f.Files) != 1 || f.Files[0].Path != "src/Greeter.php" {
		t.Fatalf("%+v", f)
	}
	text, _ := os.ReadFile(filepath.Join(e.root, "src/Greeter.php"))
	got := map[int]string{}
	for _, x := range f.Files[0].Findings {
		got[x.Line] = string(text[x.Start:x.End])
	}
	if got[13] != "missing" || got[18] != "strlen" || got[26] != "items" {
		t.Fatalf("ranges %v", got)
	}
	if fs := f.Files[0].Findings; fs[2].Tip == "" || fs[2].Identifier != "missingType.iterableValue" {
		t.Fatalf("%+v", fs[2])
	}
	// The trait error is shown in the trait.
	if f := e.findings("src/Shouting.php"); f.Count != 1 || f.Files[0].Findings[0].Line != 11 {
		t.Fatalf("%+v", f)
	}

	// A file changed after the cache: its findings are stale; a line that
	// no longer exists drops its finding.
	short := strings.Join(strings.Split(string(text), "\n")[:20], "\n") + "\n"
	modtest.WriteFile(t, filepath.Join(e.root, "src/Greeter.php"), short)
	e.app.Bus().Publish(api.TopicFilesChanged, api.FilesChangedEvent{Changes: []api.FileChange{{Kind: "modified", Path: "src/Greeter.php", File: filepath.Join(e.root, "src/Greeter.php")}}})
	e.waitCount(3)
	st = e.settle()
	if st.Stale != 2 || st.Count != 3 {
		t.Fatalf("%+v", st)
	}

	// ifChanged does nothing while the cache stays; a new cache reloads.
	var r api.AnalysisStatus
	e.call("analysis.reload", api.AnalysisReloadParams{IfChanged: true}, &r)
	if r.Version != st.Version {
		t.Fatalf("reloaded without change: %d → %d", st.Version, r.Version)
	}
	cache := filepath.Join(e.root, "build/phpstan-tmp/resultCache.php")
	modtest.WriteFile(t, cache, fixture(t, "resultCache-2.2.php")+"")
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(cache, future, future)
	e.call("analysis.reload", api.AnalysisReloadParams{IfChanged: true}, &r)
	if r.Version == st.Version || r.Stale != 0 {
		t.Fatalf("%+v", r)
	}
	// watch.refreshed checks the cache too (outside-the-project caches).
	_ = os.Chtimes(cache, future.Add(time.Minute), future.Add(time.Minute))
	e.app.Bus().Publish(api.TopicWatchRefreshed, nil)
	modtest.Eventually(t, "reload on watch.refreshed", func() bool { return e.status().Version > r.Version })

	// Off: no source.
	e.set(SettingUseCache, false)
	st = e.waitCount(0)
	if st.Used != nil || st.Sources[0].Reason == "" {
		t.Fatalf("%+v", st)
	}
	// Closing the project forgets everything.
	e.set(SettingUseCache, true)
	e.waitCount(3)
	e.call("project.close", nil, nil)
	modtest.WantCode(t, e.app, "analysis.status", nil, api.CodeNoProject)
}

func TestReports(t *testing.T) {
	e := newEnv(t, nil)
	e.waitCount(4)
	// A JSON report from a container: paths under /var/www/html.
	json := strings.ReplaceAll(fixture(t, "report.json"), "/var/www/html", e.root)
	modtest.WriteFile(t, filepath.Join(e.root, "build/phpstan.json"), json)
	modtest.WriteFile(t, filepath.Join(e.root, "build/phpstan-checkstyle.xml"), fixture(t, "report-checkstyle.xml"))
	modtest.WriteFile(t, filepath.Join(e.root, "build/other.xml"), "<x/>")
	var st api.AnalysisStatus
	e.call("analysis.reload", api.AnalysisReloadParams{}, &st)
	found := 0
	for _, s := range st.Sources {
		if s.Origin == "found" {
			found++
			if s.Used {
				t.Fatal("found reports are never used silently")
			}
		}
	}
	if found != 2 || st.Used.Kind != api.AnalysisSourceCache {
		t.Fatalf("%+v", st.Sources)
	}
	// Candidates of the setting.
	var cands api.CandidatesResult
	e.call("settings.candidates", api.CandidatesParams{Key: SettingReportFile}, &cands)
	if len(cands.Candidates) != 2 || cands.Candidates[0].Value != "build/phpstan-checkstyle.xml" && cands.Candidates[0].Value != "build/phpstan.json" {
		t.Fatalf("%+v", cands)
	}
	e.call("analysis.import", api.AnalysisImportParams{File: "build/phpstan.json"}, &st)
	if st.Used == nil || st.Used.Origin != "import" || st.Used.Format != "json" || st.Count != 4 {
		t.Fatalf("%+v", st)
	}
	e.set(SettingReportFile, "build/phpstan-checkstyle.xml")
	modtest.Eventually(t, "setting", func() bool { return e.status().Used.Origin == "import" })
	e.call("analysis.reload", api.AnalysisReloadParams{Reset: true}, &st)
	if st.Used == nil || st.Used.Origin != "setting" || st.Used.Format != "checkstyle" || st.Count != 4 {
		t.Fatalf("%+v", st)
	}
	modtest.WantCode(t, e.app, "analysis.import", api.AnalysisImportParams{File: "build/none.json"}, api.CodeNotFound)
	// A broken report reports the error and keeps the source listed.
	modtest.WriteFile(t, filepath.Join(e.root, "build/phpstan-checkstyle.xml"), "<checkstyle><file")
	modtest.WantCode(t, e.app, "analysis.reload", api.AnalysisReloadParams{}, api.CodeFailed)
	if st := e.status(); st.Error == "" || st.Count != 0 {
		t.Fatalf("%+v", st)
	}
}

// fakeContainer is an interpreter that maps the project to /var/www/html.
type fakeContainer struct{}

func (fakeContainer) ID() string                                    { return "compose:php" }
func (fakeContainer) Label() string                                 { return "fake container" }
func (fakeContainer) Kind() interp.Kind                             { return "compose" }
func (fakeContainer) Version(context.Context) (string, error)       { return "8.3.0", nil }
func (fakeContainer) Modules(context.Context) ([]string, error)     { return nil, nil }
func (fakeContainer) Ini(context.Context) (interp.IniValues, error) { return nil, nil }
func (fakeContainer) Command(context.Context, ...string) (*exec.Cmd, error) {
	return nil, os.ErrInvalid
}

func TestContainerPaths(t *testing.T) {
	e := newEnv(t, nil, func(a *api.App, root string) {
		api.HookRunInterpreter.On(a.Hooks(), 1, func(_ context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
			q.Interpreter = fakeContainer{}
			q.Mappings = []api.MappingInfo{{Host: root, Remote: "/var/www/html"}}
			return q, nil
		})
		// PHPStan 1.12 in the container wrote absolute container paths;
		// tmpDir is a container path inside the mapped project.
		modtest.WriteFile(t, filepath.Join(root, "phpstan.neon"), "includes:\n\t- phpstan-baseline.neon\nparameters:\n\ttmpDir: /var/www/html/var/phpstan\n")
		modtest.WriteFile(t, filepath.Join(root, "var/phpstan/resultCache.php"), fixture(t, "resultCache-1.12.php"))
	})
	st := e.waitCount(4)
	if st.Used == nil || st.Used.File != filepath.Join(e.root, "var/phpstan/resultCache.php") || st.Used.Version != "1.12.34" {
		t.Fatalf("%+v", st)
	}
	// Without tmpDir the cache is in the container's temp dir: explained.
	modtest.WriteFile(t, filepath.Join(e.root, "phpstan.neon"), "parameters:\n\tlevel: 1\n")
	e.call("analysis.reload", api.AnalysisReloadParams{}, &st)
	if st.Used != nil || !strings.Contains(st.Sources[0].Reason, "tmpDir") {
		t.Fatalf("%+v", st.Sources)
	}
}

// fakeQA is a PHPStan QA tool whose run finds one error in Greeter.php.
func fakeQA(a *api.App, root string) {
	api.HookQATools.On(a.Hooks(), 1, func(_ context.Context, l []api.QATool) ([]api.QATool, error) {
		return append(l, api.QATool{Plugin: qaTool, Name: "PHPStan", Analyzer: true,
			Analyze: func(context.Context, api.QAAnalyzeRequest) (api.QAReport, error) {
				return api.QAReport{Files: []api.QAFileDiagnostics{{File: filepath.Join(root, "src/Greeter.php"), Diagnostics: []api.QADiagnostic{
					{Severity: "error", Message: "Fresh from the run.", Code: "run.only", Line: 13},
				}}}}, nil
			}}), nil
	})
}

func TestOwnRunsAreFolded(t *testing.T) {
	e := newEnv(t, nil, fakeQA)
	e.waitCount(4)
	var res api.QARunResult
	e.call("qa.run", api.QARunParams{Tool: qaTool, Paths: []string{"src/Greeter.php"}}, &res)
	// Greeter.php now has the run's finding only; Shouting.php keeps the
	// cache's.
	modtest.Eventually(t, "fold", func() bool { return e.status().Count == 2 })
	f := e.findings("src/Greeter.php")
	if f.Count != 1 || f.Files[0].Findings[0].Identifier != "run.only" {
		t.Fatalf("%+v", f)
	}
}

func TestFixWithAI(t *testing.T) {
	// Without the agent plugin: the prompt can be built, not sent.
	e := newEnv(t, nil)
	e.waitCount(4)
	var r api.AnalysisFixResult
	e.call("analysis.fix", api.AnalysisFixParams{Path: "src/Greeter.php", Line: 13, DryRun: true}, &r)
	for _, want := range []string{"src/Greeter.php at line 13", "Call to an undefined method Acme\\Greeter::missing().", "Identifier: method.notFound",
		"> 13 |         return 'Hello ' . $name . $this->missing();", "do not commit"} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, r.Prompt)
		}
	}
	modtest.WantCode(t, e.app, "analysis.fix", api.AnalysisFixParams{Path: "src/Greeter.php", Line: 13}, api.CodeUnavailable)
	modtest.WantCode(t, e.app, "analysis.fix", api.AnalysisFixParams{Path: "src/Greeter.php", Line: 14, DryRun: true}, api.CodeNotFound)
	modtest.WantCode(t, e.app, "analysis.fix", api.AnalysisFixParams{Path: "src/Greeter.php"}, api.CodeInvalidParams)

	// With the agent plugin and a fake agent: offered and sent.
	var mu sync.Mutex
	var seen []string
	fake := &acptest.Agent{Script: func(_ context.Context, turn *acptest.Turn) string {
		turn.Text("Fixed.")
		return ""
	}}
	e = newEnv(t, fake)
	e.waitCount(4)
	modtest.Eventually(t, "fix offered", func() bool { return e.status().FixWithAI })
	un := e.app.Bus().Subscribe(api.TopicAgentUpdate, func(ev bus.Event) {
		mu.Lock()
		defer mu.Unlock()
		if u, ok := ev.Payload.(api.AgentUpdateEvent); ok && u.Entry.Kind == api.EntryUser {
			seen = append(seen, u.Entry.Text)
		}
	})
	defer un()
	e.call("analysis.fix", api.AnalysisFixParams{Path: "src/Greeter.php", Line: 26, Wait: true, Ask: api.PolicyDeny}, &r)
	if !r.Sent || r.Turn.StopReason != "end_turn" || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
	p := fake.Prompts()
	if len(p) != 1 || !strings.Contains(p[0], "Tip: See: https://phpstan.org/blog/solving-phpstan-no-value-type-specified-in-iterable-type") ||
		!strings.Contains(p[0], "missingType.iterableValue") {
		t.Fatalf("%q", p)
	}
	// Disabling the agent plugin withdraws the offer.
	e.call("modules.disable", api.ModuleParams{ID: agent.ID}, nil)
	modtest.Eventually(t, "fix withdrawn", func() bool { return !e.status().FixWithAI })
}
