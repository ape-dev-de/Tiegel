package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	good := `Here you go:
` + "```json\n" + `{"summary":"Orders.","flows":[{"name":"Checkout","trigger":"POST /checkout","steps":[{"file":"src/A.php","symbol":"A::run","what":"saves"}]}],"entities":["Order",{"name":"Line","file":"src/L.php"}],"external":["MySQL"]}` + "\n```\n"
	d, err := ParseDescription(good)
	if err != nil || d.Summary != "Orders." || len(d.Flows) != 1 || d.Entities[0].Name != "Order" || d.Entities[1].File != "src/L.php" || d.External[0].Name != "MySQL" {
		t.Fatal(d, err)
	}
	// Unfenced object in text.
	if _, err := ParseDescription(`ok {"summary":"x","flows":[]} done`); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDescription("no json here"); !errors.Is(err, ErrNoJSON) {
		t.Fatal(err)
	}
	if _, err := ParseDescription("```json\n{\"summary\": \n```"); err == nil {
		t.Fatal("broken JSON accepted")
	}
	if _, err := ParseDescription(`{"summary":"","flows":[{"name":"","trigger":"","steps":[{"file":"","what":""}]}],"entities":[{"name":""}]}`); err == nil ||
		!strings.Contains(err.Error(), "summary is empty") || !strings.Contains(err.Error(), "steps[0].file") {
		t.Fatal(err)
	}
	if _, err := ParseDescription(`{"summary":5}`); err == nil {
		t.Fatal("wrong type accepted")
	}
	v, err := ParseValidation("```json\n{\"confidence\":0.9,\"issues\":[\"x\"],\"description\":{\"summary\":\"s\",\"flows\":[]}}\n```")
	if err != nil || v.Confidence != 0.9 {
		t.Fatal(v, err)
	}
	for _, bad := range []string{`{"confidence":2,"description":{"summary":"s"}}`, `{"confidence":0.5}`, `nothing`, `{"confidence":"x"}`} {
		if _, err := ParseValidation(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	// Many errors are capped.
	var flows []string
	for i := 0; i < 12; i++ {
		flows = append(flows, `{"name":"","trigger":"t","steps":[{"file":"f","what":"w"}]}`)
	}
	_, err = ParseDescription(`{"summary":"s","flows":[` + strings.Join(flows, ",") + `]}`)
	if err == nil || !strings.Contains(err.Error(), "more") {
		t.Fatal(err)
	}
}

func TestPlanUnits(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"composer.json": `{"autoload":{"psr-4":{"App\\":"src/","Lib\\":["lib"]}}}`})
	psr := ReadPSR4(root)
	if psr[`App\`][0] != "src" || psr[`Lib\`][0] != "lib" {
		t.Fatal(psr)
	}
	files := []string{"src/Kernel.php", "src/Billing/Invoice.php", "src/Billing/Pay.php", "src/Shop/Cart.php", "lib/X.php",
		"vendor/a/b.php", "tests/FooTest.php", "public/index.php", "README.md", "src/Shop/Big/A.php"}
	units := PlanUnits(files, PlanOptions{PSR4: psr})
	got := map[string]Unit{}
	for _, u := range units {
		got[u.ID] = u
	}
	if len(units) != 5 || got["src-billing"].Title != `App\Billing` || len(got["src-billing"].Files) != 2 ||
		got["src"].Title != "App" || got["lib"].Title != "Lib" || got["public"].Title != "public" || got["src-shop"].Title != `App\Shop` {
		t.Fatalf("%+v", units)
	}
	// Scope, explicit excluded folder and splitting.
	units = PlanUnits(files, PlanOptions{PSR4: psr, Scope: []string{"tests", "src/Shop"}, MaxFiles: 1})
	ids := []string{}
	for _, u := range units {
		ids = append(ids, u.ID)
	}
	if strings.Join(ids, ",") != "src-shop,src-shop-big,tests" {
		t.Fatal(ids)
	}
	for _, u := range units {
		if u.ID == "src-shop-big" && u.Title != `App\Shop\Big` {
			t.Fatal(u)
		}
	}
	if UnitID("") != "root" || UnitID("--") != "unit" || UnitID("src/Foo Bar") != "src-foo-bar" {
		t.Fatal("ids")
	}
	// No composer.json, root files.
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
	units = PlanUnits([]string{"index.php", "a/b.php"}, PlanOptions{PSR4: map[string][]string{`R\`: {"."}}})
	if len(units) != 2 || units[1].ID != "root" || units[1].Title != "R" || units[0].Title != `R\a` {
		t.Fatalf("%+v", units)
	}
}

func TestManifestStale(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	write(t, root, map[string]string{"a.php": "<?php a", "b.php": "<?php b"})
	u := Unit{ID: "u", Files: []string{"a.php", "b.php"}}
	h := HashFiles(root, &u)
	if u.Bytes != 14 || h["a.php"] == "" || HashFile(filepath.Join(root, "nope")) != "" {
		t.Fatal(u, h)
	}
	m, err := LoadManifest(dir)
	if err != nil || m.Stale(dir, u, h) != StaleNew {
		t.Fatal(err)
	}
	m.Units["u"] = &UnitState{ID: "u", Status: StatusFailed}
	if m.Stale(dir, u, h) != StaleFailed {
		t.Fatal("failed")
	}
	m.Units["u"] = &UnitState{ID: "u", Status: StatusDone}
	if m.Stale(dir, u, h) != StaleMissing {
		t.Fatal("no md")
	}
	m.Units["u"] = &UnitState{ID: "u", Status: StatusDone, MD: "units/u.md", Files: h}
	if m.Stale(dir, u, h) != StaleMissing {
		t.Fatal("md file missing")
	}
	write(t, dir, map[string]string{"units/u.md": "x"})
	if m.Stale(dir, u, h) != "" {
		t.Fatal("current")
	}
	write(t, root, map[string]string{"b.php": "<?php changed"})
	if m.Stale(dir, u, HashFiles(root, &u)) != StaleChanged {
		t.Fatal("changed")
	}
	u2 := Unit{ID: "u", Files: []string{"a.php"}}
	if m.Stale(dir, u2, HashFiles(root, &u2)) != StaleChanged {
		t.Fatal("file removed")
	}
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	m2, err := LoadManifest(dir)
	if err != nil || m2.Units["u"].MD != "units/u.md" || len(m2.Sorted()) != 1 {
		t.Fatal(m2, err)
	}
	write(t, dir, map[string]string{"manifest.json": "{broken"})
	if m3, err := LoadManifest(dir); err == nil || len(m3.Units) != 0 {
		t.Fatal("broken manifest")
	}
	write(t, dir, map[string]string{"manifest.json": "{}"})
	if m4, err := LoadManifest(dir); err != nil || m4.Units == nil {
		t.Fatal(err)
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
		"src/Uses.php":    "<?php\n// calls send somewhere\n",
		"src/Nothing.php": "<?php\n",
	})
	c := &Checker{Root: root, Lookup: func(sym string) []string {
		if strings.HasSuffix(sym, "send") {
			return []string{filepath.Join(root, "src/Mailer.php")}
		}
		return nil
	}}
	d := Description{Summary: "s", Flows: []Flow{{Name: "f", Trigger: "t", Steps: []Step{
		{File: "./src/Order.php", Symbol: `App\Order::place()`, Line: 99, What: "a"},
		{File: filepath.Join(root, "src/Order.php") + ":3", Symbol: "", What: "b"},
		{File: "src/Nothing.php", Symbol: "Mailer::send", What: "c"},
		{File: "src/Gone.php", Symbol: "X::y", What: "d"},
		{File: "src/Nothing.php", Symbol: "Order::cancel", What: "e"},
		{File: "../etc/passwd", What: "f"},
		{File: "src/Order.php", Symbol: "$total", What: "g"},
	}}}, Entities: []Entity{{Name: "Order", File: "src/Order.php"}, {Name: "Ghost", File: "src/Ghost.php"}},
		External: []External{{Name: "SMTP", File: "src/Mailer.php"}, {Name: "X", File: "nope.php"}}}
	un, notes := c.Verify(&d)
	s := d.Flows[0].Steps
	if !*s[0].Verified || s[0].Line != 4 || s[0].File != "src/Order.php" {
		t.Fatal(s[0])
	}
	if !*s[1].Verified || s[1].File != "src/Order.php" {
		t.Fatal(s[1])
	}
	if !*s[2].Verified || s[2].File != "src/Mailer.php" || s[2].Line != 3 {
		t.Fatal(s[2])
	}
	if *s[3].Verified || *s[4].Verified || *s[5].Verified || !*s[6].Verified || s[6].Line != 5 {
		t.Fatal(s[3:])
	}
	if un != 4 || len(notes) != 5 || d.Entities[1].File != "" || d.External[1].File != "" || d.External[0].File != "src/Mailer.php" {
		t.Fatal(un, notes, d.Entities, d.External)
	}
	for in, want := range map[string]string{`App\Foo::bar()`: "bar", `App\Foo`: "Foo", "$x": "x", "a->b": "b", "": ""} {
		if got := ShortSymbol(in); got != want {
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
	meta := Meta{Unit: "u", Title: `App\Billing`, Sources: []string{"src/A.php"}, Commit: "abc", GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Confidence: 0.8, Model: "m:x"}
	fm := meta.FrontMatter()
	fields, body := SplitFrontMatter(fm + "\n# T\n")
	if fields["unit"] != "u" || fields["title"] != `App\Billing` || fields["sources"] != "src/A.php" || fields["confidence"] != "0.80" || fields["model"] != "m:x" || fields["generated-at"] != "2026-01-02T03:04:05Z" || body != "\n# T\n" {
		t.Fatal(fields, body)
	}
	if f, b := SplitFrontMatter("no"); f != nil || b != "no" {
		t.Fatal("no front matter")
	}
	if f, _ := SplitFrontMatter("---\nunclosed"); f != nil {
		t.Fatal("unclosed")
	}
	u := Unit{ID: "u", Title: "Billing"}
	ver, unver := true, false
	d := Description{Summary: "Bills.", Flows: []Flow{{Name: "Pay", Trigger: "POST /pay", Data: "amount", SideEffects: []string{"DB"},
		Steps: []Step{{File: "src/A.php", Symbol: "A::pay", Line: 1, What: "pays", Verified: &ver}, {File: "src/B.php", What: "x", Verified: &unver}}}},
		Entities: []Entity{{Name: "Invoice", File: "src/A.php", Description: "bill"}}, External: []External{{Name: "Stripe", Kind: "http", Description: "pay", File: "src/A.php"}},
		OpenQuestions: []string{"retries?"}}
	out := RenderBody(u, d, l)
	for _, want := range []string{"# Billing", "## Overview", "Bills.", "### Pay", "**Trigger:** POST /pay", "*(unverified)*", "## Entities", "**Invoice**", "## External", "(http)", "## Open questions", "**Side effects:** DB", "**Data:** amount"} {
		if !strings.Contains(out, want) {
			t.Fatal(want, out)
		}
	}
	if !strings.Contains(RenderBody(u, Description{Summary: "s"}, l), "No data flows") {
		t.Fatal("empty")
	}
	if OverviewOf(out) != "Bills." || OverviewOf("x") != "" || OverviewOf("## Overview\n\n## Next") != "" {
		t.Fatal(OverviewOf(out))
	}
	// Agent Markdown: fence, chatter, title, relative links.
	ans := "Sure!\n```markdown\n# Whatever\n\n## Overview\nText.\n\n## Data flows\n1. [src/A.php:1](src/A.php#L1) and [gone](src/Gone.php)\n```\n"
	b, err := NormalizeBody(u, ans, l)
	if err != nil || !strings.HasPrefix(b, "# Billing\n\n## Overview") || !strings.Contains(b, "(file://") || !strings.Contains(b, "](src/Gone.php)") || strings.Contains(b, "Whatever") {
		t.Fatal(b, err)
	}
	b, err = NormalizeBody(u, "intro\n## Overview\nx\n## Data flows\ny", l)
	if err != nil || !strings.HasPrefix(b, "# Billing\n\n## Overview") {
		t.Fatal(b, err)
	}
	if _, err := NormalizeBody(u, "## Overview\nonly", l); err == nil || !strings.Contains(err.Error(), "Data flows") {
		t.Fatal(err)
	}
	if b, err := NormalizeBody(u, "---\nunit: x\n---\n## Overview\na\n## Data flows\nb", l); err != nil || strings.Contains(b, "unit: x") {
		t.Fatal(b, err)
	}
	idx := RenderIndex("shop", time.Unix(0, 0), []IndexEntry{{ID: "u", Title: "A|B", Summary: strings.Repeat("long | ", 40), MD: "units/u.md", Confidence: 0.5, Files: 2}})
	if !strings.Contains(idx, "# Knowledge base: shop") || !strings.Contains(idx, "(units/u.md)") || !strings.Contains(idx, `\|`) || !strings.Contains(idx, "…") {
		t.Fatal(idx)
	}
	if yamlString("") != `""` || yamlString("a: b") != `"a: b"` || yamlString("plain") != "plain" {
		t.Fatal("yaml")
	}
}

func TestChunkDoc(t *testing.T) {
	para := strings.Repeat("word ", 120) // ~150 tokens
	doc := Meta{Unit: "u", Title: "T"}.FrontMatter() + "\n# Title\n\n## Overview\n\nShort.\n\n## Data flows\n\n### Checkout\n\n" + para + "\n\n### Refund\n\n" + para + "\n\n```php\n# not a heading\n```\n\n### Huge\n\n" +
		strings.Repeat(para+"\n\n", 8) + "\n## Entities\n\n- a\n\n## Entities\n\n- b\n"
	cs := ChunkDoc("u", "units/u.md", []string{"a.php"}, doc, ChunkOptions{Target: 300})
	ids := map[string]Chunk{}
	for _, c := range cs {
		ids[c.ID] = c
		if c.Unit != "u" || c.Hash == "" || c.Sources[0] != "a.php" {
			t.Fatal(c)
		}
		if strings.Contains(c.Text, "unit: u") {
			t.Fatal("front matter in chunk")
		}
	}
	if _, ok := ids["units/u.md#overview"]; !ok {
		t.Fatalf("%v", keys(ids))
	}
	if c, ok := ids["units/u.md#data-flows"]; !ok || !strings.Contains(c.Text, "### Checkout") || !strings.Contains(c.Text, "### Refund") || c.Heading != "Title › Data flows" {
		t.Fatalf("merge: %v %q", keys(ids), c.Heading)
	}
	if _, ok := ids["units/u.md#huge-2"]; !ok {
		t.Fatalf("split: %v", keys(ids))
	}
	if !strings.HasPrefix(ids["units/u.md#huge-2"].Text, "Title › Data flows › Huge") {
		t.Fatal("continued chunk without context")
	}
	if _, ok := ids["units/u.md#entities-1"]; !ok {
		t.Fatalf("duplicate anchors: %v", keys(ids))
	}
	for _, c := range cs {
		if EstimateTokens(c.Text) > 300*16/10+50 {
			t.Fatal("too long", c.ID, EstimateTokens(c.Text))
		}
	}
	// Defaults, a single overlong line.
	if cs := ChunkDoc("u", "m", nil, "## A\n"+strings.Repeat("x", 5000), ChunkOptions{}); len(cs) == 0 {
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
	// Persistence.
	s2, _ := OpenFileStore(dir)
	st, _ := s2.Stats(ctx)
	if st.Chunks != 3 || st.Embedded != 2 || st.Dim != 3 || st.Model != "m1" || st.Bytes == 0 || st.Recovered != "" {
		t.Fatal(st)
	}
	cs, emb, _ := s2.Chunks(ctx)
	if len(cs) != 3 || !emb["a"] || emb["c"] {
		t.Fatal(cs, emb)
	}
	// Upsert replaces, delete removes.
	if err := s2.Upsert(ctx, []Record{{Chunk: Chunk{ID: "a", Text: "alpha2"}, Vector: []float32{0, 0, 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete(ctx, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	s3, _ := OpenFileStore(dir)
	hits, _ = s3.Search(ctx, []float32{0, 0, 1}, 5)
	if len(hits) != 1 || hits[0].Chunk.Text != "alpha2" {
		t.Fatal(hits)
	}
	// A new model drops vectors but keeps chunks.
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
	// Corruption: flipped byte in vectors.bin → recovered empty store.
	vb := filepath.Join(dir, "vectors.bin")
	b, _ := os.ReadFile(vb)
	b[14] ^= 0xff
	_ = os.WriteFile(vb, b, 0o644)
	s4, _ := OpenFileStore(dir)
	st, _ = s4.Stats(ctx)
	if st.Recovered == "" || st.Chunks != 0 {
		t.Fatal(st)
	}
	// Broken chunks.json, mismatching rows, missing vectors, bad header.
	for name, mutate := range map[string]func(){
		"json": func() { _ = os.WriteFile(filepath.Join(dir, "chunks.json"), []byte("{"), 0o644) },
		"rows": func() {
			_ = os.WriteFile(filepath.Join(dir, "chunks.json"), []byte(`{"chunks":[{"id":"a"}],"rows":[]}`), 0o644)
		},
		"count": func() {
			_ = os.WriteFile(filepath.Join(dir, "chunks.json"), []byte(`{"dim":2,"chunks":[{"id":"a"},{"id":"b"}],"rows":[true,true]}`), 0o644)
		},
		"header": func() { _ = os.WriteFile(vb, []byte("nope"), 0o644) },
	} {
		ok, _ := OpenFileStore(dir)
		_ = ok.Upsert(ctx, []Record{{Chunk: Chunk{ID: "a"}, Vector: []float32{1, 1}}})
		mutate()
		s5, _ := OpenFileStore(dir)
		if st, _ := s5.Stats(ctx); st.Recovered == "" {
			t.Fatal(name, "not detected")
		}
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

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestHybridSearch(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenFileStore(t.TempDir())
	_ = s.Upsert(ctx, []Record{
		{Chunk: Chunk{ID: "orders", Heading: "Shop › Data flows › Checkout", Text: "The OrderService persists orders in the orders table."}, Vector: []float32{1, 0}},
		{Chunk: Chunk{ID: "mail", Heading: "Mail", Text: "Mails are sent through SMTP."}, Vector: []float32{0, 1}},
		{Chunk: Chunk{ID: "misc", Heading: "Misc", Text: "Nothing related here at all."}, Vector: []float32{-1, 0}},
	})
	// Keyword only: camel case split finds "order".
	res, err := Search(ctx, s, "order persistence", SearchOptions{})
	if err != nil || len(res) == 0 || res[0].Chunk.ID != "orders" {
		t.Fatal(res, err)
	}
	// Vector wins over a weak keyword match.
	res, _ = Search(ctx, s, "smtp", SearchOptions{Query: []float32{1, 0.1}, K: 2})
	if len(res) != 2 || res[0].Chunk.ID != "orders" || res[1].Chunk.ID != "mail" || res[0].Vector < 0.9 || res[1].Keyword != 1 {
		t.Fatalf("%+v", res)
	}
	if res, _ := Search(ctx, s, "", SearchOptions{}); len(res) != 0 {
		t.Fatal(res)
	}
	if toks := Tokenize("OrderService::placeOrder user_id HTTPClient"); strings.Join(toks, ",") != "orderservice,order,service,placeorder,place,order,user,id,httpclient,http,client" {
		t.Fatal(toks)
	}
	if len(BM25(nil, "x")) != 0 {
		t.Fatal("bm25 empty")
	}
}

// fakeAgent scripts answers per step by looking at the prompt.
type fakeAgent struct {
	mu       sync.Mutex
	answers  map[string][]string // step → answers in order
	prompts  []string
	sessions int
	fail     error
}

func (f *fakeAgent) NewSession(context.Context) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.sessions++
	return &fakeSession{f: f}, nil
}

type fakeSession struct {
	f    *fakeAgent
	step string
}

func (s *fakeSession) Prompt(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.prompts = append(s.f.prompts, text)
	switch {
	case strings.Contains(text, "You document the data flows"):
		s.step = StepDescribe
	case strings.Contains(text, "You review a description"):
		s.step = StepValidate
	case strings.Contains(text, "Write the knowledge base page"):
		s.step = StepWrite
	}
	l := s.f.answers[s.step]
	if len(l) == 0 {
		return "", fmt.Errorf("no answer for %s", s.step)
	}
	a := l[0]
	if len(l) > 1 {
		s.f.answers[s.step] = l[1:]
	}
	return a, nil
}

func (s *fakeSession) Close() {}

func descJSON(file string) string {
	d := Description{Summary: "Handles orders.", Flows: []Flow{{Name: "Place order", Trigger: "POST /orders", Steps: []Step{{File: file, Symbol: "Order::place", What: "stores the order"}}, SideEffects: []string{"INSERT orders"}}},
		Entities: []Entity{{Name: "Order", File: file}}}
	b, _ := json.Marshal(d)
	return "```json\n" + string(b) + "\n```"
}

func TestPipeline(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	write(t, root, map[string]string{"src/Order.php": "<?php\nclass Order {\n  public function place() {}\n}\n", "src/Other.php": "<?php\n"})
	valid := func(file string, conf float64) string {
		var d Description
		b, _ := ExtractJSON(descJSON(file))
		_ = json.Unmarshal(b, &d)
		v, _ := json.Marshal(Validation{Confidence: conf, Issues: []string{"ok"}, Description: &d})
		return "```json\n" + string(v) + "\n```"
	}
	page := "## Overview\nHandles orders.\n\n## Data flows\n### Place order\n1. [src/Order.php:3](src/Order.php#L3) `Order::place` – stores\n"
	fa := &fakeAgent{answers: map[string][]string{
		StepDescribe: {"I think... no JSON", "```json\n{\"summary\": broken\n```", descJSON("src/Order.php")},
		StepValidate: {valid("src/Order.php", 0.9)},
		StepWrite:    {page},
	}}
	var events []Event
	var mu sync.Mutex
	p := &Pipeline{Root: root, Dir: dir, Project: "shop", Agent: fa, Commit: "abc", Model: "fake/large", Repairs: 2,
		Progress: func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() },
		Now:      func() time.Time { return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) }}
	m, _ := LoadManifest(dir)
	units := []Unit{{ID: "src", Title: "App", Dir: "src", Files: []string{"src/Order.php", "src/Other.php"}}}
	sum := p.Run(context.Background(), units, m)
	if sum.Done != 1 || sum.Failed != 0 {
		t.Fatal(sum)
	}
	st := m.Units["src"]
	if st.Status != StatusDone || st.Confidence != 0.9 || st.Commit != "abc" || len(st.Files) != 2 || st.Unverified != 0 {
		t.Fatalf("%+v", st)
	}
	b, err := os.ReadFile(filepath.Join(dir, "units", "src.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if !strings.HasPrefix(doc, "---\nunit: src\n") || !strings.Contains(doc, "commit: abc") || !strings.Contains(doc, "confidence: 0.90") || !strings.Contains(doc, "# App\n\n## Overview") || !strings.Contains(doc, "(file://") {
		t.Fatal(doc)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	if !strings.Contains(string(idx), "[App](units/src.md)") || !strings.Contains(string(idx), "Handles orders.") {
		t.Fatal(string(idx))
	}
	// Repairs were reported; the validator saw the mechanical notes.
	repairs := 0
	for _, e := range events {
		if e.Attempt > 0 {
			repairs++
		}
	}
	if repairs != 2 || fa.sessions != 3 {
		t.Fatal(repairs, fa.sessions)
	}
	if !strings.Contains(fa.prompts[3], "all referenced files and symbols were found") {
		t.Fatal(fa.prompts[3])
	}
	if m2, _ := LoadManifest(dir); m2.Units["src"] == nil {
		t.Fatal("manifest not saved")
	}

	// Second run: the writer fails twice → Tiegel renders itself; the
	// validator claims a wrong file → confidence drops.
	fa.answers = map[string][]string{
		StepDescribe: {descJSON("src/Order.php")},
		StepValidate: {valid("src/Gone.php", 1)},
		StepWrite:    {"no sections", "still none", "nope"},
	}
	sum = p.Run(context.Background(), units, m)
	st = m.Units["src"]
	if sum.Done != 1 || st.Unverified != 2 || st.Confidence >= 0.5 {
		t.Fatalf("%+v %+v", sum, st)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "units", "src.md"))
	if !strings.Contains(string(b), "*(unverified)*") {
		t.Fatal(string(b))
	}

	// Third run: describe never parses → failed, the old document stays.
	fa.answers = map[string][]string{StepDescribe: {"nope"}}
	sum = p.Run(context.Background(), units, m)
	if sum.Failed != 1 || m.Units["src"].Status != StatusFailed || m.Units["src"].MD == "" || !strings.Contains(sum.Errors[0], "describe") {
		t.Fatalf("%+v %+v", sum, m.Units["src"])
	}
	if m.Stale(dir, units[0], HashFiles(root, &units[0])) != StaleFailed {
		t.Fatal("failed unit not stale")
	}
	// A new unit failing has no document.
	fa.fail = errors.New("agent down")
	sum = p.Run(context.Background(), []Unit{{ID: "new", Title: "N", Dir: "n"}}, m)
	if sum.Failed != 1 || m.Units["new"].MD != "" {
		t.Fatal(sum)
	}
	// Cancelled before the start.
	sum = p.Run(cancelled(), units, m)
	if !sum.Cancelled || sum.Done+sum.Failed != 0 {
		t.Fatal(sum)
	}
	if c := confidence(0.9, Description{}, 0); c != 0.9 {
		t.Fatal(c)
	}
}

func TestWriteIndexEmpty(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"index.md": "old"})
	m, _ := LoadManifest(dir)
	m.Units["x"] = &UnitState{ID: "x", MD: "units/missing.md"}
	if err := WriteIndex(dir, "p", m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); !os.IsNotExist(err) {
		t.Fatal("index.md kept")
	}
}

func TestPrompts(t *testing.T) {
	var files []string
	for i := 0; i < maxListedFiles+5; i++ {
		files = append(files, fmt.Sprintf("src/F%d.php", i))
	}
	u := Unit{ID: "u", Title: "T", Dir: "src", Files: files}
	if p := DescribePrompt("p", u); !strings.Contains(p, "and 5 more files") || !strings.Contains(p, "Only read") || !strings.Contains(p, `"sideEffects"`) {
		t.Fatal(p)
	}
	var notes []string
	for i := 0; i < 35; i++ {
		notes = append(notes, "n")
	}
	if p := ValidatePrompt("p", u, Description{Summary: "s"}, notes); !strings.Contains(p, "… 5 more") || !strings.Contains(p, `"confidence"`) {
		t.Fatal(p)
	}
	if p := WritePrompt("p", u, Description{Summary: "s"}); !strings.Contains(p, "## Data flows") {
		t.Fatal(p)
	}
	if !strings.Contains(RepairPrompt(errors.New("x")), "json") || !strings.Contains(RepairMarkdownPrompt(errors.New("x")), "## Overview") {
		t.Fatal("repair")
	}
}
