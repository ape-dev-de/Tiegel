package phpunit

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// nestedFixture is fixture() with the PHP project in project/ (D515):
// the repository root holds no composer.json.
func nestedFixture() map[string]string {
	out := map[string]string{"README.md": "# repo\n", "docker/Dockerfile": "FROM php\n"}
	for n, c := range fixture() {
		out["project/"+n] = c
	}
	return out
}

func TestNestedProject(t *testing.T) {
	a, _ := newApp(t)
	root := modtest.Project(t, a, nestedFixture())
	base := filepath.Join(root, "project")
	if a.PHPRoot() != base {
		t.Fatalf("php root %s", a.PHPRoot())
	}
	var st api.TestStatus
	modtest.Call(t, a, "test.status", nil, &st)
	if !st.Available || st.Base != "project" || st.Executable != "project/vendor/bin/phpunit" || st.Config != "project/phpunit.xml" ||
		st.Version != "11.5.0" || len(st.TestDirs) != 1 || st.TestDirs[0] != "project/tests" {
		t.Fatalf("status %+v", st)
	}

	// Only the PHP root is mounted into the "container" (/app).
	argsFile := useFakeAt(t, a, base)
	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "project/tests/CalcTest.php", Class: `Tests\CalcTest`, Method: "testFail"}}, &r)
	r = waitTests(t, a, r.RunID)
	call := readCall(t, argsFile)
	want := []string{"vendor/bin/phpunit", "--teamcity", "--configuration", "phpunit.xml", "--filter", `/^(?:Tests\\CalcTest::testFail(?: with data set .*)?)$/`, "tests/CalcTest.php"}
	if strings.Join(call.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("argv %q", call.Args)
	}
	if d, _ := filepath.EvalSymlinks(call.Dir); d != base {
		t.Fatalf("working directory %s", call.Dir)
	}
	fail := nodes(r)[`Tests\CalcTest::testFail`]
	if r.Counts.Failed != 1 || fail == nil || fail.FailFile != filepath.Join(base, "tests", "CalcTest.php") || fail.FailLine != 17 {
		t.Fatalf("results %+v %+v", r.Counts, fail)
	}
	if r.Scope.Path != filepath.Join(base, "tests", "CalcTest.php") {
		t.Fatalf("scope %+v", r.Scope)
	}
	// Rerun Failed reads the path back relative to the PHP root.
	modtest.Call(t, a, "test.rerunFailed", api.TestRunIDParams{RunID: r.RunID}, &r)
	r = waitTests(t, a, r.RunID)
	if r.Counts != (teamcity.Counts{Total: 1, Failed: 1}) {
		t.Fatalf("rerun %+v", r.Counts)
	}

	// The setting names the PHP root; a project relative configuration
	// setting still works.
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingConfig, Value: json.RawMessage(`"project/phpunit.xml"`), Scope: "project"}, nil)
	modtest.Call(t, a, "test.status", nil, &st)
	if st.Config != "project/phpunit.xml" {
		t.Fatalf("config setting %+v", st)
	}
	// Relative to the PHP root works as well.
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingConfig, Value: json.RawMessage(`"phpunit.xml"`), Scope: "project"}, nil)
	modtest.Call(t, a, "test.status", nil, &st)
	if st.Config != "project/phpunit.xml" {
		t.Fatalf("config relative to the PHP root %+v", st)
	}

	if !index.TreeSitter {
		return
	}
	var d api.TestDiscovery
	modtest.Call(t, a, "test.discover", api.TestDiscoverParams{}, &d)
	if len(d.Classes) != 1 || d.Classes[0].File != filepath.Join(base, "tests", "CalcTest.php") {
		t.Fatalf("discovery %+v", d)
	}
	// Go to Test uses the PSR-4 map of project/composer.json.
	var g api.TestGotoResult
	modtest.Call(t, a, "test.goto", api.TestGotoParams{Path: "project/src/Calc.php"}, &g)
	if len(g.Targets) != 1 || g.Targets[0].File != filepath.Join(base, "tests", "CalcTest.php") {
		t.Fatalf("goto %+v", g)
	}
	var plan api.TestCreatePlan
	modtest.Call(t, a, "test.create", api.TestCreateParams{Path: "project/src/Service/Mailer.php", DryRun: true}, &plan)
	if plan.File != filepath.Join(base, "tests", "Unit", "Service", "MailerTest.php") || !strings.Contains(plan.Content, "use Tests\\TestCase;") {
		t.Fatalf("create %+v", plan)
	}
}
