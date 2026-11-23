package phpunit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// The test binary plays PHPUnit (portable, no php needed): with
// fakeEnv set it prints TeamCity messages for the fixture's tests that
// match --filter, as a container would (paths below /app).
const (
	fakeEnv     = "TIEGEL_FAKE_PHPUNIT"
	fakeArgsEnv = "TIEGEL_FAKE_PHPUNIT_ARGS"
	// fakeSpamEnv makes testAdd print that many lines of ~95 bytes.
	fakeSpamEnv = "TIEGEL_FAKE_PHPUNIT_SPAM"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) != "" {
		os.Exit(fakePHPUnit(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeTests are the tests of tests/CalcTest.php: name, failure line.
var fakeTests = []struct {
	name string
	line int
}{
	{"testAdd", 0}, {"testFail", 17}, {"itSkips", -1}, {`testPairs with data set "same"`, 0}, {`testPairs with data set "diff"`, 29},
}

func fakePHPUnit(args []string) int {
	if f := os.Getenv(fakeArgsEnv); f != "" {
		wd, _ := os.Getwd()
		b, _ := json.Marshal(map[string]any{"args": args, "env": os.Environ(), "dir": wd})
		_ = os.WriteFile(f, b, 0o644)
	}
	var filter *regexp.Regexp
	for i, a := range args {
		if a == "--filter" && i+1 < len(args) {
			f := args[i+1]
			if strings.HasPrefix(f, "/") {
				f = strings.TrimSuffix(strings.TrimPrefix(f, "/"), "/")
			}
			filter = regexp.MustCompile(f)
		}
	}
	file := "/app/tests/CalcTest.php"
	hint := "php_qn://" + file + `::\Tests\CalcTest`
	fmt.Println("PHPUnit 11.5.0 by Sebastian Bergmann and contributors.")
	fmt.Println(teamcity.Format("testSuiteStarted", "name", `Tests\CalcTest`, "locationHint", hint, "flowId", "7"))
	failed := false
	inPairs := false
	for _, ft := range fakeTests {
		if filter != nil && !filter.MatchString(`Tests\CalcTest::`+ft.name) {
			continue
		}
		method, _, ds := strings.Cut(ft.name, " with data set ")
		_ = method
		if ds && !inPairs {
			fmt.Println(teamcity.Format("testSuiteStarted", "name", method, "locationHint", hint+"::"+method, "flowId", "7"))
			inPairs = true
		}
		fmt.Println(teamcity.Format("testStarted", "name", ft.name, "locationHint", hint+"::"+ft.name, "flowId", "7"))
		if n, _ := strconv.Atoi(os.Getenv(fakeSpamEnv)); n > 0 && ft.name == "testAdd" {
			w := bufio.NewWriter(os.Stdout)
			for i := 0; i < n; i++ {
				fmt.Fprintf(w, "spam %07d %s\n", i, strings.Repeat("x", 80))
			}
			_ = w.Flush()
		}
		switch {
		case ft.line > 0:
			failed = true
			fmt.Println(teamcity.Format("testFailed", "name", ft.name, "message", "Failed asserting that two strings are identical.",
				"details", fmt.Sprintf("%s:%d\n", file, ft.line), "type", "comparisonFailure", "expected", "'foo'", "actual", "'bar'", "flowId", "7"))
		case ft.line < 0:
			fmt.Println(teamcity.Format("testIgnored", "name", ft.name, "message", "later", "flowId", "7"))
		}
		fmt.Println(teamcity.Format("testFinished", "name", ft.name, "duration", "3", "flowId", "7"))
	}
	if inPairs {
		fmt.Println(teamcity.Format("testSuiteFinished", "name", "testPairs", "flowId", "7"))
	}
	fmt.Println(teamcity.Format("testSuiteFinished", "name", `Tests\CalcTest`, "flowId", "7"))
	if failed {
		return 1
	}
	return 0
}

// fakeInterp runs php as the test binary in fake mode.
type fakeInterp struct {
	self, argsFile string
}

func (fakeInterp) ID() string                                    { return "fake:php" }
func (fakeInterp) Label() string                                 { return "Fake PHP" }
func (fakeInterp) Kind() interp.Kind                             { return "fake" }
func (fakeInterp) Version(context.Context) (string, error)       { return "8.3.0", nil }
func (fakeInterp) Modules(context.Context) ([]string, error)     { return nil, nil }
func (fakeInterp) Ini(context.Context) (interp.IniValues, error) { return nil, nil }
func (f fakeInterp) Command(_ context.Context, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(f.self, args...)
	cmd.Env = append(os.Environ(), fakeEnv+"=1", fakeArgsEnv+"="+f.argsFile)
	return cmd, nil
}

const calcTest = `<?php
namespace Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test as It;

final class CalcTest extends TestCase
{
    public function testAdd(): void
    {
        $this->assertSame(2, 1 + 1);
    }

    public function testFail(): void
    {
        $this->assertSame("foo", "bar");
    }

    #[It]
    public function itSkips(): void
    {
        $this->markTestSkipped('later');
    }

    #[DataProvider('pairs'), Something([1, ']'])]
    public function testPairs(int $a, int $b): void
    {
        $this->assertSame($a, $b);
    }

    /**
     * @test
     * @dataProvider morePairs
     */
    public function documented(): void {}

    public static function pairs(): array { return []; }
    public function helper(): void {}
    protected function testProtected(): void {}
    // #[Test] in a comment does not count
    public function notATest(): void {}
}

abstract class AbstractCalcTest extends \PHPUnit\Framework\TestCase
{
    public function testInherited(): void {}
}

class NotATest
{
    public function testNothing(): void {}
}
`

// fixture is a project with PHPUnit installed (vendor/bin/phpunit is a
// stub: the fake interpreter plays it).
func fixture() map[string]string {
	return map[string]string{
		"composer.json":                  `{"autoload":{"psr-4":{"App\\":"src/"}},"autoload-dev":{"psr-4":{"Tests\\":"tests/"}}}`,
		"phpunit.xml":                    `<phpunit><testsuites><testsuite name="unit"><directory>tests</directory><exclude>tests/Fixtures</exclude></testsuite><testsuite name="other"><file>tests/CalcTest.php</file></testsuite></testsuites></phpunit>`,
		"vendor/bin/phpunit":             "#!/usr/bin/env php\n<?php\n",
		"vendor/bin/paratest":            "#!/usr/bin/env php\n<?php\n",
		"vendor/composer/installed.json": `{"packages":[{"name":"phpunit/phpunit","version":"11.5.0"}]}`,
		"tests/TestCase.php":             "<?php\nnamespace Tests;\n\nabstract class TestCase extends \\PHPUnit\\Framework\\TestCase {}\n",
		"tests/CalcTest.php":             calcTest,
		"tests/Unit/.keep":               "",
		"tests/Fixtures/x.php":           "<?php\n",
		"src/Calc.php":                   "<?php\n\ndeclare(strict_types=1);\n\nnamespace App;\n\nfinal class Calc {}\n",
		"src/Service/Mailer.php":         "<?php\nnamespace App\\Service;\n\nclass Mailer {}\n",
	}
}

func newApp(t *testing.T, mods ...module.Module) (*api.App, *Module) {
	t.Helper()
	m := New(Options{PublishInterval: 10 * time.Millisecond})
	a := modtest.NewApp(t, append([]module.Module{m}, mods...), nil)
	return a, m
}

// useFake makes the project interpreter the fake with /app mapped to
// the project root; it returns the file the fake writes its argv to.
func useFake(t *testing.T, a *api.App, root string) string {
	t.Helper()
	return useFakeAt(t, a, root)
}

// useFakeAt is useFake with /app mapped to host (e.g. only the PHP
// project root is mounted into the container).
func useFakeAt(t *testing.T, a *api.App, root string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args.json")
	fi := fakeInterp{self: self, argsFile: argsFile}
	maps := []api.MappingInfo{{Host: root, Remote: "/app"}}
	t.Cleanup(api.HookRunInterpreter.On(a.Hooks(), 0, func(_ context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
		q.Interpreter, q.Mappings = fi, maps
		return q, nil
	}))
	t.Cleanup(api.HookPathMappings.On(a.Hooks(), 0, func(_ context.Context, q api.PathMappingsQuery) (api.PathMappingsQuery, error) {
		q.Mappings = maps
		return q, nil
	}))
	return argsFile
}

type fakeCall struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
}

func readCall(t *testing.T, f string) fakeCall {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	var c fakeCall
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// waitTests waits until test run id finished and returns its results.
func waitTests(t *testing.T, a *api.App, id string) api.TestRun {
	t.Helper()
	modtest.Call(t, a, "run.wait", api.RunIDParams{RunID: id}, nil)
	var r api.TestRun
	modtest.Eventually(t, "test run finished", func() bool {
		modtest.Call(t, a, "test.results", api.TestRunIDParams{RunID: id}, &r)
		return r.State != run.StateRunning
	})
	return r
}

func nodes(r api.TestRun) map[string]*teamcity.Node {
	out := map[string]*teamcity.Node{}
	teamcity.Walk(r.Tree, func(n *teamcity.Node) { out[n.ID] = n })
	return out
}

func TestStatus(t *testing.T) {
	a, _ := newApp(t)
	modtest.WantCode(t, a, "test.status", nil, api.CodeNoProject)
	root := modtest.Project(t, a, map[string]string{"tests/ATest.php": "<?php\n"})
	var st api.TestStatus
	modtest.Call(t, a, "test.status", nil, &st)
	if st.Available || st.Install != ID || !strings.Contains(st.Hint, "vendor/bin/phpunit") || len(st.TestDirs) != 1 || st.TestDirs[0] != "tests" {
		t.Fatalf("missing: %+v", st)
	}
	// test.run explains instead of running anything.
	if err := modtest.WantCode(t, a, "test.run", api.TestRunParams{}, api.CodeUnavailable); !strings.Contains(err.Error(), "not installed") {
		t.Fatal(err)
	}
	// The recipe is offered (installation only on user action).
	recipes, err := api.HookInstallRecipes.Apply(context.Background(), a.Hooks(), nil)
	if err != nil || len(recipes) != 1 || recipes[0].Package != "phpunit/phpunit" || recipes[0].Interpreter != install.InterpreterProject {
		t.Fatalf("recipes %+v %v", recipes, err)
	}
	if err := recipes[0].Validate(); err != nil {
		t.Fatal(err)
	}

	for n, c := range fixture() {
		modtest.WriteFile(t, filepath.Join(root, n), c)
	}
	modtest.Call(t, a, "test.status", nil, &st)
	if !st.Available || st.Executable != "vendor/bin/phpunit" || st.Version != "11.5.0" || st.Config != "phpunit.xml" ||
		strings.Join(st.Suites, ",") != "unit,other" || st.Parallel != "" || st.Hint != "" {
		t.Fatalf("installed: %+v", st)
	}
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingParallel, Value: json.RawMessage(`true`), Scope: "project"}, nil)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingExecutable, Value: json.RawMessage(`"bin/nope"`), Scope: "project"}, nil)
	modtest.Call(t, a, "test.status", nil, &st)
	if st.Available || st.Parallel != "vendor/bin/paratest" || !strings.Contains(st.Hint, "bin/nope") || st.Install != "" {
		t.Fatalf("custom: %+v", st)
	}
}

func TestDiscover(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("needs cgo (Tree-sitter)")
	}
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	var d api.TestDiscovery
	modtest.Call(t, a, "test.discover", api.TestDiscoverParams{Path: "tests/CalcTest.php"}, &d)
	if len(d.Classes) != 1 || d.Files != 1 {
		t.Fatalf("classes %+v", d)
	}
	c := d.Classes[0]
	if c.ID != `Tests\CalcTest` || c.Line != 7 || c.File != filepath.Join(root, "tests", "CalcTest.php") {
		t.Fatalf("class %+v", c)
	}
	var names []string
	for _, m := range c.Children {
		names = append(names, fmt.Sprintf("%s:%d:%s", m.Method, m.Line, strings.Join(m.DataProviders, "+")))
	}
	if got := strings.Join(names, " "); got != "testAdd:9: testFail:14: itSkips:20: testPairs:26:pairs documented:35:morePairs" {
		t.Fatalf("methods %s", got)
	}
	// The whole test directory: the base class and fixtures are no tests.
	modtest.Call(t, a, "test.discover", nil, &d)
	if len(d.Classes) != 1 || d.Files != 3 {
		t.Fatalf("all %+v", d)
	}
	// Unsaved text of an open document counts.
	var st api.DocState
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "tests/CalcTest.php"}, &st)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "tests/CalcTest.php", At: api.Pos{Line: 1, Col: 1}, Text: "\n\n"}, nil)
	modtest.Call(t, a, "test.discover", api.TestDiscoverParams{Path: "tests/CalcTest.php"}, &d)
	if d.Classes[0].Line != 9 {
		t.Fatalf("open document: %+v", d.Classes[0])
	}
	modtest.WantCode(t, a, "test.discover", api.TestDiscoverParams{Path: "tests/Nope.php"}, api.CodeNotFound)
}

func TestAncestryFallback(t *testing.T) {
	anc := &ancestry{local: map[string]*index.Class{}, indexed: map[string]bool{`app\viaindex`: true}, memo: map[string]bool{}}
	anc.local[`a\b`] = &index.Class{FQN: `A\B`, Extends: []string{`A\Loop`}}
	anc.local[`a\loop`] = &index.Class{FQN: `A\Loop`, Extends: []string{`A\B`}}
	anc.local[`a\c`] = &index.Class{FQN: `A\C`, Extends: []string{`Vendor\IntegrationTestCase`}}
	if anc.isTestCase(`A\B`) || !anc.isTestCase(`\A\C`) || !anc.isTestCase(`App\ViaIndex`) || anc.isTestCase(`Other\Base`) {
		t.Fatal("ancestry")
	}
}

func TestAttributesParser(t *testing.T) {
	attrs := attributes(`/* #[Nope] */ #[A, B('x', "y]"), C(D::class, 'm')] // #[E]
	#[\F\G([1, [2]])]`)
	var got []string
	for _, a := range attrs {
		got = append(got, a.name+"="+a.args)
	}
	if strings.Join(got, "|") != `A=|B='x', "y]"|C=D::class, 'm'|\F\G=[1, [2]]` {
		t.Fatalf("%q", got)
	}
	if lastString(`D::class, 'm'`) != "m" || lastString(`"a\"b"`) != `a\"b` || lastString("x") != "" {
		t.Fatal("lastString")
	}
	if attributes("#[Open(") != nil && len(attributes("#[Open(")) != 0 {
		t.Fatal("unterminated")
	}
}

func TestFilterFor(t *testing.T) {
	cases := []struct {
		sc   api.TestScope
		want string
	}{
		{api.TestScope{}, ""},
		{api.TestScope{Filter: "raw"}, "raw"},
		{api.TestScope{Class: `\Tests\CalcTest`}, `/^(?:Tests\\CalcTest::.*)$/`},
		{api.TestScope{Class: `Tests\CalcTest`, Method: "testAdd"}, `/^(?:Tests\\CalcTest::testAdd(?: with data set .*)?)$/`},
		{api.TestScope{Class: `T`, Method: "m", DataSet: "0"}, `/^(?:T::m with data set \#0)$/`},
		{api.TestScope{Class: `T`, Method: "m", DataSet: "a/b"}, `/^(?:T::m with data set \"a\/b\")$/`},
		{api.TestScope{Tests: []string{`T::a`, `T::b with data set "x"`, `U`}}, `/^(?:T::a(?: with data set .*)?|T::b with data set \"x\"|U::.*)$/`},
	}
	for _, c := range cases {
		got, err := FilterFor(c.sc)
		if err != nil || got != c.want {
			t.Errorf("%+v: %q %v, want %q", c.sc, got, err, c.want)
		}
	}
	for _, bad := range []api.TestScope{{Method: "m"}, {Class: "C", Filter: "x"}, {Tests: []string{"::x"}}} {
		if _, err := FilterFor(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if quote("a\x01ü") != `a\x01ü` {
		t.Fatal(quote("a\x01ü"))
	}
	for in, want := range map[string]string{"": "", "#1": "#1", `"n"`: `"n"`, "12": "#12", "name": `"name"`} {
		if got := normDataSet(in); got != want {
			t.Errorf("normDataSet(%q) = %q", in, got)
		}
	}
}

// The gutter's play button runs exactly one method; its failure points
// at the local line although PHPUnit reported a container path.
func TestRunMethodAndRerunFailed(t *testing.T) {
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	argsFile := useFake(t, a, root)
	rec := modtest.Record(t, a, "test.*")

	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "tests/CalcTest.php", Class: `Tests\CalcTest`, Method: "testFail"}}, &r)
	if r.RunID == "" || r.Name != "CalcTest::testFail" || r.Framework != Framework {
		t.Fatalf("started %+v", r)
	}
	r = waitTests(t, a, r.RunID)
	call := readCall(t, argsFile)
	want := []string{"vendor/bin/phpunit", "--teamcity", "--configuration", "phpunit.xml", "--filter", `/^(?:Tests\\CalcTest::testFail(?: with data set .*)?)$/`, "tests/CalcTest.php"}
	if strings.Join(call.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("argv %q", call.Args)
	}
	if r.Counts.Total != 1 || r.Counts.Failed != 1 || r.State != run.StateExited || r.ExitCode != 1 {
		t.Fatalf("results %+v", r)
	}
	fail := nodes(r)[`Tests\CalcTest::testFail`]
	if fail == nil || fail.FailFile != filepath.Join(root, "tests", "CalcTest.php") || fail.FailLine != 17 || !fail.Comparison || fail.Expected != "'foo'" {
		t.Fatalf("failure %+v", fail)
	}
	rec.Wait(t, api.TopicTestFinished, nil)
	if len(rec.Events(api.TopicTestStarted)) == 0 {
		t.Fatal("no test.started")
	}

	// The whole class, then rerun the failed tests only.
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "tests/CalcTest.php"}}, &r)
	r = waitTests(t, a, r.RunID)
	if r.Counts != (teamcity.Counts{Total: 5, Passed: 2, Failed: 2, Ignored: 1}) {
		t.Fatalf("class %+v", r.Counts)
	}
	ns := nodes(r)
	if p := ns[`Tests\CalcTest::testPairs`]; p == nil || p.Kind != teamcity.KindMethod || p.Status != teamcity.StatusFailed || len(p.Children) != 2 {
		t.Fatalf("pairs %+v", p)
	}
	var again api.TestRun
	modtest.Call(t, a, "test.rerunFailed", api.TestRunIDParams{RunID: r.RunID}, &again)
	again = waitTests(t, a, again.RunID)
	call = readCall(t, argsFile)
	if !strings.Contains(strings.Join(call.Args, " "), `/^(?:Tests\\CalcTest::testFail(?: with data set .*)?|Tests\\CalcTest::testPairs with data set \"diff\")$/`) {
		t.Fatalf("rerun argv %q", call.Args)
	}
	if again.Counts.Total != 2 || again.Counts.Failed != 2 || again.Scope.Path != filepath.Join(root, "tests", "CalcTest.php") {
		t.Fatalf("rerun %+v", again)
	}

	// Last results feed the gutter.
	var last map[string]teamcity.Status
	modtest.Call(t, a, "test.lastStatus", api.TestLastStatusParams{IDs: []string{`\Tests\CalcTest::testAdd`, `Tests\CalcTest::testFail`, "x"}}, &last)
	if last[`\Tests\CalcTest::testAdd`] != teamcity.StatusPassed || last[`Tests\CalcTest::testFail`] != teamcity.StatusFailed || len(last) != 2 {
		t.Fatalf("last %+v", last)
	}
	modtest.Call(t, a, "test.lastStatus", nil, &last)
	if last[`Tests\CalcTest`] != teamcity.StatusFailed {
		t.Fatalf("all last %+v", last)
	}
	if index.TreeSitter {
		var d api.TestDiscovery
		modtest.Call(t, a, "test.discover", api.TestDiscoverParams{Path: "tests/CalcTest.php"}, &d)
		if d.Classes[0].Status != teamcity.StatusFailed || d.Classes[0].Children[0].Status != teamcity.StatusPassed {
			t.Fatalf("discover status %+v", d.Classes[0])
		}
	}
	var runs []api.TestRun
	modtest.Call(t, a, "test.runs", nil, &runs)
	if len(runs) != 3 || runs[2].RunID != again.RunID {
		t.Fatalf("runs %+v", runs)
	}
	// The latest run by default; a run without failures has nothing to rerun.
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Class: `Tests\CalcTest`, Method: "testAdd"}}, &r)
	waitTests(t, a, r.RunID)
	modtest.WantCode(t, a, "test.rerunFailed", api.TestRunIDParams{}, api.CodeNotFound)
	modtest.WantCode(t, a, "test.results", api.TestRunIDParams{RunID: "nope"}, api.CodeNotFound)
	modtest.WantCode(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Method: "x"}}, api.CodeInvalidParams)
}

func TestRunConfigurationAndDataSet(t *testing.T) {
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	argsFile := useFake(t, a, root)
	var types []api.RunConfigTypeInfo
	modtest.Call(t, a, "run.configTypes", nil, &types)
	found := false
	for _, ty := range types {
		if ty.ID == ID {
			found = true
			if strings.Join(ty.Suggestions[fieldSuite], ",") != "unit,other" {
				t.Fatalf("suggestions %+v", ty.Suggestions)
			}
		}
	}
	if !found {
		t.Fatal("type not registered")
	}
	var cfg run.Config
	modtest.Call(t, a, "run.configs.save", run.Config{Name: "Pairs diff", Type: ID, Values: map[string]any{
		fieldScope: scopeMethod, fieldPath: "tests/CalcTest.php", fieldClass: `Tests\CalcTest`, fieldMethod: "testPairs", fieldDataSet: "diff",
		fieldArgs: "--colors=never", fieldEnv: []string{"APP_ENV=testing"},
	}}, &cfg)
	// Started like any configuration (Ctrl+R): the plugin follows it.
	var info run.Info
	modtest.Call(t, a, "run.start", api.RunStartParams{Config: cfg.ID}, &info)
	r := waitTests(t, a, info.ID)
	if r.Counts.Total != 1 || r.Counts.Failed != 1 || r.Name != "Pairs diff" {
		t.Fatalf("config run %+v", r)
	}
	call := readCall(t, argsFile)
	if !strings.Contains(strings.Join(call.Args, " "), `--filter /^(?:Tests\\CalcTest::testPairs with data set \"diff\")$/ --colors=never tests/CalcTest.php`) {
		t.Fatalf("argv %q", call.Args)
	}
	if !contains(call.Env, "APP_ENV=testing") {
		t.Fatal("env missing")
	}
	// test.run with a configuration, and errors of incomplete ones.
	modtest.Call(t, a, "test.run", api.TestRunParams{Config: "Pairs diff"}, &r)
	waitTests(t, a, r.RunID)
	modtest.WantCode(t, a, "run.configs.save", run.Config{Name: "x", Type: ID, Values: map[string]any{fieldScope: "bogus"}}, api.CodeInvalidParams)
	modtest.Call(t, a, "run.configs.save", run.Config{Name: "No class", Type: ID, Values: map[string]any{fieldScope: scopeClass}}, nil)
	modtest.WantCode(t, a, "run.start", api.RunStartParams{Config: "No class"}, api.CodeInvalidParams)
	modtest.Call(t, a, "run.configs.save", run.Config{Name: "Script", Type: run.TypeCommand, Values: map[string]any{"command": "true"}}, nil)
	modtest.WantCode(t, a, "test.run", api.TestRunParams{Config: "Script"}, api.CodeInvalidParams)
}

func contains(l []string, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}

// Debug runs ask the debugger plugin to listen and set the trigger.
func TestDebugRun(t *testing.T) {
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	argsFile := useFake(t, a, root)
	err := modtest.WantCode(t, a, "test.run", api.TestRunParams{Debug: true}, api.CodeModuleDisabled)
	if !strings.Contains(err.Error(), "debug") {
		t.Fatal(err)
	}
	listens := 0
	t.Cleanup(api.HookDebugListener.On(a.Hooks(), 0, func(_ context.Context, q api.DebugListenerQuery) (api.DebugListenerQuery, error) {
		listens++
		q.Addr = "127.0.0.1:9100"
		return q, nil
	}))
	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{Debug: true, TestScope: api.TestScope{Class: `Tests\CalcTest`, Method: "testFail"}}, &r)
	r = waitTests(t, a, r.RunID)
	call := readCall(t, argsFile)
	if !r.Debug || listens != 1 || call.Args[0] != "-dxdebug.mode=debug" || !contains(call.Args, "-dxdebug.client_port=9100") ||
		!contains(call.Env, "XDEBUG_SESSION="+run.DebugSession) || !contains(call.Env, "XDEBUG_TRIGGER=1") {
		t.Fatalf("debug run %+v argv %q", r, call.Args)
	}
	// Rerun failed stays a debug run; the scope survives the php options.
	modtest.Call(t, a, "test.rerunFailed", api.TestRunIDParams{}, &r)
	r = waitTests(t, a, r.RunID)
	if !r.Debug || listens != 2 {
		t.Fatalf("debug rerun %+v %d", r, listens)
	}
	// A rerun from the Run window makes the debugger listen again.
	modtest.Call(t, a, "run.rerun", api.RunIDParams{RunID: r.RunID}, nil)
	if listens != 3 {
		t.Fatalf("listens %d", listens)
	}
}

func TestScopeOfArgs(t *testing.T) {
	root := t.TempDir()
	modtest.WriteFile(t, filepath.Join(root, "tests", "A.php"), "")
	sc := scopeOfArgs([]string{"-dxdebug.mode=debug", "vendor/bin/phpunit", "--teamcity", "--configuration", "x.xml", "--testsuite", "unit",
		"--filter", "f", "--group", "slow", "tests/A.php"}, root)
	if sc.Suite != "unit" || sc.Path != filepath.Join(root, "tests", "A.php") {
		t.Fatalf("%+v", sc)
	}
}

func TestMapPath(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "tests", "A.php")
	modtest.WriteFile(t, f, "")
	if mapPath(f, root, nil) != f {
		t.Fatal("host path")
	}
	if got := mapPath("/var/www/html/tests/A.php", root, nil); got != f {
		t.Fatalf("suffix %s", got)
	}
	if got := mapPath("/nowhere/B.php", root, nil); got != "/nowhere/B.php" {
		t.Fatalf("unknown %s", got)
	}
	if got := mapPath(`C:\x\tests\A.php`, root, nil); got != f {
		t.Fatalf("windows %s", got)
	}
	if got := mapPath("tests", root, nil); got != filepath.Join(root, "tests") {
		t.Fatalf("relative %s", got)
	}
}

func TestGotoAndCreateTest(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("needs cgo (Tree-sitter)")
	}
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	var g api.TestGotoResult
	modtest.Call(t, a, "test.goto", api.TestGotoParams{Path: "src/Calc.php"}, &g)
	if g.Direction != "test" || g.Class != `App\Calc` || len(g.Targets) != 1 || g.Targets[0].File != filepath.Join(root, "tests", "CalcTest.php") || !g.Targets[0].Exists {
		t.Fatalf("goto test %+v", g)
	}
	if g.Create == nil || !strings.HasSuffix(g.Create.File, filepath.Join("tests", "Unit", "CalcTest.php")) || g.Create.Class != `Tests\Unit\CalcTest` {
		t.Fatalf("proposal %+v", g.Create)
	}
	modtest.Call(t, a, "test.goto", api.TestGotoParams{Path: "tests/CalcTest.php"}, &g)
	if g.Direction != "subject" || len(g.Targets) != 1 || g.Targets[0].File != filepath.Join(root, "src", "Calc.php") {
		t.Fatalf("goto subject %+v", g)
	}
	// No test yet: the candidates are proposed, create writes the skeleton.
	modtest.Call(t, a, "test.goto", api.TestGotoParams{Path: "src/Service/Mailer.php"}, &g)
	if len(g.Targets) == 0 || g.Targets[0].Exists || g.Create.Exists {
		t.Fatalf("mailer %+v", g)
	}
	var plan api.TestCreatePlan
	modtest.Call(t, a, "test.create", api.TestCreateParams{Path: "src/Service/Mailer.php", DryRun: true}, &plan)
	want := filepath.Join(root, "tests", "Unit", "Service", "MailerTest.php")
	if plan.File != want || isFile(want) {
		t.Fatalf("dry run %+v", plan)
	}
	if !strings.Contains(plan.Content, "namespace Tests\\Unit\\Service;") || !strings.Contains(plan.Content, "use App\\Service\\Mailer;") ||
		!strings.Contains(plan.Content, "use Tests\\TestCase;") || !strings.Contains(plan.Content, "final class MailerTest extends TestCase") ||
		strings.Contains(plan.Content, "strict_types") {
		t.Fatalf("content:\n%s", plan.Content)
	}
	modtest.Call(t, a, "test.create", api.TestCreateParams{Path: "src/Service/Mailer.php"}, &plan)
	b, err := os.ReadFile(want)
	if err != nil || string(b) != plan.Content {
		t.Fatalf("written %q %v", b, err)
	}
	modtest.Call(t, a, "test.create", api.TestCreateParams{Path: "src/Service/Mailer.php"}, &plan)
	if !plan.Exists {
		t.Fatal("existing test overwritten?")
	}
	// Own file name; strict types follow the subject.
	modtest.Call(t, a, "test.create", api.TestCreateParams{Path: "src/Calc.php", File: "tests/Feature/CalcFeatureTest.php"}, &plan)
	if plan.Class != `Tests\Feature\CalcFeatureTest` || !strings.Contains(plan.Content, "declare(strict_types=1);") || !isFile(plan.File) {
		t.Fatalf("own file %+v", plan)
	}
	modtest.WantCode(t, a, "test.goto", api.TestGotoParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "test.create", api.TestCreateParams{Path: "src/Nope.php"}, api.CodeNotFound)
	modtest.WriteFile(t, filepath.Join(root, "src", "empty.php"), "<?php\n")
	modtest.WantCode(t, a, "test.goto", api.TestGotoParams{Path: "src/empty.php"}, api.CodeNotFound)
}

func TestSkeletonAndPSR4(t *testing.T) {
	s := Skeleton("ATest", `Lib\A`, testCaseFQN, false)
	if !strings.Contains(s, "use Lib\\A;\nuse PHPUnit\\Framework\\TestCase;") || strings.Contains(s, "namespace") {
		t.Fatal(s)
	}
	root := t.TempDir()
	modtest.WriteFile(t, filepath.Join(root, "composer.json"), `{"autoload":{"psr-4":{"App\\":["src/","lib/"],"":"fallback/"}},"autoload-dev":{"psr-4":{"App\\Tests\\":"tests"}}}`)
	es := readPSR4(root)
	if len(es) != 3 || es[0].Prefix != `App\Tests\` || len(es[1].Dirs) != 2 || es[2].Prefix != "" {
		t.Fatalf("%+v", es)
	}
	res := testsOf(root, `App\X`, es)
	if len(res.Targets) == 0 || res.Targets[0].Class != `App\Tests\XTest` {
		t.Fatalf("%+v", res)
	}
	// Without composer.json: the convention Tests\ → tests/.
	if d := devEntries(root, nil); d[0].Prefix != `Tests\` {
		t.Fatal(d)
	}
	if readPSR4(t.TempDir()) != nil {
		t.Fatal("no composer.json")
	}
}

func TestStartedEventOrder(t *testing.T) {
	a, m := newApp(t)
	root := modtest.Project(t, a, fixture())
	useFake(t, a, root)
	got := make(chan string, 10)
	unsub := a.Bus().Subscribe("test.*", func(e bus.Event) { got <- e.Topic })
	defer unsub()
	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{}, &r)
	waitTests(t, a, r.RunID)
	if first := <-got; first != api.TopicTestStarted {
		t.Fatalf("first event %s", first)
	}
	// A new project forgets the results.
	modtest.Project(t, a, fixture())
	modtest.Eventually(t, "reset", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.sessions) == 0
	})
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
}

// A test printing megabytes: the events stay small, the results keep the
// tail of the output within the limits (D380).
func TestLargeOutputBounded(t *testing.T) {
	t.Setenv(fakeSpamEnv, "4000") // ~380 KB, beyond the limit per test
	a, _ := newApp(t)
	root := modtest.Project(t, a, fixture())
	useFake(t, a, root)
	rec := modtest.Record(t, a, "test.*")
	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "tests/CalcTest.php"}}, &r)
	r = waitTests(t, a, r.RunID)
	rec.Wait(t, api.TopicTestFinished, nil)
	if r.Light || r.Project != root || r.Counts.Total != 5 {
		t.Fatalf("results %+v", r.Counts)
	}
	for _, topic := range []string{api.TopicTestUpdated, api.TopicTestFinished} {
		for _, e := range rec.Events(topic) {
			b, err := json.Marshal(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if len(b) > 16<<10 {
				t.Fatalf("%s payload has %d bytes", topic, len(b))
			}
			var ev api.TestRun
			if json.Unmarshal(b, &ev) != nil || !ev.Light {
				t.Fatalf("%s not light", topic)
			}
		}
	}
	add := nodes(r)[`Tests\CalcTest::testAdd`]
	if add == nil || add.OutputTruncated == 0 || len(add.Output) > teamcity.DefaultMaxOutput+100 ||
		!strings.HasSuffix(add.Output, "spam 0003999 "+strings.Repeat("x", 80)) {
		t.Fatalf("testAdd output: %d bytes, %d truncated", len(add.Output), add.OutputTruncated)
	}
	// Failure details are in test.results and test.node, not in the events.
	if fail := nodes(r)[`Tests\CalcTest::testFail`]; fail.Expected != "'foo'" || fail.Details == "" {
		t.Fatalf("fail %+v", fail)
	}
	var n teamcity.Node
	modtest.Call(t, a, "test.node", api.TestNodeParams{ID: `Tests\CalcTest::testAdd`}, &n)
	if n.Output != add.Output || n.Status != teamcity.StatusPassed || len(n.Children) != 0 {
		t.Fatalf("node %d bytes %s", len(n.Output), n.Status)
	}
	modtest.Call(t, a, "test.node", api.TestNodeParams{RunID: r.RunID, ID: `Tests\CalcTest::testFail`}, &n)
	if n.Actual != "'bar'" || n.FailLine != 17 {
		t.Fatalf("node %+v", n)
	}
	modtest.WantCode(t, a, "test.node", api.TestNodeParams{ID: "nope"}, api.CodeNotFound)
	modtest.WantCode(t, a, "test.node", api.TestNodeParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "test.node", api.TestNodeParams{RunID: "x", ID: "y"}, api.CodeNotFound)
}

// A run of the previous project that ends after a project switch does
// not show up in the new project (D381).
func TestRunOfPreviousProjectIgnored(t *testing.T) {
	a, m := newApp(t)
	old := modtest.Project(t, a, fixture())
	rec := modtest.Record(t, a, "test.*")
	info := run.Info{ID: "run-old", Name: "old", Type: ID, Project: old, State: run.StateRunning, Started: time.Now(),
		Spec: run.Spec{Interpreter: run.InterpreterHost}}
	m.onRun(bus.Event{Topic: api.TopicRunStarted, Payload: info})
	if len(rec.Events(api.TopicTestStarted)) == 0 {
		rec.Wait(t, api.TopicTestStarted, nil)
	}
	cur := modtest.Project(t, a, fixture())
	modtest.Eventually(t, "reset", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.sessions) == 0
	})
	info.State, info.ExitCode = run.StateExited, 1
	m.onRun(bus.Event{Topic: api.TopicRunOutput, Payload: run.Output{RunID: info.ID}})
	m.onRun(bus.Event{Topic: api.TopicRunExited, Payload: info})
	var runs []api.TestRun
	modtest.Call(t, a, "test.runs", nil, &runs)
	if len(runs) != 0 {
		t.Fatalf("run of the old project kept: %+v", runs)
	}
	// A run of the open project is followed.
	info2 := run.Info{ID: "run-new", Name: "new", Type: ID, Project: cur, State: run.StateExited, Started: time.Now(),
		Spec: run.Spec{Interpreter: run.InterpreterHost}}
	m.onRun(bus.Event{Topic: api.TopicRunExited, Payload: info2})
	modtest.Call(t, a, "test.runs", nil, &runs)
	if len(runs) != 1 || runs[0].RunID != "run-new" || runs[0].Project != cur {
		t.Fatalf("runs %+v", runs)
	}
	rec.Wait(t, api.TopicTestFinished, func(e bus.Event) bool { return e.Payload.(api.TestRun).RunID == "run-new" })
	for _, e := range rec.Events(api.TopicTestFinished) {
		if e.Payload.(api.TestRun).RunID == "run-old" {
			t.Fatal("test.finished for the old project's run")
		}
	}
	if !sameProject("", cur) || sameProject(old, "") || !sameProject(cur+string(filepath.Separator), cur) {
		t.Fatal("sameProject")
	}
}
