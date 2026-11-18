package teamcity

import (
	"os"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	m, ok := Parse(`##teamcity[testStarted name='testAdd' locationHint='php_qn:///app/tests/CalcTest.php::\Tests\CalcTest::testAdd' flowId='42']`)
	if !ok || m.Name != "testStarted" || m.Attr("name") != "testAdd" || m.Attr("flowId") != "42" {
		t.Fatalf("%+v %v", m, ok)
	}
	if m.Attr("locationHint") != `php_qn:///app/tests/CalcTest.php::\Tests\CalcTest::testAdd` {
		t.Fatal(m.Attr("locationHint"))
	}
	for _, bad := range []string{
		"", "hello", "##teamcity[", "##teamcity[]", "##teamcity[x a=]", "##teamcity[x a='b]", "##teamcity[x a='b' c]",
		"##teamcity[x a='b'] tail", "##teamcity[x a='b|']", "##teamcity[x =b]", "##teamcity[x a='b' 'c",
	} {
		if _, ok := Parse(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
	if m, ok := Parse("  ##teamcity[message 'hello |'world|'']  "); !ok || m.Attr("") != "hello 'world'" {
		t.Fatalf("single value: %+v %v", m, ok)
	}
	if m, ok := Parse("##teamcity[flowStarted]"); !ok || m.Name != "flowStarted" || len(m.Attrs) != 0 {
		t.Fatalf("no attrs: %+v", m)
	}
}

func TestUnescape(t *testing.T) {
	cases := map[string]string{
		`a|nb`:       "a\nb",
		`a|rb`:       "a\rb",
		`|'q|'`:      "'q'",
		`|||[|]`:     "|[]",
		`|x|l|p`:     "\u0085\u2028\u2029",
		`|0x00e4|0x`: "ä0x",
		`|z`:         "z",
		`|0x12`:      "0x12",
	}
	for in, want := range cases {
		m, ok := Parse("##teamcity[m v='" + in + "']")
		if !ok {
			t.Errorf("%q: not parsed", in)
			continue
		}
		if got := m.Attr("v"); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	// Escape round trip.
	for _, s := range []string{"plain", "it's [x] | y\nz\r", "\u0085\u2028\u2029", "ä ö"} {
		m, ok := Parse(Format("m", "v", s))
		if !ok || m.Attr("v") != s {
			t.Errorf("round trip %q: %+v", s, m)
		}
	}
}

func TestFind(t *testing.T) {
	msgs, before := Find("hello output##teamcity[testFinished name='t' duration='0']")
	if before != "hello output" || len(msgs) != 1 || msgs[0].Name != "testFinished" {
		t.Fatalf("%q %+v", before, msgs)
	}
	msgs, before = Find("##teamcity[a x='1']##teamcity[b y='2'] junk ##teamcity[broken")
	if before != "" || len(msgs) != 2 || msgs[1].Attr("y") != "2" {
		t.Fatalf("%q %+v", before, msgs)
	}
	if msgs, before := Find("no message"); msgs != nil || before != "no message" {
		t.Fatal(msgs, before)
	}
	if msgs, _ := Find("##teamcity[##teamcity[ok]"); len(msgs) != 1 || msgs[0].Name != "ok" {
		t.Fatalf("recovery: %+v", msgs)
	}
}

func TestSplitLocation(t *testing.T) {
	p, n := SplitLocation(`php_qn:///app/tests/A.php::\Ns\A::testX with data set "a::b"`)
	if p != "/app/tests/A.php" || n != `Ns\A::testX with data set "a::b"` {
		t.Fatal(p, n)
	}
	c, m, d := SplitName(n)
	if c != `Ns\A` || m != "testX" || d != `"a::b"` {
		t.Fatal(c, m, d)
	}
	if p, n := SplitLocation(`php_qn://C:\proj\tests\A.php::\A`); p != `C:\proj\tests\A.php` || n != "A" {
		t.Fatal(p, n)
	}
	if p, n := SplitLocation("nothing"); p != "" || n != "" {
		t.Fatal(p, n)
	}
	if p, n := SplitLocation("php_qn:///app/x.php"); p != "/app/x.php" || n != "" {
		t.Fatal(p, n)
	}
}

func TestFailureLocation(t *testing.T) {
	d := "/app/src/Calc.php:10\n/app/tests/CalcTest.php:17\n/app/vendor/x.php:3"
	if f, l := FailureLocation(d, "/app/tests/CalcTest.php"); f != "/app/tests/CalcTest.php" || l != 17 {
		t.Fatal(f, l)
	}
	if f, l := FailureLocation(d, "/other.php"); f != "/app/src/Calc.php" || l != 10 {
		t.Fatal(f, l)
	}
	if f, l := FailureLocation("no frames here", ""); f != "" || l != 0 {
		t.Fatal(f, l)
	}
	if f, l := FailureLocation(`C:\p\t\ATest.php:5`, ""); f != `C:\p\t\ATest.php` || l != 5 {
		t.Fatal(f, l)
	}
}

// feedFile feeds a captured run into a new tree.
func feedFile(t *testing.T, name string) *Tree {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTree()
	tr.MapPath = func(p string) string { return strings.Replace(p, "/app/", "/host/", 1) }
	for _, l := range strings.Split(string(b), "\n") {
		tr.Feed(l)
	}
	tr.Finish()
	return tr
}

func TestRealOutput(t *testing.T) {
	for _, f := range []string{"phpunit11.txt", "phpunit13.txt"} {
		t.Run(f, func(t *testing.T) {
			tr := feedFile(t, f)
			c := tr.Counts()
			want := Counts{Total: 9, Passed: 3, Failed: 4, Ignored: 2, Expected: 9}
			if c != want {
				t.Fatalf("counts %+v, want %+v", c, want)
			}
			snap := tr.Snapshot()
			if len(snap) != 1 {
				t.Fatalf("roots %d", len(snap))
			}
			cls := snap[0]
			if cls.Kind != KindClass || cls.Class != `Tests\CalcTest` || cls.File != "/host/tests/CalcTest.php" || cls.Status != StatusFailed {
				t.Fatalf("class %+v", cls)
			}
			byID := map[string]*Node{}
			Walk(snap, func(n *Node) { byID[n.ID] = n })
			fail := byID[`Tests\CalcTest::testFail`]
			if fail == nil || fail.Status != StatusFailed || !fail.Comparison || fail.Expected != "'foo\nbar'" || fail.Actual != "'foo\nbaz'" {
				t.Fatalf("testFail %+v", fail)
			}
			if fail.FailFile != "/host/tests/CalcTest.php" || fail.FailLine != 17 || fail.Kind != KindTest || fail.Method != "testFail" {
				t.Fatalf("testFail location %+v", fail)
			}
			pairs := byID[`Tests\CalcTest::testPairs`]
			if pairs == nil || pairs.Kind != KindMethod || len(pairs.Children) != 3 || pairs.Status != StatusFailed {
				t.Fatalf("pairs %+v", pairs)
			}
			diff := byID[`Tests\CalcTest::testPairs with data set "diff"`]
			if diff == nil || diff.Kind != KindDataSet || diff.DataSet != `"diff"` || diff.FailLine != 29 || diff.Comparison {
				t.Fatalf("diff %+v", diff)
			}
			if n := byID[`Tests\CalcTest::itSkips`]; n.Status != StatusIgnored || n.Message != "later" {
				t.Fatalf("skip %+v", n)
			}
			if n := byID[`Tests\CalcTest::testIncomplete`]; n.Status != StatusIgnored || n.FailLine != 44 {
				t.Fatalf("incomplete %+v", n)
			}
			if n := byID[`Tests\CalcTest::testRisky`]; n.Status != StatusFailed || n.Output != "hello output" {
				t.Fatalf("risky %+v", n)
			}
			if n := byID[`Tests\CalcTest::testError`]; n.Message != "RuntimeException: boom" || n.FailLine != 39 {
				t.Fatalf("error %+v", n)
			}
			if got := len(tr.Tests(StatusFailed)); got != 4 {
				t.Fatalf("failed tests %d", got)
			}
			if tr.Find(`Tests\CalcTest::testAdd`) == nil || tr.Find("nope") != nil {
				t.Fatal("find")
			}
		})
	}
}

func TestSuitesFlowsAndTermination(t *testing.T) {
	tr := NewTree()
	lines := []string{
		Format("testCount", "count", "3", "flowId", "1"),
		Format("testSuiteStarted", "name", "/app/phpunit.xml", "flowId", "1"),
		Format("testSuiteStarted", "name", "unit", "flowId", "1"),
		Format("testSuiteStarted", "name", `A\XTest`, "locationHint", `php_qn:///app/tests/XTest.php::\A\XTest`, "flowId", "1"),
		// a second worker
		Format("testSuiteStarted", "name", `A\YTest`, "locationHint", `php_qn:///app/tests/YTest.php::\A\YTest`, "flowId", "2"),
		Format("testStarted", "name", "testA", "locationHint", `php_qn:///app/tests/XTest.php::\A\XTest::testA`, "flowId", "1"),
		Format("testStarted", "name", "testB", "locationHint", `php_qn:///app/tests/YTest.php::\A\YTest::testB`, "flowId", "2"),
		Format("testStdOut", "name", "testB", "out", "printed", "flowId", "2"),
		Format("testFinished", "name", "testA", "duration", "5", "flowId", "1"),
		Format("testFinished", "name", "testB", "duration", "7", "flowId", "2"),
		Format("testSuiteFinished", "name", `A\YTest`, "flowId", "2"),
		Format("testStarted", "name", "testC", "locationHint", `php_qn:///app/tests/XTest.php::\A\XTest::testC`, "flowId", "1"),
		"some output of testC",
		"more",
		// the process dies here
	}
	for _, l := range lines {
		tr.Feed(l)
	}
	if c := tr.Counts(); c.Running != 1 || c.Passed != 2 || c.Expected != 3 {
		t.Fatalf("before finish %+v", c)
	}
	snap := tr.Snapshot()
	if len(snap) != 2 || snap[0].Kind != KindSuite || snap[0].Status != StatusRunning || snap[1].ID != `A\YTest` {
		t.Fatalf("roots %+v", snap)
	}
	tr.Finish()
	c := tr.Counts()
	if c.Terminated != 1 || c.Running != 0 {
		t.Fatalf("after finish %+v", c)
	}
	snap = tr.Snapshot()
	unit := snap[0].Children[0]
	if unit.ID != "/app/phpunit.xml/unit" || unit.Kind != KindSuite || unit.Status != StatusTerminated || unit.DurationMs != 5 {
		t.Fatalf("unit %+v", unit)
	}
	if n := tr.Find(`A\XTest::testC`); n.Output != "some output of testC\nmore" {
		t.Fatalf("output %q", n.Output)
	}
	if n := tr.Find(`A\YTest::testB`); n.Output != "printed" || n.DurationMs != 7 {
		t.Fatalf("stdout %+v", n)
	}
}

func TestAggregateAndRepeat(t *testing.T) {
	if aggregate(nil) != StatusPassed {
		t.Fatal("empty")
	}
	ign := &Node{Status: StatusIgnored}
	if aggregate([]*Node{ign, ign}) != StatusIgnored || aggregate([]*Node{ign, {Status: StatusPassed}}) != StatusPassed {
		t.Fatal("ignored")
	}
	tr := NewTree()
	hint := `php_qn:///app/T.php::\T::testX`
	for i := 0; i < 2; i++ {
		tr.Feed(Format("testStarted", "name", "testX", "locationHint", hint))
		if i == 0 {
			tr.Feed(Format("testFailed", "name", "testX", "message", "m", "details", "/app/T.php:3"))
		}
		tr.Feed(Format("testFinished", "name", "testX", "duration", "1"))
	}
	// --repeat: the same test again resets the result.
	if n := tr.Tests(""); len(n) != 1 || n[0].Status != StatusPassed || n[0].Message != "" {
		t.Fatalf("repeat %+v", n)
	}
	// Unknown messages and tests are ignored.
	if tr.Apply(Message{Name: "testFinished", Attrs: map[string]string{"name": "ghost"}}) {
		t.Fatal("ghost")
	}
	if tr.Apply(Message{Name: "whatever"}) || tr.Apply(Message{Name: "testCount", Attrs: map[string]string{"count": "x"}}) {
		t.Fatal("unknown")
	}
	if tr.Apply(Message{Name: "testSuiteFinished", Attrs: map[string]string{"name": "none"}}) {
		t.Fatal("suite")
	}
	// A late failure after testFinished still lands on the test.
	tr.Feed(Format("testSuiteStarted", "name", "S"))
	tr.Feed(Format("testStarted", "name", "late"))
	tr.Feed(Format("testFinished", "name", "late"))
	tr.Feed(Format("testFailed", "name", "late", "message", "risky"))
	if n := tr.Find("S/late"); n == nil || n.Status != StatusFailed || n.Message != "risky" || n.Kind != KindTest {
		t.Fatalf("late %+v", n)
	}
	// Ignored after failed stays failed.
	tr.Feed(Format("testStarted", "name", "f2"))
	tr.Feed(Format("testFailed", "name", "f2", "message", "x", "expected", "", "actual", "1"))
	tr.Feed(Format("testIgnored", "name", "f2", "message", ""))
	n := tr.Find("S/f2")
	if n.Status != StatusFailed || !n.Comparison || n.Actual != "1" || n.Message != "x" {
		t.Fatalf("f2 %+v", n)
	}
	if !KindDataSet.IsTest() || KindMethod.IsTest() {
		t.Fatal("kinds")
	}
}
