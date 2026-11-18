package headless

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
)

const testCmdCalc = `<?php
namespace Tests;

use PHPUnit\Framework\TestCase;

final class CalcTest extends TestCase
{
    public function testAdd(): void {}

    /** @test */
    public function failing(): void {}
}
`

// fakePHPUnitScript is a vendor/bin/phpunit that prints TeamCity
// messages like PHPUnit (run with the host's php; no PHPUnit needed).
const fakePHPUnitScript = `<?php
$file = getcwd() . '/tests/CalcTest.php';
$hint = 'php_qn://' . $file . '::\Tests\CalcTest';
echo "##teamcity[testSuiteStarted name='Tests\\CalcTest' locationHint='$hint']\n";
echo "##teamcity[testStarted name='testAdd' locationHint='$hint::testAdd']\n";
echo "##teamcity[testFinished name='testAdd' duration='1']\n";
if (!in_array('--filter', $argv, true)) {
    echo "##teamcity[testStarted name='failing' locationHint='$hint::failing']\n";
    echo "##teamcity[testFailed name='failing' message='boom' details='$file:11|n']\n";
    echo "##teamcity[testFinished name='failing' duration='2']\n";
}
echo "##teamcity[testSuiteFinished name='Tests\\CalcTest']\n";
exit(in_array('--filter', $argv, true) ? 0 : 1);
`

func testFixture() map[string]string {
	return map[string]string{
		"composer.json":      `{"autoload":{"psr-4":{"App\\":"src/"}},"autoload-dev":{"psr-4":{"Tests\\":"tests/"}}}`,
		"phpunit.xml":        `<phpunit><testsuites><testsuite name="unit"><directory>tests</directory></testsuite></testsuites></phpunit>`,
		"tests/CalcTest.php": testCmdCalc,
		"src/Calc.php":       "<?php\nnamespace App;\n\nclass Calc {}\n",
	}
}

func TestTestCommandWithoutRunner(t *testing.T) {
	dir := project(t, testFixture())
	code, out, _ := run(t, "", "test", "--project", dir)
	if code != 0 || !strings.Contains(out, "PHPUnit: missing") || !strings.Contains(out, "install phpunit --dry-run") || !strings.Contains(out, "suites:   unit") {
		t.Fatalf("status: %d %q", code, out)
	}
	code, _, errOut := run(t, "", "test", "run", "--project", dir)
	if code != ExitError || !strings.Contains(errOut, "not installed") {
		t.Fatalf("run without phpunit: %d %q", code, errOut)
	}
	if code, _, _ := run(t, "", "test", "bogus", "--project", dir); code != ExitUsage {
		t.Fatalf("usage: %d", code)
	}
	if code, _, _ := run(t, "", "test", "goto", "--project", dir); code != ExitUsage {
		t.Fatalf("goto usage: %d", code)
	}
	if code, _, _ := run(t, "", "test", "results", "--project", dir); code != ExitError {
		t.Fatalf("no results: %d", code)
	}
	if code, _, _ := run(t, "", "test", "node", "--project", dir); code != ExitUsage {
		t.Fatalf("node usage: %d", code)
	}
	if code, _, errOut := run(t, "", "test", "node", `Tests\CalcTest::testAdd`, "--project", dir); code != ExitError || !strings.Contains(errOut, "no test run") {
		t.Fatalf("node without run: %d %q", code, errOut)
	}
	code, out, _ = run(t, "", "test", "config", "tests/CalcTest.php", "--class", `Tests\CalcTest`, "--method", "testAdd", "--project", dir)
	if code != 0 || !strings.Contains(out, `"method": "testAdd"`) || !strings.Contains(out, `"name": "CalcTest::testAdd"`) {
		t.Fatalf("config: %d %q", code, out)
	}
	if !index.TreeSitter {
		return
	}
	code, out, _ = run(t, "", "test", "discover", "--project", dir)
	if code != 0 || !strings.Contains(out, `Tests\CalcTest  tests/CalcTest.php:6`) || !strings.Contains(out, "  failing  :11") || !strings.Contains(out, "1 classes, 2 tests in 1 files") {
		t.Fatalf("discover: %d %q", code, out)
	}
	code, out, _ = run(t, "", "test", "goto", "src/Calc.php", "--project", dir)
	if code != 0 || !strings.Contains(out, "exists  Tests\\CalcTest  tests/CalcTest.php") {
		t.Fatalf("goto: %d %q", code, out)
	}
	code, out, _ = run(t, "", "test", "create", "src/Calc.php", "--file", "tests/Unit/CalcTest.php", "--dry-run", "--project", dir)
	if code != 0 || !strings.Contains(out, "would create tests/Unit/CalcTest.php") || !strings.Contains(out, "namespace Tests\\Unit;") {
		t.Fatalf("create dry: %d %q", code, out)
	}
	code, out, _ = run(t, "", "--json", "test", "create", "src/Calc.php", "--file", "tests/Unit/CalcTest.php", "--project", dir)
	var plan api.TestCreatePlan
	if code != 0 || json.Unmarshal([]byte(out), &plan) != nil || plan.Class != `Tests\Unit\CalcTest` {
		t.Fatalf("create: %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tests", "Unit", "CalcTest.php")); err != nil {
		t.Fatal(err)
	}
}

// A run with the host's php and a stand-in script: results stream, the
// exit code tells about failures. Skipped without php.
func TestTestCommandRun(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not found")
	}
	files := testFixture()
	files["vendor/bin/phpunit"] = fakePHPUnitScript
	dir := project(t, files)
	code, out, errOut := run(t, "", "test", "run", "--interpreter", "host", "--project", dir)
	if code != ExitError || !strings.Contains(out, `PASSED     Tests\CalcTest::testAdd (1 ms)`) || !strings.Contains(out, `FAILED     Tests\CalcTest::failing`) ||
		!strings.Contains(out, "2 tests, 1 passed, 1 failed") {
		t.Fatalf("run: %d %q %q", code, out, errOut)
	}
	code, out, errOut = run(t, "", "test", "run", "tests/CalcTest.php", "--class", `Tests\CalcTest`, "--method", "testAdd", "--interpreter", "host", "--project", dir)
	if code != 0 || !strings.Contains(out, "CalcTest::testAdd: 1 tests, 1 passed") {
		t.Fatalf("method: %d %q %q", code, out, errOut)
	}
	code, out, _ = run(t, "", "--json", "test", "run", "--interpreter", "host", "--project", dir)
	var r api.TestRun
	if code != ExitError || json.Unmarshal([]byte(out), &r) != nil || r.Counts.Failed != 1 || len(r.Tree) != 1 {
		t.Fatalf("json: %d %q", code, out)
	}
}
