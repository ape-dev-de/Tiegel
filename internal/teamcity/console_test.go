package teamcity

import (
	"strings"
	"testing"
)

// The console keeps everything but service messages; a suite's snapshot
// joins the failures and output of its tests.
func TestConsoleAndSuiteOutput(t *testing.T) {
	tr := NewTree()
	for _, l := range []string{
		"PHPUnit 11.5 by Sebastian Bergmann",
		"##teamcity[testSuiteStarted name='Tests\\CalcTest' flowId='1']",
		"##teamcity[testStarted name='testA' flowId='1']",
		"hello",
		"##teamcity[testFinished name='testA' duration='3' flowId='1']",
		"##teamcity[testStarted name='testB' flowId='1']",
		"##teamcity[testFailed name='testB' message='boom' details='at x.php:3' flowId='1']",
		"##teamcity[testFinished name='testB' flowId='1']",
		"##teamcity[testStarted name='testC' flowId='1']",
		"##teamcity[testFinished name='testC' flowId='1']",
		"##teamcity[testSuiteFinished name='Tests\\CalcTest' flowId='1']",
		"There was 1 failure:",
	} {
		tr.Feed(l)
	}
	c := tr.Console()
	if strings.Contains(c, "##teamcity") || !strings.Contains(c, "PHPUnit 11.5") || !strings.Contains(c, "hello") || !strings.HasSuffix(c, "There was 1 failure:") {
		t.Fatalf("console %q", c)
	}
	n, ok := tr.SnapshotNode(`Tests\CalcTest`)
	if !ok || n.Status != StatusFailed || !strings.Contains(n.Output, "testA (passed)\nhello") ||
		!strings.Contains(n.Output, "testB (failed)\nboom\nat x.php:3") || strings.Contains(n.Output, "testC") {
		t.Fatalf("suite %+v", n)
	}
	if Aggregate(nil) != StatusPassed {
		t.Fatal("aggregate")
	}

	small := NewTree()
	small.MaxRunOutput = 10
	small.Feed(strings.Repeat("x", 30))
	small.Feed("tail")
	if c := small.Console(); !strings.Contains(c, "truncated") || !strings.HasSuffix(c, "tail") {
		t.Fatalf("truncated %q", c)
	}
}
