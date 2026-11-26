package phpstan

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The fixtures in testdata were produced by PHPStan 1.12.34, 2.1.56 and
// 2.2.17 on the same two files (testdata/*.php.txt) with level 6 and a
// baseline ignoring one error (phpstan-baseline.neon). Absolute paths are
// anonymized: /var/www/html (as in a container) or /__ROOT__ (replaced
// by the test's project directory).

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// project writes the fixture project (src/, phpstan.neon, baseline) and
// returns its directory.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	files := map[string][]byte{
		"src/Greeter.php":       readFixture(t, "Greeter.php.txt"),
		"src/Shouting.php":      readFixture(t, "Shouting.php.txt"),
		"phpstan-baseline.neon": readFixture(t, "phpstan-baseline.neon"),
		"phpstan.neon":          []byte("includes:\n\t- phpstan-baseline.neon\n\nparameters:\n\tlevel: 6\n\tpaths:\n\t\t- src\n\ttmpDir: build/phpstan-tmp\n"),
	}
	for n, b := range files {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type flat struct {
	File, Message, Identifier, Tip string
	Line                           int
}

func flatten(rs []Raw, base string) []flat {
	var out []flat
	for _, r := range rs {
		p := r.Path
		if base != "" {
			if rel, err := filepath.Rel(base, p); err == nil {
				p = filepath.ToSlash(rel)
			}
		}
		out = append(out, flat{p, r.Message, r.Identifier, r.Tip, r.Line})
	}
	return out
}

const tipIterable = "See: https://phpstan.org/blog/solving-phpstan-no-value-type-specified-in-iterable-type"

// want are the errors PHPStan reports (after the baseline).
var want = []flat{
	{"src/Greeter.php", "Call to an undefined method Acme\\Greeter::missing().", "method.notFound", "", 13},
	{"src/Greeter.php", "Parameter #1 $string of function strlen expects string, int given.", "argument.type", "", 18},
	{"src/Greeter.php", "Method Acme\\Greeter::items() return type has no value type specified in iterable type array.", "missingType.iterableValue", tipIterable, 26},
	{"src/Shouting.php", "Method Acme\\Greeter::shout() should return int but returns string.", "return.type", "", 11},
}

func TestReports(t *testing.T) {
	r, err := ParseReport(readFixture(t, "report.json"))
	if err != nil || r.Format != FormatJSON {
		t.Fatal(r.Format, err)
	}
	if got := flatten(r.Errors, "/var/www/html"); !reflect.DeepEqual(got, want) {
		t.Fatalf("json:\n%v\nwant\n%v", got, want)
	}
	// checkstyle and junit carry no tips (junit no identifiers either).
	noTip := make([]flat, len(want))
	for i, w := range want {
		w.Tip = ""
		noTip[i] = w
	}
	r, err = ParseReport(readFixture(t, "report-checkstyle.xml"))
	if err != nil || r.Format != FormatCheckstyle {
		t.Fatal(r.Format, err)
	}
	if got := flatten(r.Errors, ""); !reflect.DeepEqual(got, noTip) {
		t.Fatalf("checkstyle:\n%v", got)
	}
	r, err = ParseReport(readFixture(t, "report-junit.xml"))
	if err != nil || r.Format != FormatJUnit {
		t.Fatal(r.Format, err)
	}
	for i := range noTip {
		noTip[i].Identifier = ""
	}
	if got := flatten(r.Errors, ""); !reflect.DeepEqual(got, noTip) {
		t.Fatalf("junit:\n%v", got)
	}
	// Text before the JSON payload, general errors, unknown formats.
	r, err = ParseReport([]byte("Note: deprecated\n{\"totals\":{},\"files\":{},\"errors\":[\"Config broken\"]}"))
	if err != nil || len(r.Messages) != 1 {
		t.Fatal(r, err)
	}
	r, err = ParseReport([]byte(`<testsuites><testsuite><testcase name="General error"><failure message="Boom"/></testcase></testsuite></testsuites>`))
	if err != nil || len(r.Messages) != 1 || r.Messages[0] != "Boom" {
		t.Fatal(r, err)
	}
	for _, bad := range []string{"", "hello", "<html/>", "{", "<checkstyle><file", serializedPrefix} {
		if _, err := ParseReport([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// cacheErrors reads a cache fixture, resolves and filters its errors like
// the module does and returns them relative to root.
func cacheErrors(t *testing.T, c Cache, root string, res Resolver) []flat {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join(root, "phpstan.neon"), Vars{WorkDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ignore) != 1 || len(cfg.Files) != 2 {
		t.Fatalf("config: %+v", cfg)
	}
	var abs []Raw
	for _, e := range c.Errors {
		h, ok := res.Resolve(e.Path)
		if !ok {
			t.Fatalf("unresolved %s", e.Path)
		}
		e.Path = h
		if fp, ok := res.Resolve(e.FilePath); ok {
			e.FilePath = fp
		}
		if e.TraitPath != "" {
			e.TraitPath, _ = res.Resolve(e.TraitPath)
		}
		abs = append(abs, e)
	}
	if len(abs) != 5 {
		t.Fatalf("%d errors before the baseline", len(abs))
	}
	kept, msgs := Ignore(abs, cfg.Ignore)
	if len(msgs) != 0 {
		t.Fatal(msgs)
	}
	sortRaw(kept)
	return flatten(kept, root)
}

func TestResultCaches(t *testing.T) {
	root := project(t)
	// 1.12: var_export, absolute container paths mapped to the host.
	c, err := ParseResultCache(bytes.NewReader(readFixture(t, "resultCache-1.12.php")))
	if err != nil || c.Format != CacheVarExport || c.Version != "1.12.34" || c.CacheVersion == "" || c.Analysed.IsZero() {
		t.Fatalf("%+v %v", c, err)
	}
	toHost := func(p string) (string, bool) {
		if rest, ok := strings.CutPrefix(p, "/var/www/html/"); ok {
			return filepath.Join(root, rest), true
		}
		return "", false
	}
	if got := cacheErrors(t, c, root, Resolver{ToHost: toHost}); !reflect.DeepEqual(got, want) {
		t.Fatalf("1.12:\n%v", got)
	}
	// 2.1: var_export with host paths.
	data := bytes.ReplaceAll(readFixture(t, "resultCache-2.1.php"), []byte("/__ROOT__"), []byte(filepath.ToSlash(root)))
	c, err = ParseResultCache(bytes.NewReader(data))
	if err != nil || c.Format != CacheVarExport || c.Version != "2.1.56" {
		t.Fatalf("%+v %v", c, err)
	}
	if got := cacheErrors(t, c, root, Resolver{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("2.1:\n%v", got)
	}
	// 2.2: serialized frames, paths relative to %rootDir%.
	file := filepath.Join(root, "build", "resultCache.php")
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	if err := os.WriteFile(file, readFixture(t, "resultCache-2.2.php"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = ReadResultCache(file)
	if err != nil || c.Format != CacheSerialized || c.Version != "2.2.17" || c.CacheVersion == "" || c.Analysed.IsZero() {
		t.Fatalf("%+v %v", c, err)
	}
	anchor := filepath.Join(root, "vendor", "phpstan", "phpstan")
	if got := cacheErrors(t, c, root, Resolver{Base: anchor}); !reflect.DeepEqual(got, want) {
		t.Fatalf("2.2:\n%v", got)
	}
	// The trait error is shown in the trait, attributed to the class.
	for _, e := range c.Errors {
		if e.Identifier == "return.type" && (!strings.HasSuffix(e.Path, "Shouting.php") || !strings.HasSuffix(e.FilePath, "Greeter.php")) {
			t.Fatalf("%+v", e)
		}
	}
	if DetectFormat(readFixture(t, "resultCache-2.2.php")) != FormatCache || DetectFormat(readFixture(t, "resultCache-1.12.php")) != FormatCache {
		t.Fatal("detect cache")
	}
	if _, err := ParseReport(readFixture(t, "resultCache-2.2.php")); err == nil {
		t.Fatal("a cache is no report")
	}
}

func TestBrokenCaches(t *testing.T) {
	full := readFixture(t, "resultCache-2.2.php")
	i := bytes.Index(full, []byte("errors* "))
	for _, data := range [][]byte{
		nil,
		[]byte("<?php return [];"),
		[]byte(serializedPrefix + "\nmeta x\n"),
		[]byte(serializedPrefix + "\nmeta 999999999999\n"),
		full[:i+30],
		append(append([]byte{}, full[:i]...), []byte("errors* 1\n5\na:1:{\n")...),
		[]byte("<?php declare(strict_types = 1);\n\nreturn [\n\t'meta' => array('phpstanVersion' => '1.0'),\n\t'errorsCallback' => static function (): array { return array (\n 'x' => \n"),
	} {
		if _, err := ParseResultCache(bytes.NewReader(data)); err == nil {
			t.Errorf("%q: no error", truncate(string(data), 60))
		}
	}
	if _, err := ReadResultCache(filepath.Join(t.TempDir(), "missing.php")); err == nil {
		t.Fatal("missing file")
	}
	// Very old caches keep the errors in 'errors'.
	old := "<?php declare(strict_types = 1);\n\nreturn [\n\t'lastFullAnalysisTime' => 1,\n\t'meta' => array (\n  'phpstanVersion' => '0.12.99',\n),\n\t'errors' => array (\n  '/a.php' => \n  array (\n    0 => \n    PHPStan\\Analyser\\Error::__set_state(array(\n       'message' => 'It''s \\'quoted\\' ' . \"\\0\" . 'x',\n       'file' => '/a.php',\n       'line' => NULL,\n       'canBeIgnored' => false,\n    )),\n  ),\n),\n\t'dependencies' => array (),\n];\n"
	c, err := ParseResultCache(strings.NewReader(old))
	if err != nil || len(c.Errors) != 1 || c.Version != "0.12.99" {
		t.Fatalf("%+v %v", c, err)
	}
	if e := c.Errors[0]; e.Path != "/a.php" || e.Line != 0 || e.Ignorable || !strings.HasPrefix(e.Message, "It") {
		t.Fatalf("%+v", e)
	}
}

func TestUnserialize(t *testing.T) {
	v, err := Unserialize([]byte(`a:6:{i:0;N;s:1:"b";b:1;i:2;d:1.5;i:3;O:3:"Foo":2:{s:9:"` + "\x00Foo\x00priv" + `";i:7;s:6:"` + "\x00*\x00pro" + `";r:1;}i:4;E:7:"Foo:Bar";i:5;C:3:"Baz":2:{xy}}`))
	if err != nil {
		t.Fatal(err)
	}
	a := v.(*Array)
	if a.Len() != 6 || a.Values[1] != true || a.Values[2] != 1.5 || a.Values[4] != "Foo:Bar" {
		t.Fatalf("%+v", a)
	}
	o := a.Values[3].(*Object)
	if o.Class != "Foo" || num(field(o, "priv")) != 7 {
		t.Fatalf("%+v", o.Props)
	}
	if _, ok := o.Get("pro"); !ok {
		t.Fatal("protected")
	}
	for _, bad := range []string{"", "x:1;", "i:x;", `s:5:"ab";`, "a:2:{i:0;i:1;}", "a:1:{d:1;i:1;}", "O:1:\"A\":1:{", "b:1"} {
		if _, err := Unserialize([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	deep := strings.Repeat("a:1:{i:0;", maxDepth+2)
	if _, err := Unserialize([]byte(deep)); err == nil {
		t.Fatal("deep")
	}
}

func TestParseLiteral(t *testing.T) {
	v, err := ParseLiteral([]byte(`[ 'a' => __DIR__ . '/x', 2 => -3, 'f' => 1.5E+3, 'o' => (object) array('k' => true), 'e' => \Foo\Bar::BAZ, 'c' => \Foo::make(1), "d" => "t\t\x41\101\\", /* c */ 'n' => null, # c
		'l' => [1, 2,], 7 => PHP_EOL, ]`), "/dir")
	if err != nil {
		t.Fatal(err)
	}
	a := v.(*Array)
	if str(field(a, "a")) != "/dir/x" || field(a, "f") != 1500.0 || str(field(a, "e")) != `Foo\Bar::BAZ` || str(field(a, "d")) != "t\tAA\\" {
		t.Fatalf("%+v", a)
	}
	if o, ok := field(a, "o").(*Object); !ok || !truthy(field(o, "k")) {
		t.Fatal("object")
	}
	if field(a, "l").(*Array).Len() != 2 {
		t.Fatal("list")
	}
	for _, bad := range []string{"", "array(", "'x", `"x`, "[1 => ]", "Foo::", "@"} {
		if _, err := ParseLiteral([]byte(bad), ""); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestNeon(t *testing.T) {
	src := "# comment\nincludes:\n\t- a.neon # trailing\n\t- 'b.neon'\nparameters:\n  level: max\n  paths: [src, 'tests']\n  map: {a: 1, b: [x,\n     y]}\n  tmpDir: %currentWorkingDirectory%/var\n  ignoreErrors:\n    - '#^Foo \\#1$#'\n    - message: \"#^Bar\\\\.$#\"\n      path: src/A.php\n      count: 2\n    -\n      identifier: x.y\n      reportUnmatched: false\n  services:\n    - Foo\\Bar(arg: 1)\n  text: '''\n    line one\n    it's\n  '''\n  url: http://x:80\nlist:\n- a\n- b\nyes: on\n"
	v, err := ParseNeon([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	params := field(v, "parameters")
	if str(field(params, "level")) != "max" || field(params, "paths").(*Array).Len() != 2 {
		t.Fatalf("%+v", params)
	}
	if m := field(params, "map").(*Array); m.Len() != 2 || field(m, "b").(*Array).Len() != 2 {
		t.Fatalf("map %+v", m)
	}
	ign := field(params, "ignoreErrors").(*Array)
	if ign.Len() != 3 || str(ign.Values[0]) != "#^Foo \\#1$#" || str(field(ign.Values[1], "message")) != "#^Bar\\.$#" || num(field(ign.Values[1], "count")) != 2 {
		t.Fatalf("ignore %+v %+v", ign.Values[0], ign.Values[1])
	}
	if str(field(field(params, "services").(*Array).Values[0], "")) != "" || str(field(params, "text")) != "line one\nit's" || str(field(params, "url")) != "http://x:80" {
		t.Fatalf("%+v", params)
	}
	if field(v, "list").(*Array).Len() != 2 || field(v, "yes") != true {
		t.Fatalf("%+v", v)
	}
	for _, bad := range []string{"a: [1, 2", "a: 'x", "a:\n  b: 1\n c: 2", "a: {x: }}"} {
		if _, err := ParseNeon([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	if v, err := ParseNeon(nil); err != nil || v.(*Array).Len() != 0 {
		t.Fatal(v, err)
	}
}

func TestConfig(t *testing.T) {
	root := project(t)
	vars := Vars{WorkDir: root, TempDir: "/tmp/host"}
	cfg, err := LoadConfig(filepath.Join(root, "phpstan.neon"), vars)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := cfg.CacheFile(); !ok || p != filepath.Join(root, "build", "phpstan-tmp", "resultCache.php") {
		t.Fatal(p, ok)
	}
	// Defaults: the system temp dir; unknown inside a container.
	write := func(name, s string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg, _ = LoadConfig(write("a.neon", "parameters:\n\tlevel: 1\n"), vars)
	if p, ok := cfg.CacheFile(); !ok || p != filepath.FromSlash("/tmp/host/phpstan/resultCache.php") {
		t.Fatal(p, ok)
	}
	cfg, _ = LoadConfig(write("b.neon", "parameters:\n\tlevel: 1\n"), Vars{})
	if _, ok := cfg.CacheFile(); ok {
		t.Fatal("unknown temp dir")
	}
	cfg, _ = LoadConfig(write("c.neon", "includes:\n\t- missing.neon\n\t- %rootDir%/conf/x.neon\nparameters:\n\tresultCachePath: %currentWorkingDirectory%/rc.php\n\tignoreErrors:\n\t\t- 'no delimiter'\n\t\t- '#(?<=x)y#'\n\t\t- path: x\n"), vars)
	if p, ok := cfg.CacheFile(); !ok || p != filepath.Join(root, "rc.php") || len(cfg.Warnings) != 4 || len(cfg.Ignore) != 0 {
		t.Fatal(p, ok, cfg.Warnings)
	}
	cfg, _ = LoadConfig(write("d.neon", "parameters:\n\ttmpDir: /tmp/in-container\n\tresultCachePath: %tmpDir%/x.php\n"), vars)
	if p, ok := cfg.CacheFile(); !ok || p != filepath.FromSlash("/tmp/in-container/x.php") {
		t.Fatal(p, ok)
	}
	if _, err := LoadConfig(write("e.neon", "parameters: [\n"), vars); err == nil {
		t.Fatal("broken neon")
	}
	if _, err := LoadConfig(filepath.Join(root, "none.neon"), vars); err == nil {
		t.Fatal("missing")
	}
	// A PHP baseline.
	write("baseline.php", "<?php declare(strict_types = 1);\n\n$ignoreErrors = [];\n$ignoreErrors[] = [\n\t'message' => '#^Call to an undefined method Acme\\\\\\\\Greeter\\\\:\\\\:missing\\\\(\\\\)\\\\.$#',\n\t'identifier' => 'method.notFound',\n\t'count' => 1,\n\t'path' => __DIR__ . '/src/Greeter.php',\n];\n\nreturn ['parameters' => ['ignoreErrors' => $ignoreErrors]];\n")
	cfg, err = LoadConfig(write("f.neon", "includes:\n\t- baseline.php\n"), vars)
	if err != nil || len(cfg.Ignore) != 1 {
		t.Fatal(err, cfg.Warnings)
	}
	e := Raw{Message: `Call to an undefined method Acme\Greeter::missing().`, Identifier: "method.notFound", FilePath: filepath.Join(root, "src", "Greeter.php"), Ignorable: true}
	kept, msgs := Ignore([]Raw{e, e}, cfg.Ignore)
	if len(kept) != 0 || len(msgs) != 1 {
		t.Fatal(kept, msgs)
	}
	write("g.php", "<?php return ['parameters' => ['tmpDir' => __DIR__ . '/var/tmp']];")
	cfg, err = LoadConfig(write("g.neon", "includes:\n\t- g.php\n"), vars)
	if p, _ := cfg.CacheFile(); err != nil || p != filepath.Join(root, "var", "tmp", "resultCache.php") {
		t.Fatal(p, err)
	}
	if FindConfig(root) != filepath.Join(root, "phpstan.neon") || FindConfig(t.TempDir()) != "" {
		t.Fatal("find")
	}
}

func TestIgnoreRules(t *testing.T) {
	re, err := CompilePHPRegex(`~^foo/bar$~i`)
	if err != nil || !re.MatchString("FOO/BAR") {
		t.Fatal(err)
	}
	if re, err := CompilePHPRegex(`{^a\{b}`); err != nil || !re.MatchString("a{b") {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "x", "abc", "#a", "#a#x", "#(#"} {
		if _, err := CompilePHPRegex(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	abs := func(p string) string { return filepath.Join("/p", p) }
	mk := func(v string) *IgnoreRule {
		n, err := ParseNeon([]byte(v))
		if err != nil {
			t.Fatal(err)
		}
		r, err := newIgnoreRule(n, abs)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	e := Raw{Message: "Bad thing.", Identifier: "bad.thing", FilePath: "/p/src/A.php", TraitPath: "/p/src/T.php", Ignorable: true}
	for _, c := range []struct {
		rule string
		want bool
	}{
		{"rawMessage: Bad thing.", true},
		{"rawMessages: [Other, Bad thing.]", true},
		{"identifier: bad.thing\npath: src", true},
		{"identifiers: [x]", false},
		{"message: '#^Bad#'\npath: src/*.php", true},
		{"message: '#^Bad#'\npaths: [src/T.php]", true},
		{"message: '#^Bad#'\npath: src/B.php", false},
		{"message: '#^Good#'", false},
		{"message: '#^Bad#'\npath: 'src/[AB].php'", true},
		{"message: '#^Bad#'\npath: 'sr?/[!B].php'", true},
	} {
		if got := mk(c.rule).matches(e); got != c.want {
			t.Errorf("%s: %v", c.rule, got)
		}
	}
	e.Ignorable = false
	if kept, _ := Ignore([]Raw{e}, []*IgnoreRule{mk("rawMessage: Bad thing.")}); len(kept) != 1 {
		t.Fatal("not ignorable")
	}
	if _, err := newIgnoreRule(int64(1), abs); err == nil {
		t.Fatal("bad entry")
	}
}

func TestLocate(t *testing.T) {
	for _, c := range []struct{ line, msg, want string }{
		{"        return 'Hello ' . $name . $this->missing();", "Call to an undefined method Acme\\Greeter::missing().", "missing"},
		{"        return strlen(42);", "Parameter #1 $string of function strlen expects string, int given.", "strlen"},
		{"    public function items(): array", "Method Acme\\Greeter::items() return type has no value type specified in iterable type array.", "items"},
		{"        echo $undefined;", "Variable $undefined might not be defined.", "$undefined"},
		{"        $this->prop = 1;", "Property Acme\\Greeter::$prop is never read.", "prop"},
		{"        return self::FOO;", "Access to undefined constant Acme\\Greeter::FOO.", "FOO"},
		{"        new Missing();", "Instantiated class Acme\\Missing not found.", "Missing"},
		{"        foo();", "Unknown problem.", "foo();"},
		{"\t\treturn 1;\t", "Something else entirely.", "return 1;"},
	} {
		s, e, ok := Locate(c.line, c.msg)
		if !ok || strings.TrimSpace(c.line[s:e]) != strings.TrimSpace(c.want) {
			t.Errorf("%q: %q", c.msg, c.line[s:e])
		}
	}
	if _, _, ok := Locate("   \t", "x"); ok {
		t.Fatal("blank")
	}
	res := Resolver{Base: "/b", Exists: func(p string) bool { return p == filepath.FromSlash("/b/x.php") || p == filepath.FromSlash("/h/y.php") }}
	if p, ok := res.Resolve("x.php"); !ok || p != filepath.FromSlash("/b/x.php") {
		t.Fatal(p)
	}
	res.ToHost = func(p string) (string, bool) { return filepath.FromSlash("/h/" + filepath.Base(p)), true }
	if p, ok := res.Resolve("/c/y.php"); !ok || p != filepath.FromSlash("/h/y.php") {
		t.Fatal(p)
	}
	if _, ok := res.Resolve("/c/z.php"); ok {
		t.Fatal("missing")
	}
	if _, ok := (Resolver{}).Resolve("rel.php"); ok {
		t.Fatal("no base")
	}
	if _, ok := res.Resolve(""); ok {
		t.Fatal("empty")
	}
}
