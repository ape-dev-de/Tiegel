package modules

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// Real PHPUnit runs (AP15.2 acceptance). They need php and an installed
// PHPUnit and are skipped otherwise; nothing is downloaded:
//
//	TIEGEL_TEST_PHPUNIT=/path/to/vendor/bin/phpunit   (its vendor/autoload.php is used)
//	TIEGEL_TEST_PHP_IMAGE=php:8.3-cli                 (Compose variant; local image, never pulled)

const realCalcTest = `<?php
namespace Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

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

    #[DataProvider('pairs')]
    public function testPairs(int $a, int $b): void
    {
        $this->assertSame($a, $b);
    }

    public static function pairs(): array
    {
        return ['same' => [1, 1], 'diff' => [1, 2]];
    }
}
`

// realPHPUnit returns php and the phpunit script, or skips.
func realPHPUnit(t *testing.T) (php, phpunit string) {
	t.Helper()
	phpunit = os.Getenv("TIEGEL_TEST_PHPUNIT")
	if phpunit == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if p := filepath.Join(home, ".composer", "vendor", "bin", "phpunit"); fileExists(p) {
				phpunit = p
			}
		}
	}
	if phpunit == "" || !fileExists(phpunit) {
		t.Skip("no PHPUnit installed (set TIEGEL_TEST_PHPUNIT=/path/to/vendor/bin/phpunit)")
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	return php, phpunit
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// vendorOf returns the vendor directory of a vendor/bin/phpunit script.
func vendorOf(phpunit string) string {
	return filepath.Dir(filepath.Dir(phpunit))
}

func waitTestRun(t *testing.T, a *api.App, id string) api.TestRun {
	t.Helper()
	modtest.Call(t, a, "run.wait", api.RunIDParams{RunID: id}, nil)
	var r api.TestRun
	modtest.Eventually(t, "test run finished", func() bool {
		modtest.Call(t, a, "test.results", api.TestRunIDParams{RunID: id}, &r)
		return r.State != run.StateRunning
	})
	return r
}

func findNode(r api.TestRun, id string) *teamcity.Node {
	var out *teamcity.Node
	teamcity.Walk(r.Tree, func(n *teamcity.Node) {
		if n.ID == id {
			out = n
		}
	})
	return out
}

func TestRealPHPUnitHost(t *testing.T) {
	php, phpunit := realPHPUnit(t)
	opts := modtest.Options(t)
	opts.Interp.Dirs = []string{filepath.Dir(php)}
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	autoload := filepath.Join(vendorOf(phpunit), "autoload.php")
	root := modtest.Project(t, a, map[string]string{
		"phpunit.xml":        `<phpunit bootstrap="` + autoload + `"><testsuites><testsuite name="unit"><directory>tests</directory></testsuite></testsuites></phpunit>`,
		"tests/CalcTest.php": realCalcTest,
	})
	raw, _ := json.Marshal(phpunit)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: "phpunit.executable", Scope: "project", Value: raw}, nil)

	// Gutter play on testFail: exactly that method.
	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "tests/CalcTest.php", Class: `Tests\CalcTest`, Method: "testFail"}}, &r)
	r = waitTestRun(t, a, r.RunID)
	if r.Counts.Total != 1 || r.Counts.Failed != 1 {
		t.Fatalf("method run %+v", r)
	}
	n := findNode(r, `Tests\CalcTest::testFail`)
	if n == nil || n.FailFile != filepath.Join(root, "tests", "CalcTest.php") || n.FailLine != 16 || !n.Comparison || n.Expected != "'foo'" || n.Actual != "'bar'" {
		t.Fatalf("failure %+v", n)
	}
	// One data set.
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "tests/CalcTest.php", Class: `Tests\CalcTest`, Method: "testPairs", DataSet: "same"}}, &r)
	if r = waitTestRun(t, a, r.RunID); r.Counts.Total != 1 || r.Counts.Passed != 1 {
		t.Fatalf("data set run %+v", r)
	}
	// The suite, then the failed tests again.
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Suite: "unit"}}, &r)
	r = waitTestRun(t, a, r.RunID)
	if r.Counts.Total != 4 || r.Counts.Failed != 2 || r.Counts.Passed != 2 {
		t.Fatalf("suite run %+v", r.Counts)
	}
	modtest.Call(t, a, "test.rerunFailed", api.TestRunIDParams{}, &r)
	if r = waitTestRun(t, a, r.RunID); r.Counts.Total != 2 || r.Counts.Failed != 2 {
		t.Fatalf("rerun failed %+v", r.Counts)
	}
}

// The Compose variant: PHPUnit runs in a service, paths come back mapped.
func TestRealPHPUnitCompose(t *testing.T) {
	_, phpunit := realPHPUnit(t)
	image := os.Getenv("TIEGEL_TEST_PHP_IMAGE")
	if image == "" {
		t.Skip("set TIEGEL_TEST_PHP_IMAGE to a local PHP image for the Compose variant")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, docker, "image", "inspect", image).Run(); err != nil {
		t.Skipf("image %s is not present locally (never pulled): %v", image, err)
	}
	opts := modtest.Options(t)
	opts.ComposeEnv = nil
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	vendor := vendorOf(phpunit)
	root := modtest.Project(t, a, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: " + image + "\n    pull_policy: never\n    working_dir: /srv/app\n    entrypoint: []\n" +
			"    volumes:\n      - ./:/srv/app\n      - " + vendor + ":/srv/app/vendor:ro\n",
		"phpunit.xml":        `<phpunit bootstrap="vendor/autoload.php"><testsuites><testsuite name="unit"><directory>tests</directory></testsuite></testsuites></phpunit>`,
		"tests/CalcTest.php": realCalcTest,
		// Host side stub: the container sees the real vendor mount.
		"vendor/bin/phpunit": "<?php\n",
	})
	var list []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	modtest.Call(t, a, "interp.list", nil, &list)
	id := ""
	for _, i := range list {
		if i.Kind == "compose" {
			id = i.ID
		}
	}
	if id == "" {
		t.Fatalf("no compose interpreter: %+v", list)
	}
	modtest.Call(t, a, "interp.select", map[string]string{"id": id}, nil)
	var r api.TestRun
	modtest.Call(t, a, "test.run", api.TestRunParams{TestScope: api.TestScope{Path: "tests/CalcTest.php", Class: `Tests\CalcTest`, Method: "testFail"}}, &r)
	r = waitTestRun(t, a, r.RunID)
	n := findNode(r, `Tests\CalcTest::testFail`)
	if r.Counts.Total != 1 || n == nil || n.FailFile != filepath.Join(root, "tests", "CalcTest.php") || n.FailLine != 16 ||
		!strings.HasPrefix(n.Location, "php_qn:///srv/app/") {
		t.Fatalf("compose run %+v node %+v", r, n)
	}
}
