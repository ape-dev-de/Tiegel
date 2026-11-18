package qa

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/modules/qa/qakit"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePHPStan(t *testing.T) {
	fs, msgs, err := ParsePHPStan(fixture(t, "phpstan.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []qakit.Finding{
		{File: "/app/src/Repo.php", Line: 1, Severity: qakit.SevError, Message: "Ignored error pattern was not matched."},
		{File: "/app/src/User.php", Line: 7, Severity: qakit.SevError, Code: "property.notFound",
			Message: `Access to an undefined property App\User::$mail. (Learn more: https://phpstan.org/blog/solving-phpstan-access-to-undefined-property)`},
		{File: "/app/src/User.php", Line: 12, Severity: qakit.SevError, Code: "return.type", Message: `Method App\User::name() should return string but returns int.`},
	}
	if !reflect.DeepEqual(fs, want) {
		t.Fatalf("findings\n%+v\nwant\n%+v", fs, want)
	}
	if !reflect.DeepEqual(msgs, []string{"Child process error: memory limit"}) {
		t.Fatalf("messages %v", msgs)
	}
	if _, _, err := ParsePHPStan([]byte("PHP Fatal error: nope")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestParsePsalm(t *testing.T) {
	fs, _, err := ParsePsalm(fixture(t, "psalm.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []qakit.Finding{
		{File: "/var/www/html/src/A.php", Line: 5, Col: 13, EndLine: 5, EndCol: 16, Severity: qakit.SevError, Code: "UndefinedClass",
			Message: "Class, interface or enum named Foo does not exist"},
		{File: "src/A.php", Line: 9, Col: 21, EndLine: 11, EndCol: 24, Severity: qakit.SevInfo, Code: "MissingReturnType",
			Message: "Method A::run does not have a return type"},
	}
	if !reflect.DeepEqual(fs, want) {
		t.Fatalf("findings\n%+v\nwant\n%+v", fs, want)
	}
	// No issues: Psalm prints an empty list (or nothing).
	for _, out := range []string{"[]", ""} {
		if fs, _, err := ParsePsalm([]byte(out)); err != nil || len(fs) != 0 {
			t.Fatalf("%q: %v %v", out, fs, err)
		}
	}
}

func TestParsePHPMD(t *testing.T) {
	fs, msgs, err := ParsePHPMD(fixture(t, "phpmd.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []qakit.Finding{
		{File: "/app/src/Service.php", Line: 14, Severity: qakit.SevWarning, Code: "UnusedLocalVariable", Message: "Avoid unused local variables such as '$tmp'."},
		{File: "/app/src/Service.php", Line: 3, Severity: qakit.SevError, Code: "CouplingBetweenObjects", Message: "The class Service has a coupling between objects value of 14."},
		{File: "/app/src/Service.php", Line: 20, Severity: qakit.SevInfo, Code: "ShortVariable", Message: "Avoid variables with short names like $x."},
	}
	if !reflect.DeepEqual(fs, want) {
		t.Fatalf("findings\n%+v\nwant\n%+v", fs, want)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "/app/src/Broken.php: Unexpected token") {
		t.Fatalf("messages %v", msgs)
	}
}

func TestParsePHPCS(t *testing.T) {
	fs, _, err := ParsePHPCS(fixture(t, "phpcs.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []qakit.Finding{
		{File: "src/Kernel.php", Line: 3, Col: 132, Severity: qakit.SevWarning, Code: "Generic.Files.LineLength.TooLong", Message: "Line exceeds 120 characters; contains 131 characters"},
		{File: "src/Kernel.php", Line: 8, Col: 9, Severity: qakit.SevError, Code: "PSR12.ControlStructures.ControlStructureSpacing.SpaceAfterKeyword",
			Message: "Expected 1 space after IF keyword; 0 found (fixable)"},
	}
	if !reflect.DeepEqual(fs, want) {
		t.Fatalf("findings\n%+v\nwant\n%+v", fs, want)
	}
}

// Every analyzer reads checkstyle reports too (e.g. with an extra
// argument that switches the report format).
func TestParseCheckstyle(t *testing.T) {
	want := []qakit.Finding{
		{File: "/app/src/Kernel.php", Line: 8, Col: 9, Severity: qakit.SevError, Code: "PSR12.ControlStructures.ControlStructureSpacing.SpaceAfterKeyword", Message: "Expected 1 space after IF keyword; 0 found"},
		{File: "/app/src/Kernel.php", Line: 3, Col: 132, Severity: qakit.SevWarning, Code: "Generic.Files.LineLength.TooLong", Message: "Line exceeds 120 characters & more"},
		{File: "src/Other.php", Line: 1, Severity: qakit.SevInfo, Message: "Note"},
	}
	for name, parse := range map[string]func([]byte) ([]qakit.Finding, []string, error){
		"phpstan": ParsePHPStan, "psalm": ParsePsalm, "phpmd": ParsePHPMD, "phpcs": ParsePHPCS,
	} {
		fs, _, err := parse(fixture(t, "checkstyle.xml"))
		if err != nil || !reflect.DeepEqual(fs, want) {
			t.Fatalf("%s: %v\n%+v", name, err, fs)
		}
	}
	if _, err := qakit.ParseCheckstyle([]byte("<?xml version=\"1.0\"?><checkstyle><file")); err == nil {
		t.Fatal("broken xml accepted")
	}
}

// Container paths of a report map back to host files.
func TestReportMapsPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	m := compose.Mappings{}.Set(root, "/app")
	fs, _, _ := ParsePHPMD(fixture(t, "phpmd.json"))
	fs = append(fs, qakit.Finding{File: "src/Rel.php", Line: 1, Message: "rel", Severity: ""}, qakit.Finding{File: "", Line: 1, Message: "nowhere"})
	rep := qakit.Report(root, m, fs, nil, qakit.SeverityAuto)
	if len(rep.Files) != 2 || rep.Files[0].File != filepath.Join(root, "src", "Service.php") || len(rep.Files[0].Diagnostics) != 3 ||
		rep.Files[1].File != filepath.Join(root, "src", "Rel.php") || rep.Files[1].Diagnostics[0].Severity != qakit.SevWarning {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Messages) != 1 || !strings.Contains(rep.Messages[0], "nowhere") {
		t.Fatalf("messages %v", rep.Messages)
	}
	rep = qakit.Report(root, m, fs[:1], nil, qakit.SevHint)
	if rep.Files[0].Diagnostics[0].Severity != qakit.SevHint {
		t.Fatalf("severity override %+v", rep)
	}
}

// The argument builders produce the documented command lines.
func TestArgs(t *testing.T) {
	c := qakit.Call{File: "src/__tmp_A.php", Paths: []string{"src", "tests/X.php"}, Config: "cfg.xml"}
	cases := map[string][]string{
		"pint":   Pint().Format.Args(c),
		"fixer":  PHPCSFixer().Format.Args(c),
		"phpcbf": PHPCS().Format.Args(c),
		"phpcs":  PHPCS().Analyze.Args(c),
		"stan":   PHPStan().Analyze.Args(c),
		"psalm":  Psalm().Analyze.Args(c),
		"phpmd":  PHPMD().Analyze.Args(c),
	}
	want := map[string]string{
		"pint":   "--no-interaction --config=cfg.xml src/__tmp_A.php",
		"fixer":  "fix --no-interaction --using-cache=no --show-progress=none --quiet --config=cfg.xml src/__tmp_A.php",
		"phpcbf": "-q --no-colors --standard=cfg.xml --stdin-path=src/__tmp_A.php -",
		"phpcs":  "--report=json -q --no-colors --standard=cfg.xml src tests/X.php",
		"stan":   "analyse --error-format=json --no-progress --no-interaction --configuration=cfg.xml src tests/X.php",
		"psalm":  "--output-format=json --no-progress --monochrome --config=cfg.xml src tests/X.php",
		"phpmd":  "src,tests/X.php json cfg.xml --exclude vendor/*",
	}
	for k, w := range want {
		if got := strings.Join(cases[k], " "); got != w {
			t.Errorf("%s: %q, want %q", k, got, w)
		}
	}
	if got := strings.Join(PHPMD().Analyze.Args(qakit.Call{}), " "); got != ". json "+phpmdRulesets+" --exclude vendor/*" {
		t.Errorf("phpmd defaults: %q", got)
	}
	// Default paths: the tool's configuration decides when present.
	dir := t.TempDir()
	if got := PHPStan().Analyze.DefaultPaths(dir); !reflect.DeepEqual(got, []string{"."}) {
		t.Fatalf("no src: %v", got)
	}
	_ = os.Mkdir(filepath.Join(dir, "src"), 0o755)
	if got := PHPCS().Analyze.DefaultPaths(dir); !reflect.DeepEqual(got, []string{"src"}) {
		t.Fatalf("src: %v", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "phpstan.neon.dist"), nil, 0o644)
	if got := PHPStan().Analyze.DefaultPaths(dir); got != nil {
		t.Fatalf("config: %v", got)
	}
	if !PHPCS().Format.Unchanged(0, []byte("  ")) || PHPCS().Format.Unchanged(1, []byte("<?php\n")) {
		t.Fatal("phpcbf unchanged detection")
	}
}
