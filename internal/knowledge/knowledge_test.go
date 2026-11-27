package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		f := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtractAndParse(t *testing.T) {
	good := "Here you go:\n```json\n" + `{"name":"Checkout","summary":"Orders.","triggers":["POST /checkout"],"steps":[{"file":"src/A.php","symbol":"A::run","what":"saves"}],"writes":["orders",{"name":"lines","kind":"database"}],"openQuestions":["why?",{"question":"how?","refs":[{"file":"src/A.php"}]}]}` + "\n```\n"
	d, err := ParseDoc(LevelProcesses, good)
	p := d.(*ProcessDoc)
	if err != nil || p.Summary != "Orders." || len(p.Steps) != 1 || p.Writes[0].Name != "orders" || p.Writes[1].Kind != "database" || p.OpenQuestions[0].Question != "why?" || p.OpenQuestions[1].Refs[0].File != "src/A.php" {
		t.Fatal(p, err)
	}
	if len(d.refs()) != 2 || d.summary() != "Orders." {
		t.Fatal(d.refs())
	}
	// Unfenced object in text.
	if _, err := ParseDoc(LevelOverview, `ok {"summary":"x","containers":[{"name":"App"}]} done`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDoc(LevelOverview, "no json here"); !errors.Is(err, ErrNoJSON) {
		t.Fatal(err)
	}
	if _, err := ParseDoc(LevelOverview, "```json\n{\"summary\": \n```"); err == nil {
		t.Fatal("broken JSON accepted")
	}
	if _, err := ParseDoc("nope", "{}"); err == nil {
		t.Fatal("unknown level")
	}
	for level, bad := range map[string]string{
		LevelOverview:    `{"summary":"","containers":[{"name":""}],"relations":[{"from":"a"}],"answeredQuestions":[{"id":""}],"openQuestions":[{"question":""}]}`,
		LevelEntryPoints: `{"summary":"","entries":[{"name":"","purpose":""}]}`,
		LevelProcesses:   `{"name":"","summary":"","steps":[]}`,
		LevelComponents:  `{"name":"","summary":"","methods":[{"file":"a"}]}`,
		LevelQuestions:   `{}`,
	} {
		if _, err := ParseDoc(level, bad); err == nil {
			t.Fatal("accepted", level)
		}
	}
	if _, err := ParseDoc(LevelProcesses, `{"name":"n","summary":"s","steps":[{"file":"","what":""}],"reads":[{"name":""}]}`); err == nil || !strings.Contains(err.Error(), "steps[0].file") || !strings.Contains(err.Error(), "no name") {
		t.Fatal(err)
	}
	if _, err := ParseDoc(LevelOverview, `{"summary":5}`); err == nil {
		t.Fatal("wrong type accepted")
	}
	// Many errors are capped.
	var steps []string
	for i := 0; i < 12; i++ {
		steps = append(steps, `{"file":"","what":"w"}`)
	}
	if _, err := ParseDoc(LevelProcesses, `{"name":"n","summary":"s","steps":[`+strings.Join(steps, ",")+`]}`); err == nil || !strings.Contains(err.Error(), "more") {
		t.Fatal(err)
	}
	v, doc, err := ParseValidation(LevelOverview, "```json\n{\"confidence\":0.9,\"issues\":[\"x\"],\"result\":{\"summary\":\"s\",\"containers\":[{\"name\":\"App\"}]}}\n```")
	if err != nil || v.Confidence != 0.9 || doc.(*OverviewDoc).Containers[0].Name != "App" {
		t.Fatal(v, err)
	}
	for _, bad := range []string{`{"confidence":2,"result":{"summary":"s"}}`, `{"confidence":0.5}`, `nothing`, `{"confidence":"x"}`, `{"confidence":0.5,"result":{"summary":5}}`} {
		if _, _, err := ParseValidation(LevelOverview, bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, _, err := ParseValidation("nope", `{"confidence":0.5,"result":{}}`); err == nil {
		t.Fatal("unknown level")
	}
	for _, l := range Levels {
		var v any
		if err := json.Unmarshal([]byte(Schema(l)), &v); err == nil {
			continue // schemas are illustrative, not strict JSON
		}
		if !strings.Contains(Schema(l), "openQuestions") {
			t.Fatal("schema without questions", l)
		}
	}
	if Schema("x") != "{}" || NewDoc("x") != nil {
		t.Fatal("unknown level")
	}
	a := &AnswersDoc{StillOpen: []string{"q"}}
	if a.Check() != nil || a.summary() != "0 answered, 1 still open" {
		t.Fatal(a.summary())
	}
	var e Data
	if json.Unmarshal([]byte(`5`), &e) == nil {
		t.Fatal("number accepted")
	}
	var q Question
	if json.Unmarshal([]byte(`5`), &q) == nil {
		t.Fatal("number accepted")
	}
}

func TestLevels(t *testing.T) {
	l, err := ParseLevels("")
	if err != nil || len(l) != 5 || l[0] != LevelOverview {
		t.Fatal(l, err)
	}
	if l, _ := ParseLevels("processes, overview,routes,answer"); strings.Join(l, ",") != "overview,entrypoints,processes,questions" {
		t.Fatal(l)
	}
	if l, _ := ParseLevels("component,process,process"); strings.Join(l, ",") != "processes,components" {
		t.Fatal(l)
	}
	if _, err := ParseLevels("nope"); err == nil {
		t.Fatal("unknown level")
	}
	for _, l := range append(Levels, "x") {
		if LevelTitle(l) == "" {
			t.Fatal(l)
		}
	}
	if LevelRank("x") != len(Levels) || (Item{ID: "a"}).Data() != "data/a.json" {
		t.Fatal("rank")
	}
}

func TestFilterFilesAndPSR4(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"composer.json": `{"autoload":{"psr-4":{"App\\":"src/","Lib\\":["lib"]}}}`})
	psr := ReadPSR4(root)
	if psr[`App\`][0] != "src" || psr[`Lib\`][0] != "lib" {
		t.Fatal(psr)
	}
	files := []string{"src/Kernel.php", "src/Billing/Invoice.php", "lib/X.php", "vendor/a/b.php", "tests/FooTest.php", "public/index.php", "README.md"}
	if got := FilterFiles(files, nil, nil); strings.Join(got, ",") != "lib/X.php,public/index.php,src/Billing/Invoice.php,src/Kernel.php" {
		t.Fatal(got)
	}
	if got := FilterFiles(files, []string{"tests", "src/Billing/"}, nil); strings.Join(got, ",") != "src/Billing/Invoice.php,tests/FooTest.php" {
		t.Fatal(got)
	}
	if ReadPSR4(t.TempDir()) != nil {
		t.Fatal("psr4 without composer")
	}
	write(t, root, map[string]string{"bad/composer.json": `{`, "odd/composer.json": `{"autoload":{"psr-4":{"A\\":5,"B\\":""}}}`})
	if ReadPSR4(filepath.Join(root, "bad")) != nil {
		t.Fatal("broken")
	}
	if p := ReadPSR4(filepath.Join(root, "odd")); len(p[`A\`]) != 0 || p[`B\`][0] != "." {
		t.Fatal(p)
	}
	if Slug("App\\Controller\\Order") != "app-controller-order" || Slug("--") != "x" || len(Slug(strings.Repeat("a", 200))) != 80 {
		t.Fatal(Slug("App\\Controller\\Order"))
	}
}

func TestManifestStaleAndLegacy(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	write(t, root, map[string]string{"a.php": "<?php a", "b.php": "<?php b", "c.php": "<?php c"})
	it := Item{ID: "u", Level: LevelProcesses, MD: "processes/u.md", Files: []string{"a.php", "b.php"}, Inputs: map[string]string{"@entries": "1"}}
	h := ItemHashes(root, &it)
	if it.Bytes != 14 || h["a.php"] == "" || h["@entries"] != "1" || HashFile(filepath.Join(root, "nope")) != "" {
		t.Fatal(it, h)
	}
	m, err := LoadManifest(dir)
	if err != nil || m.Legacy || m.Stale(dir, root, it, h) != StaleNew {
		t.Fatal(err)
	}
	m.Items["u"] = &ItemState{ID: "u", Status: StatusFailed}
	if m.Stale(dir, root, it, h) != StaleFailed {
		t.Fatal("failed")
	}
	files := map[string]string{"c.php": HashFile(filepath.Join(root, "c.php"))}
	for k, v := range h {
		files[k] = v
	}
	m.Items["u"] = &ItemState{ID: "u", Level: LevelProcesses, Status: StatusDone, MD: "processes/u.md", Files: files}
	if m.Stale(dir, root, it, h) != StaleMissing {
		t.Fatal("md file missing")
	}
	write(t, dir, map[string]string{"processes/u.md": "x"})
	if m.Stale(dir, root, it, h) != "" {
		t.Fatal("current")
	}
	// A cited file (not one of the item's files) changed.
	write(t, root, map[string]string{"c.php": "<?php changed"})
	if m.Stale(dir, root, it, h) != StaleChanged {
		t.Fatal("cited file changed")
	}
	write(t, root, map[string]string{"c.php": "<?php c"})
	write(t, root, map[string]string{"b.php": "<?php changed"})
	if m.Stale(dir, root, it, ItemHashes(root, &it)) != StaleChanged {
		t.Fatal("changed")
	}
	it2 := Item{ID: "u", Files: []string{"a.php", "b.php"}}
	h2 := ItemHashes(root, &it2)
	m.Items["u"].Files["b.php"] = h2["b.php"]
	if m.Stale(dir, root, it2, h2) != StaleChanged {
		t.Fatal("input gone")
	}
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	m2, err := LoadManifest(dir)
	if err != nil || m2.Items["u"].MD != "processes/u.md" || len(m2.Sorted()) != 1 || len(m2.Level(LevelProcesses)) != 1 || m2.Clone().Items["u"] == m2.Items["u"] {
		t.Fatal(m2, err)
	}
	write(t, dir, map[string]string{"manifest.json": "{broken"})
	if m3, err := LoadManifest(dir); err == nil || len(m3.Items) != 0 {
		t.Fatal("broken manifest")
	}
	write(t, dir, map[string]string{"manifest.json": `{"version":2,"items":null}`})
	if m4, err := LoadManifest(dir); err != nil || m4.Items == nil || m4.Legacy {
		t.Fatal(err)
	}
	write(t, dir, map[string]string{"manifest.json": `{"version":2,"items":{"u":5}}`})
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("bad items")
	}
	// The old unit format (D560) is legacy: empty, rebuilt.
	write(t, dir, map[string]string{"manifest.json": `{"version":1,"units":{"src":{"id":"src","md":"units/src.md","status":"done"}}}`, "units/src.md": "old"})
	if m5, err := LoadManifest(dir); err != nil || !m5.Legacy || len(m5.Items) != 0 {
		t.Fatal(m5, err)
	}
	legacy := t.TempDir()
	write(t, legacy, map[string]string{"units/a.md": "x"})
	if m6, _ := LoadManifest(legacy); !m6.Legacy {
		t.Fatal("units without manifest")
	}
	if err := WriteFileAtomic(filepath.Join(dir, "x", "y.txt"), []byte("z")); err != nil {
		t.Fatal(err)
	}
}

func TestVerify(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"src/Order.php":   "<?php\nnamespace App;\nclass Order {\n  public function place() {}\n  private $total;\n}\n",
		"src/Mailer.php":  "<?php\nclass Mailer {\n  function send() {}\n}\n",
		"src/Nothing.php": "<?php\n",
		"config/app.yaml": "x: 1\n",
	})
	c := &Checker{Root: root, Lookup: func(sym string) []string {
		if strings.HasSuffix(sym, "send") {
			return []string{filepath.Join(root, "src/Mailer.php")}
		}
		return nil
	}}
	d := &ProcessDoc{Name: "n", Summary: "s", Steps: []Step{
		{Ref: Ref{File: "./src/Order.php", Symbol: `App\Order::place()`, Line: 99}, What: "a"},
		{Ref: Ref{File: filepath.Join(root, "src/Order.php") + ":3"}, What: "b"},
		{Ref: Ref{File: "src/Nothing.php", Symbol: "Mailer::send"}, What: "c"},
		{Ref: Ref{File: "src/Gone.php", Symbol: "X::y"}, What: "d"},
		{Ref: Ref{File: "src/Nothing.php", Symbol: "Order::cancel"}, What: "e"},
		{Ref: Ref{File: "../etc/passwd"}, What: "f"},
		{Ref: Ref{File: "src/Order.php", Symbol: "$total"}, What: "g"},
	}, Reads: []Data{{Name: "cfg", Ref: &Ref{File: "config/app.yaml"}}, {Name: "none", Ref: &Ref{}}}}
	un, notes := c.Verify(d)
	s := d.Steps
	if !*s[0].Verified || s[0].Line != 4 || s[0].File != "src/Order.php" {
		t.Fatal(s[0])
	}
	if !*s[1].Verified || s[1].File != "src/Order.php" {
		t.Fatal(s[1])
	}
	if !*s[2].Verified || s[2].File != "src/Mailer.php" || s[2].Line != 3 {
		t.Fatal(s[2])
	}
	if *s[3].Verified || *s[4].Verified || *s[5].Verified || !*s[6].Verified || s[6].Line != 5 || !s[3].Unverified() {
		t.Fatal(s[3:])
	}
	if un != 3 || len(notes) != 4 || !*d.Reads[0].Ref.Verified || d.Reads[1].Ref.Verified != nil {
		t.Fatal(un, notes)
	}
	for in, want := range map[string]string{`App\Foo::bar()`: "bar", `App\Foo`: "Foo", "$x": "x", "a->b": "b", "": ""} {
		if got := ShortSymbol(in); got != want {
			t.Fatal(in, got)
		}
	}
	for in, want := range map[string]string{`\App\Foo::bar()`: `App\Foo`, "X->run": "X", `App\Foo`: `App\Foo`, "helper": "", "$x": ""} {
		if got := SymbolClass(in); got != want {
			t.Fatal(in, got)
		}
	}
	if findSymbol([]string{"x"}, "") != 0 || findSymbol([]string{"a", "use foo here"}, "foo") != 2 {
		t.Fatal("findSymbol")
	}
}

func TestMarkdown(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"src/A.php": "<?php"})
	l := Linker{Root: root}
	if got := l.Link("src/A.php", 3); !strings.HasPrefix(got, "[src/A.php:3](file://") || !strings.HasSuffix(got, "/src/A.php#L3)") {
		t.Fatal(got)
	}
	meta := Meta{ID: "u", Level: LevelProcesses, Title: `App\Billing`, Sources: []string{"src/A.php"}, Commit: "abc", GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Confidence: 0.8, Model: "m:x"}
	fields, body := SplitFrontMatter(meta.FrontMatter() + "\n# T\n")
	if fields["id"] != "u" || fields["level"] != "processes" || fields["title"] != `App\Billing` || fields["sources"] != "src/A.php" || fields["confidence"] != "0.80" || fields["model"] != "m:x" || fields["generated-at"] != "2026-01-02T03:04:05Z" || body != "\n# T\n" {
		t.Fatal(fields, body)
	}
	if f, b := SplitFrontMatter("no"); f != nil || b != "no" {
		t.Fatal("no front matter")
	}
	if f, _ := SplitFrontMatter("---\nunclosed"); f != nil {
		t.Fatal("unclosed")
	}
	if yamlString("") != `""` || yamlString("a: b") != `"a: b"` || yamlString("plain") != "plain" {
		t.Fatal("yaml")
	}
	ver, unver := true, false
	// Process: an invalid agent diagram is dropped, Tiegel's sequence
	// diagram is drawn instead; unverified steps are marked.
	pd := &ProcessDoc{Name: "Pay", Summary: "Bills.", Triggers: []string{"POST /pay"}, Inputs: []Data{{Name: "amount", Kind: "request"}},
		Steps:  []Step{{Ref: Ref{File: "src/A.php", Symbol: `App\A::pay`, Line: 1, Verified: &ver}, What: "pays", Data: "amount"}, {Ref: Ref{File: "src/B.php", Verified: &unver}, What: "x [y]"}},
		Writes: []Data{{Name: "invoices", Kind: "database", Ref: &Ref{File: "src/A.php"}}}, External: []Data{{Name: "Stripe", Kind: "http"}},
		Diagram: "sequenceDiagram\n  A->>", QA: QA{OpenQuestions: []Question{{Question: "retries?", Refs: []Ref{{File: "src/A.php"}}}}, Answered: []Answer{{ID: "q-1", Answer: "yes"}}}}
	out := RenderProcess(Item{Title: "Pay (HTTP)"}, pd, l)
	for _, want := range []string{"# Process: Pay", "**Triggers:** POST /pay", "## Inputs", "## Data flow", "```mermaid\nsequenceDiagram\n", "`App\\A::pay` – pays (data: amount)", "*(unverified)*", "## Writes", "## External systems", "## Open questions", "retries?", "**q-1**: yes", "(../questions.md)"} {
		if !strings.Contains(out, want) {
			t.Fatal(want, out)
		}
	}
	if strings.Contains(out, "A->>\n") {
		t.Fatal("invalid diagram written")
	}
	pd.Diagram = "sequenceDiagram\n    participant A\n    participant B\n    A->>B: hello\n"
	if out := RenderProcess(Item{}, pd, l); !strings.Contains(out, "A->>B: hello") {
		t.Fatal("valid agent diagram dropped", out)
	}
	// Diagrams that do not parse are left out entirely.
	if mermaidBlock("graph TD\n  A -->") != "" || mermaidBlock("C4Context\n  Person(a, \"A\")") != "" || mermaidBlock("") != "" {
		t.Fatal("invalid diagram kept")
	}
	if ValidMermaid(SequenceDiagram(&ProcessDoc{Steps: []Step{{Ref: Ref{File: "x.php"}, What: `quote " | ; # <b>`}}})) != nil {
		t.Fatal("generated sequence diagram does not parse")
	}
	// Overview: C4-style flowchart from containers and relations.
	od := &OverviewDoc{Summary: "Shop.", Architecture: "MVC", Containers: []Container{{Name: "Customer", Kind: "person"}, {Name: "Web \"app\"", Kind: "webapp", Tech: "Symfony"}, {Name: "DB", Kind: "database"},
		{Name: "Q", Kind: "queue"}, {Name: "Redis", Kind: "cache"}, {Name: "Stripe", Kind: "external"}},
		Relations: []Relation{{From: "Customer", To: "Web \"app\"", Label: "orders [x]"}, {From: "Web \"app\"", To: "DB"}, {From: "x", To: "DB"}}, Folders: []Folder{{Path: "src/", Role: "code"}}}
	diag := ContextDiagram("shop", od)
	if err := ValidMermaid(diag); err != nil {
		t.Fatal(err, diag)
	}
	f := Facts{Project: "shop", PHPRoot: "app", Framework: "symfony", Frameworks: []string{"symfony/framework-bundle ^7"}, PHP: ">=8.2", Style: "mvc", Packages: []string{"doctrine/orm ^3"},
		PSR4: map[string][]string{`App\`: {"app/src"}}, Folders: []FolderInfo{{Path: "src", PHPFiles: 3}}, Services: []string{"db (mysql:8)"}, DevTools: []string{"phpunit/phpunit ^11"},
		Tooling: []Tool{{Name: "PHPUnit", File: "phpunit.xml"}}, Scripts: []Script{{Name: "test", Command: "phpunit"}}}
	ov := RenderOverview("shop", od, f, l)
	for _, want := range []string{"# Overview: shop", "## Summary", "## Architecture", "```mermaid\nflowchart LR", "subgraph sys", "| Web \"app\" |", "## Tech stack", "symfony/framework-bundle ^7", "**PHP root:** app",
		"## Folders", "| src | 3 | code |", "## Development tooling", "db (mysql:8)", "`test`: `phpunit`"} {
		if !strings.Contains(ov, want) {
			t.Fatal(want, ov)
		}
	}
	if ov := RenderOverview("shop", nil, Facts{}, l); !strings.Contains(ov, "not available yet") || !strings.Contains(ov, "none detected") {
		t.Fatal(ov)
	}
	// Entry points: agent rows and the mechanical inventory.
	it := Item{Title: "HTTP routes", Entries: []EntryPoint{{Kind: KindHTTP, Name: "GET /a", Handler: "A::b", File: "src/A.php", Line: 2}}}
	ed := &EntryPointsDoc{Summary: "Orders come in.", Entries: []EntryDesc{{Name: "GET /a", Handler: Ref{File: "src/A.php", Symbol: "A::b"}, Purpose: "lists | orders", Process: "Orders", Found: true}, {Name: "POST /x", Purpose: "p"}}}
	ep := RenderEntryPoints(it, ed, l)
	if !strings.Contains(ep, "| GET /a |") || !strings.Contains(ep, `lists \| orders`) || !strings.Contains(ep, "*(found by the agent)*") || !strings.Contains(ep, "**Orders:** GET /a") {
		t.Fatal(ep)
	}
	if ep := RenderEntryPoints(it, nil, l); !strings.Contains(ep, "Found by Tiegel") || !strings.Contains(ep, "`A::b`") {
		t.Fatal(ep)
	}
	// Component.
	cd := &ComponentDoc{Name: `App\S\OrderService`, Summary: "Saves.", State: "orders", Collaborators: []string{"Db"},
		Methods: []Method{{Ref: Ref{File: "src/A.php", Symbol: `App\S\OrderService::save`, Line: 3}, Inputs: "array", Outputs: "Order", Reads: []string{"carts"}, Writes: []string{"orders"}, SideEffects: []string{"event"}, Throws: []string{"E"}}}}
	co := RenderComponent(Item{Group: `App\S\OrderService`, Files: []string{"src/A.php"}, UsedBy: []string{"Pay (HTTP)", "Other"}}, cd, map[string]string{"Pay (HTTP)": "processes/pay.md"}, l)
	for _, want := range []string{"# Component: OrderService", "### `OrderService::save`", "- **In:** array", "- **Writes:** orders", "## Used by", "[Pay (HTTP)](../processes/pay.md)", "- Other", "## Collaborators", "## State"} {
		if !strings.Contains(co, want) {
			t.Fatal(want, co)
		}
	}
	if ShortMethod("X") != "X" {
		t.Fatal("short")
	}
	if firstSentences("One. Two three four five six. Seven eight nine ten.", 30) != "One. Two three four five six." || firstSentences(strings.Repeat("x", 40), 10) != "xxxxxxxxxx…" {
		t.Fatal(firstSentences("One. Two three four five six. Seven eight nine ten.", 30))
	}
	if Snippet("## H\n```mermaid\ngraph TD\n```\nSome **bold** [link](x).", 0) != "Some bold link." || Snippet("abcdef", 4) != "abc…" {
		t.Fatal(Snippet("## H\n```mermaid\ngraph TD\n```\nSome **bold** [link](x).", 0))
	}
}

func TestIndexAndQuestions(t *testing.T) {
	dir := t.TempDir()
	m, _ := LoadManifest(dir)
	m.Items["overview"] = &ItemState{ID: "overview", Level: LevelOverview, Title: "Overview", MD: "overview.md", Summary: "Shop.", Confidence: 0.9, Status: StatusDone}
	m.Items["entrypoints-http"] = &ItemState{ID: "entrypoints-http", Level: LevelEntryPoints, Title: "HTTP routes", MD: "entrypoints/http.md", Mechanical: true}
	m.Items["process-a"] = &ItemState{ID: "process-a", Level: LevelProcesses, Title: "A|B", MD: "processes/a.md", Summary: strings.Repeat("long | ", 40), Confidence: 0.5}
	m.Items["component-x"] = &ItemState{ID: "component-x", Level: LevelComponents, Title: "X", MD: "components/x.md"}
	m.Items["gone"] = &ItemState{ID: "gone", Level: LevelComponents, Title: "Gone", MD: "components/gone.md"}
	write(t, dir, map[string]string{"overview.md": "x", "entrypoints/http.md": "x", "processes/a.md": "x", "components/x.md": "x"})
	qs := &QuestionStore{}
	now := time.Unix(100, 0)
	qs.Apply(Item{ID: "overview", Level: LevelOverview}, &QA{OpenQuestions: []Question{{Question: "Which DB?"}, {Question: " "}, {Question: "Where are mails sent?", Refs: []Ref{{File: "src/M.php"}}}}}, nil, now)
	if open, _ := qs.Counts(); open != 2 {
		t.Fatal(open)
	}
	id := QuestionID("overview", "which   DB?")
	if qs.Get(id) == nil || qs.Get("nope") != nil {
		t.Fatal("id not stable")
	}
	// A process sees the overview's general question and the one citing
	// its file; answering records the source.
	rel := qs.Relevant(Item{ID: "process-a", Level: LevelProcesses, Files: []string{"src/M.php"}}, 8)
	if len(rel) != 2 || rel[0].Text != "Where are mails sent?" {
		t.Fatal(rel)
	}
	if len(qs.Relevant(Item{ID: "process-a", Level: LevelProcesses}, 1)) != 1 || len(qs.Relevant(Item{ID: "o2", Level: LevelOverview}, 8)) != 0 {
		t.Fatal("relevance")
	}
	qs.Apply(Item{ID: "process-a", Level: LevelProcesses}, &QA{Answered: []Answer{{ID: id, Answer: "MySQL", Refs: []Ref{{File: "config/db.yaml"}}}, {ID: "q-unknown", Answer: "x"}}}, nil, now)
	if q := qs.Get(id); q.Status != QuestionAnswered || q.AnsweredBy != "process-a" || q.Answer != "MySQL" || q.AnsweredAt != now {
		t.Fatal(q)
	}
	// Rebuilding the overview without the mail question drops it; the
	// answered one stays.
	qs.Apply(Item{ID: "overview", Level: LevelOverview}, &QA{OpenQuestions: []Question{{Question: "Which cache?"}}}, nil, now)
	if open, answered := qs.Counts(); open != 1 || answered != 1 {
		t.Fatal(open, answered)
	}
	// Answer batches: attempts count, exhausted questions are left out.
	items := PlanQuestions(qs, 5, false)
	if len(items) != 1 || len(items[0].Questions) != 1 || items[0].Level != LevelQuestions {
		t.Fatal(items)
	}
	cache := QuestionID("overview", "Which cache?")
	qs.Apply(items[0], &QA{}, []string{cache}, now)
	qs.Apply(items[0], &QA{}, []string{cache}, now)
	if len(PlanQuestions(qs, 5, false)) != 0 || len(PlanQuestions(qs, 5, true)) != 1 || len(qs.Answerable()) != 0 {
		t.Fatal("attempts")
	}
	for i := 0; i < 20; i++ {
		qs.Apply(Item{ID: "c", Level: LevelComponents}, &QA{OpenQuestions: []Question{{Question: "q" + string(rune('a'+i)), Refs: []Ref{{File: "f.php"}}}}}, nil, now)
	}
	if b := PlanQuestions(qs, 2, false); len(b) != 1 || len(b[0].Questions) != 1 || b[0].Files[0] != "f.php" {
		t.Fatal(b) // only the last re-ask survives (each Apply supersedes)
	}
	if err := qs.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := WriteIndex(dir, "shop", m, qs, dir); err != nil {
		t.Fatal(err)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	s := string(idx)
	for _, want := range []string{"# Knowledge base: shop", "## 1. Overview", "[Overview](overview.md) (0.90) – Shop.", "## 2. Entry points", "(facts only)", "## 3. Processes", `[A\|B](processes/a.md)`, "…", "## 4. Components", "[questions.md](questions.md)"} {
		if !strings.Contains(s, strings.ReplaceAll(want, `A\|B`, "A|B")) && !strings.Contains(s, want) {
			t.Fatal(want, s)
		}
	}
	if strings.Contains(s, "Gone") {
		t.Fatal("page without file listed")
	}
	qmd, _ := os.ReadFile(filepath.Join(dir, "questions.md"))
	if !strings.Contains(string(qmd), "## Open (2)") || !strings.Contains(string(qmd), "## Answered (1)") || !strings.Contains(string(qmd), "**Answer:** MySQL (by [process-a](processes/a.md)") {
		t.Fatal(string(qmd))
	}
	qs2, err := LoadQuestions(dir)
	if err != nil || len(qs2.Questions) != len(qs.Questions) {
		t.Fatal(err)
	}
	write(t, dir, map[string]string{"questions.json": "{"})
	if _, err := LoadQuestions(dir); err == nil {
		t.Fatal("broken questions")
	}
	// Without pages index.md and questions.md go.
	empty := emptyManifest()
	if err := WriteIndex(dir, "shop", empty, nil, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); !os.IsNotExist(err) {
		t.Fatal("index.md kept")
	}
}

func TestChunkDoc(t *testing.T) {
	para := strings.Repeat("word ", 120) // ~150 tokens
	doc := Meta{ID: "u", Title: "T"}.FrontMatter() + "\n# Title\n\n## Overview\n\nShort.\n\n## Data flows\n\n### Checkout\n\n" + para + "\n\n### Refund\n\n" + para + "\n\n```php\n# not a heading\n```\n\n### Huge\n\n" +
		strings.Repeat(para+"\n\n", 8) + "\n## Entities\n\n- a\n\n## Entities\n\n- b\n"
	cs := ChunkDoc("u", LevelProcesses, "processes/u.md", []string{"a.php"}, doc, ChunkOptions{Target: 300})
	ids := map[string]Chunk{}
	for _, c := range cs {
		ids[c.ID] = c
		if c.Unit != "u" || c.Level != LevelProcesses || c.Hash == "" || c.Sources[0] != "a.php" {
			t.Fatal(c)
		}
		if strings.Contains(c.Text, "id: u") {
			t.Fatal("front matter in chunk")
		}
	}
	if _, ok := ids["processes/u.md#overview"]; !ok {
		t.Fatalf("%v", keys(ids))
	}
	if c, ok := ids["processes/u.md#data-flows"]; !ok || !strings.Contains(c.Text, "### Checkout") || !strings.Contains(c.Text, "### Refund") || c.Heading != "Title › Data flows" {
		t.Fatalf("merge: %v %q", keys(ids), c.Heading)
	}
	if _, ok := ids["processes/u.md#huge-2"]; !ok {
		t.Fatalf("split: %v", keys(ids))
	}
	if !strings.HasPrefix(ids["processes/u.md#huge-2"].Text, "Title › Data flows › Huge") {
		t.Fatal("continued chunk without context")
	}
	if _, ok := ids["processes/u.md#entities-1"]; !ok {
		t.Fatalf("duplicate anchors: %v", keys(ids))
	}
	for _, c := range cs {
		if EstimateTokens(c.Text) > 300*16/10+50 {
			t.Fatal("too long", c.ID, EstimateTokens(c.Text))
		}
	}
	if cs := ChunkDoc("u", "", "m", nil, "## A\n"+strings.Repeat("x", 5000), ChunkOptions{}); len(cs) == 0 {
		t.Fatal("no chunks")
	}
	if l, h := heading("#nospace"); l != 0 || h != "" {
		t.Fatal("not a heading")
	}
	if l, h := heading("## **Bold** `x` ##"); l != 2 || h != "Bold x" {
		t.Fatal(l, h)
	}
}

func keys(m map[string]Chunk) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// vectorFile returns the vector file chunks.json points to.
func vectorFile(t *testing.T, dir string) string {
	t.Helper()
	var meta storeMeta
	b, _ := os.ReadFile(filepath.Join(dir, "chunks.json"))
	_ = json.Unmarshal(b, &meta)
	if meta.Vectors == "" {
		return filepath.Join(dir, legacyVectors)
	}
	return filepath.Join(dir, meta.Vectors)
}

func TestFileStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	recs := []Record{
		{Chunk: Chunk{ID: "a", Text: "alpha"}, Vector: []float32{1, 0, 0}},
		{Chunk: Chunk{ID: "b", Text: "beta"}, Vector: []float32{0, 2, 0}},
		{Chunk: Chunk{ID: "c", Text: "gamma"}},
	}
	if err := s.Upsert(ctx, recs); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(ctx, []Record{{Chunk: Chunk{ID: "x"}, Vector: []float32{1}}}); err == nil {
		t.Fatal("dimension mismatch accepted")
	}
	hits, err := s.Search(ctx, []float32{0.1, 1, 0}, 1)
	if err != nil || len(hits) != 1 || hits[0].Chunk.ID != "b" || hits[0].Score < 0.99 {
		t.Fatal(hits, err)
	}
	if _, err := s.Search(ctx, []float32{1}, 1); err == nil {
		t.Fatal("query dimension")
	}
	// Persistence; old vector files are cleaned up (current + previous).
	s2, _ := OpenFileStore(dir)
	st, _ := s2.Stats(ctx)
	if st.Chunks != 3 || st.Embedded != 2 || st.Dim != 3 || st.Model != "m1" || st.Bytes == 0 || st.Recovered != "" {
		t.Fatal(st)
	}
	cs, emb, _ := s2.Chunks(ctx)
	if len(cs) != 3 || !emb["a"] || emb["c"] {
		t.Fatal(cs, emb)
	}
	if err := s2.Upsert(ctx, []Record{{Chunk: Chunk{ID: "a", Text: "alpha2"}, Vector: []float32{0, 0, 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete(ctx, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := filepath.Glob(filepath.Join(dir, "vectors*.bin")); len(ms) != 2 {
		t.Fatal("vector files", ms)
	}
	s3, _ := OpenFileStore(dir)
	hits, _ = s3.Search(ctx, []float32{0, 0, 1}, 5)
	if len(hits) != 1 || hits[0].Chunk.Text != "alpha2" {
		t.Fatal(hits)
	}
	// Another process (s) sees the writes of s2 without reopening.
	if cs, _, _ := s.Chunks(ctx); len(cs) != 2 {
		t.Fatal("no reload", len(cs))
	}
	// A new model drops vectors but keeps chunks.
	if err := s3.SetModel(ctx, "m2"); err != nil {
		t.Fatal(err)
	}
	if err := s3.SetModel(ctx, "m2"); err != nil {
		t.Fatal(err)
	}
	st, _ = s3.Stats(ctx)
	if st.Chunks != 2 || st.Embedded != 0 || st.Dim != 0 {
		t.Fatal(st)
	}
	if err := s3.Upsert(ctx, []Record{{Chunk: Chunk{ID: "a"}, Vector: []float32{1, 1}}}); err != nil {
		t.Fatal(err)
	}
	// Corruption: flipped byte in the vector file → recovered empty store.
	vb := vectorFile(t, dir)
	b, _ := os.ReadFile(vb)
	b[14] ^= 0xff
	_ = os.WriteFile(vb, b, 0o644)
	s4, _ := OpenFileStore(dir)
	st, _ = s4.Stats(ctx)
	if st.Recovered == "" || st.Chunks != 0 {
		t.Fatal(st)
	}
	// Broken chunks.json, mismatching rows, missing vectors, bad header,
	// an old store (vectors.bin) still loads.
	for name, mutate := range map[string]func(){
		"json": func() { _ = os.WriteFile(filepath.Join(dir, "chunks.json"), []byte("{"), 0o644) },
		"rows": func() {
			_ = os.WriteFile(filepath.Join(dir, "chunks.json"), []byte(`{"chunks":[{"id":"a"}],"rows":[]}`), 0o644)
		},
		"count": func() {
			_ = os.WriteFile(filepath.Join(dir, "chunks.json"), []byte(`{"dim":2,"chunks":[{"id":"a"},{"id":"b"}],"rows":[true,true]}`), 0o644)
		},
		"header": func() { _ = os.WriteFile(vectorFile(t, dir), []byte("nope"), 0o644) },
		"gone":   func() { _ = os.Remove(vectorFile(t, dir)) },
	} {
		ok, _ := OpenFileStore(dir)
		_ = ok.Upsert(ctx, []Record{{Chunk: Chunk{ID: "a"}, Vector: []float32{1, 1}}})
		mutate()
		s5, _ := OpenFileStore(dir)
		if st, _ := s5.Stats(ctx); st.Recovered == "" {
			t.Fatal(name, "not detected")
		}
	}
	old := t.TempDir()
	write(t, old, map[string]string{"chunks.json": `{"model":"m","dim":2,"chunks":[{"id":"a","text":"x"}],"rows":[true]}`})
	_ = os.WriteFile(filepath.Join(old, "vectors.bin"), encodeVectors(2, [][]float32{{1, 0}}), 0o644)
	if s6, _ := OpenFileStore(old); func() bool { st, _ := s6.Stats(ctx); return st.Embedded != 1 || st.Recovered != "" }() {
		t.Fatal("old store not loaded")
	}
	if err := s.Upsert(cancelled(), nil); err == nil {
		t.Fatal("cancelled ctx")
	}
	if err := s.Delete(cancelled(), nil); err == nil {
		t.Fatal("cancelled ctx")
	}
	if n := Normalize([]float32{0, 0}); n[0] != 0 {
		t.Fatal(n)
	}
	_ = s.Close()
}

// TestFileStoreReadersDuringWrites searches while another goroutine
// and another "process" (a second FileStore on the same directory)
// write: readers never fail and always see a consistent snapshot.
func TestFileStoreReadersDuringWrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	w, _ := OpenFileStore(dir)
	r, _ := OpenFileStore(dir)
	_ = w.Upsert(ctx, []Record{{Chunk: Chunk{ID: "0", Text: "order"}, Vector: []float32{1, 0}}})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, s := range []*FileStore{w, r} {
		wg.Add(1)
		go func(s *FileStore) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cs, emb, err := s.Chunks(ctx)
				if err != nil || len(cs) == 0 || len(emb) != len(cs) {
					t.Error("inconsistent", err, len(cs))
					return
				}
				if _, err := Search(ctx, s, "order", SearchOptions{Query: []float32{1, 0}}); err != nil {
					t.Error(err)
					return
				}
				if st, _ := s.Stats(ctx); st.Recovered != "" {
					t.Error("reader saw a broken store:", st.Recovered)
					return
				}
			}
		}(s)
	}
	for i := 1; i < 60; i++ {
		if err := w.Upsert(ctx, []Record{{Chunk: Chunk{ID: string(rune('a' + i%26)), Text: "order mail"}, Vector: []float32{float32(i), 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	cs, _, _ := r.Chunks(ctx)
	if len(cs) != 27 {
		t.Fatal(len(cs))
	}
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestHybridSearch(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenFileStore(t.TempDir())
	_ = s.Upsert(ctx, []Record{
		{Chunk: Chunk{ID: "orders", Heading: "Shop › Data flows › Checkout", Text: "The OrderService persists orders in the orders table.", Hash: "1"}, Vector: []float32{1, 0}},
		{Chunk: Chunk{ID: "mail", Heading: "Mail", Text: "Mails are sent through SMTP.", Hash: "2"}, Vector: []float32{0, 1}},
		{Chunk: Chunk{ID: "misc", Heading: "Misc", Text: "Nothing related here at all.", Hash: "3"}, Vector: []float32{-1, 0}},
	})
	res, err := Search(ctx, s, "order persistence", SearchOptions{})
	if err != nil || len(res) == 0 || res[0].Chunk.ID != "orders" {
		t.Fatal(res, err)
	}
	res, _ = Search(ctx, s, "smtp", SearchOptions{Query: []float32{1, 0.1}, K: 2})
	if len(res) != 2 || res[0].Chunk.ID != "orders" || res[1].Chunk.ID != "mail" || res[0].Vector < 0.9 || res[1].Keyword != 1 {
		t.Fatalf("%+v", res)
	}
	if res, _ := Search(ctx, s, "", SearchOptions{}); len(res) != 0 {
		t.Fatal(res)
	}
	// Pages written after the embedding: new ones and changed ones take
	// part keyword only (the old vector of a changed one is ignored).
	extra := []Chunk{{ID: "new", Heading: "Invoices", Text: "Invoices are written to the invoices table.", Hash: "9"}, {ID: "orders", Heading: "Shop", Text: "Carts only now.", Hash: "x"}, {ID: "mail", Text: "Mails are sent through SMTP.", Hash: "2"}}
	res, _ = Search(ctx, s, "invoices", SearchOptions{Query: []float32{1, 0}, Extra: extra})
	if len(res) == 0 || res[0].Chunk.ID != "new" || res[0].Vector != 0 {
		t.Fatalf("%+v", res)
	}
	for _, r := range res {
		if r.Chunk.ID == "orders" {
			t.Fatal("stale vector used", r)
		}
	}
	if toks := Tokenize("OrderService::placeOrder user_id HTTPClient"); strings.Join(toks, ",") != "orderservice,order,service,placeorder,place,order,user,id,httpclient,http,client" {
		t.Fatal(toks)
	}
	if len(BM25(nil, "x")) != 0 {
		t.Fatal("bm25 empty")
	}
}
