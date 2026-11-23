package phpunit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

const junitDoc = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="unit" tests="4" assertions="9">
    <testsuite name="Tests\CalcTest" file="/p/tests/CalcTest.php" tests="4">
      <testcase name="testAdd" file="/p/tests/CalcTest.php" line="8" class="Tests\CalcTest" classname="Tests.CalcTest" assertions="3" time="0.01"/>
      <testsuite name="Tests\CalcTest::testPairs">
        <testcase name="testPairs with data set #0" class="Tests\CalcTest" line="12" assertions="2"/>
        <testcase name="testPairs with data set #1 (2, 2)" classname="Tests.CalcTest" line="12" assertions="2"/>
      </testsuite>
      <testcase name="testGone" class="Tests\CalcTest" assertions="1"/>
    </testsuite>
  </testsuite>
</testsuites>`

func junitTree() *teamcity.Tree {
	tr := teamcity.NewTree()
	hint := "php_qn:///p/tests/CalcTest.php::\\Tests\\CalcTest"
	for _, l := range []string{
		"##teamcity[testSuiteStarted name='Tests\\CalcTest' locationHint='" + hint + "']",
		"##teamcity[testStarted name='testAdd' locationHint='" + hint + "::testAdd']",
		"##teamcity[testFinished name='testAdd']",
		"##teamcity[testSuiteStarted name='testPairs' locationHint='" + hint + "::testPairs']",
		"##teamcity[testStarted name='testPairs with data set #0' locationHint='" + hint + "::testPairs with data set #0']",
		"##teamcity[testFinished name='testPairs with data set #0']",
		"##teamcity[testStarted name='testPairs with data set #1' locationHint='" + hint + "::testPairs with data set #1']",
		"##teamcity[testFinished name='testPairs with data set #1']",
		"##teamcity[testSuiteFinished name='testPairs']",
		"##teamcity[testSuiteFinished name='Tests\\CalcTest']",
	} {
		tr.Feed(l)
	}
	return tr
}

func TestJUnitCollector(t *testing.T) {
	tr := junitTree()
	var j junitCollector
	if j.feed("PHPUnit 11.5", tr) {
		t.Fatal("plain line taken")
	}
	for _, l := range strings.Split(junitDoc, "\n") {
		if !j.feed(l, tr) {
			t.Fatalf("XML line not taken: %q", l)
		}
	}
	if !j.done || j.feed("<?xml again", tr) {
		t.Fatal("collector state")
	}
	add, _ := tr.SnapshotNode(`Tests\CalcTest::testAdd`)
	pairs, _ := tr.SnapshotNode(`Tests\CalcTest::testPairs`)
	ds1, _ := tr.SnapshotNode(`Tests\CalcTest::testPairs with data set #1`)
	cls, _ := tr.SnapshotNode(`Tests\CalcTest`)
	if add.Assertions != 3 || add.Line != 8 || pairs.Assertions != 4 || pairs.Line != 12 || ds1.Assertions != 2 || cls.Assertions != 7 {
		t.Fatalf("add %+v pairs %+v ds1 %+v cls %d", add, pairs, ds1, cls.Assertions)
	}
	if c := tr.Counts(); c.Assertions != 7 {
		t.Fatalf("counts %+v", c)
	}
	if applyJUnit("<not xml", tr) != 0 || applyJUnit("<testsuites/>", tr) != 0 {
		t.Fatal("bad documents applied")
	}
	var one junitCollector
	if !one.feed("<testsuites/>", tr) || !one.done {
		t.Fatal("empty document on one line")
	}
}

func TestUseJUnit(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "phpunit.xml")
	logging := filepath.Join(dir, "logging.xml")
	_ = os.WriteFile(plain, []byte("<phpunit/>"), 0o644)
	_ = os.WriteFile(logging, []byte(`<phpunit><logging><junit outputFile="j.xml"/></logging></phpunit>`), 0o644)
	for _, c := range []struct {
		parallel bool
		config   string
		args     []string
		want     bool
	}{
		{false, plain, nil, true},
		{false, "", []string{"--colors=never"}, true},
		{true, plain, nil, false},
		{false, logging, nil, false},
		{false, plain, []string{"--log-junit", "x.xml"}, false},
		{false, plain, []string{"--log-junit=x.xml"}, false},
		{false, filepath.Join(dir, "missing.xml"), nil, true},
	} {
		if got := useJUnit(c.parallel, c.config, c.args); got != c.want {
			t.Errorf("useJUnit(%v, %s, %v) = %v", c.parallel, filepath.Base(c.config), c.args, got)
		}
	}
}
