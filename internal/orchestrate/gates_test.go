package orchestrate

import (
	"context"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

const hollowFixture = `<?php
namespace App\Tests;

use PHPUnit\Framework\TestCase;
use PHPUnit\Framework\Attributes\Test;
use App\Billing\Invoice;

final class InvoiceTest extends TestCase
{
    public function testEmpty(): void
    {
        // nothing yet
    }

    public function testSkipped(): void
    {
        $this->markTestSkipped('later');
    }

    public function testNoAssertion(): void
    {
        $invoice = new Invoice(10);
        $invoice->total();
    }

    public function testTrivial(): void
    {
        $this->assertTrue(true);
    }

    public function testFreshObject(): void
    {
        $invoice = new Invoice(10);
        $this->assertNotNull($invoice);
        $this->assertInstanceOf(Invoice::class, $invoice);
    }

    public function testSwallows(): void
    {
        try {
            $this->assertSame(10, (new Invoice(10))->total());
        } catch (\Throwable $e) {
            // ignore
        }
    }

    public function testMocksTheSut(): void
    {
        $invoice = $this->createMock(Invoice::class);
        $invoice->method('total')->willReturn(10);
        $this->assertSame(10, $invoice->total());
    }

    /** @test */
    public function itComputesTheTotal(): void
    {
        $this->assertSame(12, (new Invoice(10))->withTax(0.2)->total());
    }

    #[Test]
    public function viaHelper(): void
    {
        $this->assertTotal(10, new Invoice(10));
    }

    public function testExpectsException(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new Invoice(-1);
    }

    public function testString(): void
    {
        $s = "function testFake() { }";
        $this->assertSame('a', strtolower('A'));
    }

    private function assertTotal(int $want, Invoice $i): void
    {
        $this->assertSame($want, $i->total());
    }
}
`

func TestHollowTests(t *testing.T) {
	issues := HollowTests("tests/InvoiceTest.php", []byte(hollowFixture), nil)
	got := map[string]string{}
	for _, is := range issues {
		name := strings.SplitN(strings.TrimPrefix(is.Claim, "InvoiceTest::"), " ", 2)
		got[name[0]] = name[1] + " @" + is.Evidence
		if is.To != api.RoleQA || is.Gate != "hollow" {
			t.Errorf("%+v", is)
		}
	}
	want := map[string]string{
		"testEmpty":       "has an empty body @tests/InvoiceTest.php:10",
		"testSkipped":     "is skipped",
		"testNoAssertion": "has no assertion",
		"testTrivial":     "asserts only trivialities",
		"testFreshObject": "asserts only trivialities",
		"testSwallows":    "swallows the failure",
		"testMocksTheSut": "mocks the class under test (Invoice)",
	}
	for k, v := range want {
		if !strings.Contains(got[k], v) {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	for _, ok := range []string{"itComputesTheTotal", "viaHelper", "testExpectsException", "testString", "testFake", "assertTotal"} {
		if got[ok] != "" {
			t.Errorf("%s flagged: %s", ok, got[ok])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	// Limited to changed lines.
	if is := HollowTests("t.php", []byte(hollowFixture), []int{lineOf(hollowFixture, "assertTrue(true)")}); len(is) != 1 || !strings.Contains(is[0].Claim, "testTrivial") {
		t.Fatalf("%+v", is)
	}
}

func TestScanPHP(t *testing.T) {
	src := []byte("<?php\ninterface I { public function a(): int; }\nabstract class B {\n  abstract protected function b();\n  public function c() { $x = '}'; /* } */ return <<<EOT\n  }\n  EOT;\n }\n}\nfunction free(&$r) { return [1]; }\n")
	fs := ScanPHP(src)
	if len(fs) != 4 {
		t.Fatalf("%+v", fs)
	}
	if !fs[0].Abstract || fs[0].Class != "I" || !fs[1].Abstract || fs[2].Name != "c" || fs[2].Class != "B" || fs[2].EndLine != 8 || fs[3].Class != "" || fs[3].Name != "free" {
		t.Fatalf("%+v", fs)
	}
	if got := splitArgs(`1, [2, 3], f(4, 5), 'a,b'`); len(got) != 4 || got[3] != `'a,b'` {
		t.Fatal(got)
	}
	if v := string(CodeView([]byte("#[Attr]\n# comment\n$a = \"x\";"))); !strings.Contains(v, "#[Attr]") || strings.Contains(v, "comment") || strings.Contains(v, "x") {
		t.Fatal(v)
	}
}

func change(path, old, cur string) FileChange {
	return newChange(path, []byte(old), []byte(cur), old != "", true)
}

func TestUnfinished(t *testing.T) {
	old := "<?php\nclass A\n{\n    public function keep(): int\n    {\n        return 1;\n    }\n}\n"
	cur := "<?php\nclass A\n{\n    public function keep(): int\n    {\n        return 1;\n    }\n\n    public function empty(): void\n    {\n    }\n\n    public function stub(): ?array\n    {\n        return null;\n    }\n\n    public function __construct(private int $x)\n    {\n    }\n\n    public function later(): void\n    {\n        // TODO handle errors\n        throw new \\LogicException('Not implemented');\n    }\n}\n"
	issues := Unfinished([]FileChange{change("src/A.php", old, cur), change("tests/ATest.php", "", "<?php // TODO in a test is fine\n")})
	var claims []string
	for _, is := range issues {
		claims = append(claims, is.Claim+" @"+is.Evidence)
	}
	all := strings.Join(claims, "\n")
	for _, want := range []string{"empty() has an empty body @src/A.php:9", "stub() is a stub (return null;)", "added TODO", "not implemented: throw new"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if strings.Contains(all, "__construct") || strings.Contains(all, "keep") || strings.Contains(all, "ATest") {
		t.Fatal(all)
	}
	steps := []api.PlanStep{{ID: "S1", Status: "done", Files: []string{"src/A.php"}}, {ID: "S2", Status: "done", Files: []string{"src/B.php"}}, {ID: "S3", Title: "x", Status: "partial"}, {ID: "S4", Status: "done"}}
	st := StepsDone(steps, []FileChange{change("src/A.php", old, cur)})
	if len(st) != 2 || !strings.Contains(st[0].Claim, "S2") || !strings.Contains(st[1].Claim, "S3 (x) is partial") {
		t.Fatalf("%+v", st)
	}
}

func TestMutants(t *testing.T) {
	old := "<?php\nfunction f($a, $b) {\n    return 0;\n}\n"
	cur := "<?php\nfunction f($a, $b) {\n    if ($a === $b) {\n        return true;\n    }\n    $s = 'a && b';\n    return $a > $b;\n}\n"
	ms := Mutants([]FileChange{change("src/f.php", old, cur), change("tests/fTest.php", "", "<?php\n")}, 0)
	var whats []string
	for _, m := range ms {
		whats = append(whats, m.What)
		if m.Path != "src/f.php" {
			t.Fatal(m.Path)
		}
	}
	got := strings.Join(whats, ",")
	if got != "revert the change,negate the condition,true → false,> → <=" {
		t.Fatal(got)
	}
	if !strings.Contains(string(ms[0].Src), "return 0;") || strings.Contains(string(ms[0].Src), "if (") {
		t.Fatal(string(ms[0].Src))
	}
	if !strings.Contains(string(ms[1].Src), "if (!($a === $b))") {
		t.Fatal(string(ms[1].Src))
	}
	if limited := Mutants([]FileChange{change("src/f.php", old, cur)}, 2); len(limited) != 2 || limited[0].What != "revert the change" {
		t.Fatalf("%+v", limited)
	}
	// New files have no revert mutant.
	if ms := Mutants([]FileChange{change("src/n.php", "", "<?php\nreturn false;\n")}, 0); len(ms) != 1 || ms[0].What != "false → true" {
		t.Fatalf("%+v", ms)
	}
}

func TestCoverage(t *testing.T) {
	clover := `<?xml version="1.0"?><coverage><project>
<package name="App"><file name="/app/src/A.php"><line num="3" type="stmt" count="1"/><line num="4" type="stmt" count="0"/><line num="5" type="method" count="0"/></file></package>
<file name="/app/src/B.php"><line num="2" type="stmt" count="0"/></file></project></coverage>`
	cov, err := ParseClover([]byte(clover), func(p string) string { return strings.TrimPrefix(p, "/app/") })
	if err != nil || !cov["src/A.php"][3] || cov["src/A.php"][4] || len(cov["src/A.php"]) != 2 || len(cov["src/B.php"]) != 1 {
		t.Fatalf("%v %v", cov, err)
	}
	if _, err := ParseClover([]byte("<coverage"), nil); err == nil {
		t.Fatal("broken XML")
	}
	ch := change("src/A.php", "<?php\n", "<?php\n$a = 1;\n$b = 2;\n$c = 3;\n")
	is := Uncovered([]FileChange{ch}, map[string]map[int]bool{"src/A.php": {2: true, 3: false, 4: false}})
	if len(is) != 1 || !strings.Contains(is[0].Claim, "3-4") || is[0].Evidence != "src/A.php:3" {
		t.Fatalf("%+v", is)
	}
	if ranges([]int{1, 2, 3, 5, 7, 8}) != "1-3, 5, 7-8" {
		t.Fatal(ranges([]int{1, 2, 3, 5, 7, 8}))
	}
}

func TestVerifyEvidence(t *testing.T) {
	root := t.TempDir()
	_ = writeFile(root, "src/A.php", []byte("<?php\nclass A\n{\n    public function run() {}\n}\n"+strings.Repeat("\n", 40)+"function helper() {}\n"))
	syms := func(_ context.Context, name string) []string {
		if name == "Moved" {
			return []string{"src/Moved.php"}
		}
		return nil
	}
	cases := []struct {
		ev, claim, status, note string
	}{
		{"", "x", api.FindingUnknown, "no evidence"},
		{"just words here", "x", api.FindingUnknown, "not path:line"},
		{"src/A.php:4", "`A::run` runs", "", ""},
		{"src/A.php#L4", "`run()` runs", "", ""},
		{"src/A.php:400", "x", api.FindingRefuted, "has only"},
		{"src/A.php:2-3", "`Missing::thing` exists", api.FindingRefuted, "does not occur"},
		{"src/A.php:2", "`helper` is far away", "", "not near line 2"},
		{"src/Gone.php:3", "`Moved::x` exists", api.FindingRefuted, "declared in src/Moved.php"},
	}
	for _, c := range cases {
		st, note, _ := VerifyEvidence(context.Background(), root, c.ev, c.claim, syms)
		if st != c.status || !strings.Contains(note, c.note) {
			t.Errorf("%q %q: %q %q", c.ev, c.claim, st, note)
		}
	}
}

func TestExtractJSONAndChecks(t *testing.T) {
	if b, err := ExtractJSON("x ```json\n{\"a\":1}\n``` y ```json\n{\"a\":2}\n```"); err != nil || string(b) != `{"a":2}` {
		t.Fatal(string(b), err)
	}
	if b, err := ExtractJSON(`sure: {"a": {"b": 1}} done`); err != nil || string(b) != `{"a": {"b": 1}}` {
		t.Fatal(string(b), err)
	}
	if _, err := ExtractJSON("nothing"); err != ErrNoJSON {
		t.Fatal(err)
	}
	if _, err := ExtractJSON("{broken"); err == nil {
		t.Fatal("broken accepted")
	}
	bad := map[string]interface{ check() error }{
		`{"findings":[{"id":"F1","status":"maybe"}]}`: &Validation{},
		`{}`: &Validation{},
		`{"issues":[{"severity":"huge","claim":"x"}]}`: &QAReview{},
		`{"summary":""}`: &Summary{},
		`{"summary":"x","steps":[{"id":"S1","status":"almost"}]}`:                   &DevResult{},
		`{"summary":"x","steps":[],"responses":[{"item":"Q1","verdict":"reject"}]}`: &DevResult{},
		`{"summary":"x"}`: &QAResult{},
		`{"issues":[{"severity":"minor","target":"docs","claim":"x"}]}`: &Rating{},
		`{"issues":[],"rebuttals":[{"item":"Q1","verdict":"maybe"}]}`:   &Rating{},
		`{"decisions":[{"item":"Q1","winner":"both","reason":"x"}]}`:    &Mediation{},
		`{"decisions":[]}`:         &Mediation{},
		`{"summary":" "}`:          &Final{},
		`{"steps":[{"title":""}]}`: &Contribution{},
		` {}`:                      &Contribution{},
	}
	for in, v := range bad {
		if err := decode(in, v); err == nil {
			t.Errorf("%s accepted as %T", in, v)
		}
	}
	var r Rating
	if err := decode(`{"issues":[{"severity":"minor","target":"impl","claim":"x"}],"rebuttals":[{"item":"Q1","verdict":"withdraw"}]}`, &r); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrap(t *testing.T) {
	cj := []byte(`{"autoload":{"psr-4":{"App\\":"src/"}},"autoload-dev":{"psr-4":{"App\\Tests\\":["tests/","tests2"]}}}`)
	b, err := WorktreeBootstrap("../../../vendor", cj, "tests/bootstrap.php")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"$loader = require __DIR__ . '/../../../vendor/autoload.php';",
		"$loader->setPsr4('App\\\\', [__DIR__ . '/src/']);",
		"$loader->setPsr4('App\\\\Tests\\\\', [__DIR__ . '/tests/', __DIR__ . '/tests2']);",
		"require __DIR__ . '/tests/bootstrap.php';",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in\n%s", want, b)
		}
	}
	if b, _ := WorktreeBootstrap("v", nil, "vendor/autoload.php"); strings.Contains(b, "require __DIR__ . '/vendor/autoload.php'") {
		t.Fatal(b)
	}
	if _, err := WorktreeBootstrap("v", []byte("{"), ""); err == nil {
		t.Fatal("broken composer.json")
	}
}

func TestTestReport(t *testing.T) {
	r := TestReport{Tests: []TestResult{{ID: `App\T::a`, Status: TestPassed}, {ID: `App\T::b with data set #0`, Status: TestFailed, Message: "boom", Details: "a\nb\nc\nd\ne", FailFile: "t.php", FailLine: 3}}}
	if r.Green() || len(r.Failed()) != 1 || len(r.Find(`\App\T::b`)) != 1 || len(r.Find("App\\T::a")) != 1 {
		t.Fatal(r)
	}
	c := r.Failed()[0].Compact()
	if !strings.Contains(c, "FAILED at t.php:3: boom") || strings.Contains(c, "e") && strings.Count(c, "\n") != 4 {
		t.Fatal(c)
	}
	if (TestReport{}).Green() {
		t.Fatal("empty report green")
	}
}

func lineOf(s, sub string) int { return strings.Count(s[:strings.Index(s, sub)], "\n") + 1 }
