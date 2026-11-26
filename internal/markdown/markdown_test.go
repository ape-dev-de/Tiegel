package markdown

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = "# Title\n" + // 1
	"\n" +
	"Some *it* and **bold** `code` ~~gone~~ [link](a.md#b) https://x.org\n" + // 3
	"line2  \n" +
	"hard\n" +
	"\n" +
	"- [ ] todo\n" + // 7
	"- [x] done\n" +
	"  - nested\n" +
	"\n" +
	"3. three\n" + // 11
	"4. four\n" +
	"\n" +
	"> quote\n" + // 14
	"> more\n" +
	"\n" +
	"```php\n" + // 17
	"<?php echo 1;\n" +
	"```\n" +
	"\n" +
	"| a | b | c |\n" + // 21
	"|:-|-:|:-:|\n" +
	"| 1 | **2** | x |\n" +
	"\n" +
	"---\n" + // 25
	"\n" +
	"![alt *text*](img.png) <me@example.org>\n" + // 27
	"\n" +
	"<div>\n" + // 29
	"hi\n" +
	"</div>\n" +
	"\n" +
	"Title\n" + // 33
	"=====\n" +
	"\n" +
	"***\n" + // 36
	"\n" +
	"    indented code\n" + // 38
	"\n" +
	"Größe & Maße!\n" + // 40
	"------\n"

func TestParseBlocks(t *testing.T) {
	d := Parse([]byte(sample))
	type kl struct {
		k BlockKind
		l int
	}
	want := []kl{{Heading, 1}, {Paragraph, 3}, {List, 7}, {List, 11}, {Quote, 14}, {CodeBlock, 17}, {Table, 21},
		{Rule, 25}, {Paragraph, 27}, {HTML, 29}, {Heading, 33}, {Rule, 36}, {CodeBlock, 38}, {Heading, 40}}
	if len(d.Blocks) != len(want) {
		t.Fatalf("%d blocks: %+v", len(d.Blocks), d.Blocks)
	}
	for i, w := range want {
		if b := d.Blocks[i]; b.Kind != w.k || b.Line != w.l {
			t.Errorf("block %d = %s@%d, want %s@%d", i, b.Kind, b.Line, w.k, w.l)
		}
	}

	p := d.Blocks[1].Spans
	find := func(text string) Span {
		for _, s := range p {
			if s.Text == text {
				return s
			}
		}
		t.Fatalf("no span %q in %+v", text, p)
		return Span{}
	}
	if find("it").Style != Italic || find("bold").Style != Bold || find("code").Style != Code || find("gone").Style != Strike {
		t.Fatalf("styles %+v", p)
	}
	if find("link").Link != "a.md#b" || find("https://x.org").Link != "https://x.org" {
		t.Fatalf("links %+v", p)
	}
	if !strings.Contains(spansText(p), "line2 hard") {
		t.Fatalf("text %q", spansText(p))
	}
	brk := false
	for _, s := range p {
		brk = brk || s.Break
	}
	if !brk {
		t.Fatal("no hard break")
	}

	tasks := d.Blocks[2]
	if !tasks.Tight || len(tasks.Children) != 2 || !tasks.Children[0].Task || tasks.Children[0].Checked || !tasks.Children[1].Checked {
		t.Fatalf("tasks %+v", tasks)
	}
	if got := spansText(tasks.Children[0].Children[0].Spans); got != "todo" {
		t.Fatalf("task text %q", got)
	}
	if n := tasks.Children[1].Children; len(n) != 2 || n[1].Kind != List {
		t.Fatalf("nested %+v", n)
	}
	if ol := d.Blocks[3]; !ol.Ordered || ol.Start != 3 || len(ol.Children) != 2 {
		t.Fatalf("ordered %+v", ol)
	}
	if q := d.Blocks[4]; len(q.Children) != 1 || spansText(q.Children[0].Spans) != "quote more" {
		t.Fatalf("quote %+v", q)
	}
	if c := d.Blocks[5]; c.Lang != "php" || c.Code != "<?php echo 1;" {
		t.Fatalf("code %+v", c)
	}
	tb := d.Blocks[6]
	if len(tb.Rows) != 2 || len(tb.Align) != 3 || tb.Align[0] != AlignLeft || tb.Align[1] != AlignRight || tb.Align[2] != AlignCenter {
		t.Fatalf("table %+v", tb)
	}
	if s := tb.Rows[1][1].Spans; len(s) != 1 || s[0].Style != Bold || s[0].Text != "2" {
		t.Fatalf("cell %+v", s)
	}
	img := d.Blocks[8].Spans
	if !img[0].Image || img[0].Text != "alt text" || img[0].Link != "img.png" {
		t.Fatalf("image %+v", img)
	}
	if last := img[len(img)-1]; last.Link != "mailto:me@example.org" {
		t.Fatalf("email %+v", img)
	}
	if h := d.Blocks[9]; !strings.Contains(h.Code, "<div>") || !strings.Contains(h.Code, "</div>") {
		t.Fatalf("html %q", h.Code)
	}
	if c := d.Blocks[12]; c.Code != "indented code" || c.Lang != "" {
		t.Fatalf("indented %+v", c)
	}
}

func TestOutlineAndSlug(t *testing.T) {
	o := Parse([]byte(sample)).Outline()
	if len(o) != 3 || o[0].Anchor != "title" || o[1].Anchor != "title-1" || o[2].Anchor != "größe--maße" || o[2].Level != 2 || o[2].Line != 40 {
		t.Fatalf("outline %+v", o)
	}
	if s := Slug(" Hello, World_2-x "); s != "hello-world_2-x" {
		t.Fatal(s)
	}
	nested := Parse([]byte("> ## In quote\n")).Outline()
	if len(nested) != 1 || nested[0].Text != "In quote" {
		t.Fatalf("nested %+v", nested)
	}
}

func TestPlainText(t *testing.T) {
	out := Parse([]byte(sample)).PlainText(0)
	for _, w := range []string{
		"# Title\n", "link <a.md#b>", "https://x.org line2\nhard", "- [ ] todo\n- [x] done\n  - nested\n",
		"3. three\n4. four\n", "> quote more\n", "    <?php echo 1;\n", "| a | b | c |\n| --- | --- | --- |\n| 1 | 2 | x |\n",
		"----------\n", "[image: alt text] <img.png>", "me@example.org\n", "    <div>\n", "## Größe & Maße!\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
	wrapped := Parse([]byte("> " + strings.Repeat("word ", 30) + "\n")).PlainText(40)
	for _, l := range strings.Split(strings.TrimSpace(wrapped), "\n") {
		if len(l) > 40 || !strings.HasPrefix(l, "> ") {
			t.Fatalf("wrap %q", wrapped)
		}
	}
	loose := Parse([]byte("- a\n\n- b\n")).PlainText(0)
	if loose != "- a\n\n- b\n" {
		t.Fatalf("loose %q", loose)
	}
}

func TestParseEdgeCases(t *testing.T) {
	for _, src := range []string{"", "\n\n", "```\n", "| a |\n|-|\n", "- \n", "[x]: http://ref\n", "***", "<!-- c -->"} {
		d := Parse([]byte(src))
		if _, err := json.Marshal(d); err != nil {
			t.Fatal(err)
		}
		_ = d.PlainText(10)
		_ = d.Outline()
	}
	if d := Parse([]byte("---\n")); len(d.Blocks) != 1 || d.Blocks[0].Line != 1 {
		t.Fatalf("rule %+v", d.Blocks)
	}
	if d := Parse([]byte("# A\n---\n")); d.Blocks[1].Kind != Rule || d.Blocks[1].Line != 2 {
		t.Fatalf("rule after ATX %+v", d.Blocks)
	}
}

func TestSettingSpecs(t *testing.T) {
	if registerErr != nil {
		t.Fatal(registerErr)
	}
	if s := SettingSpecs(); len(s) != 1 || s[0].Key != KeyOpenMode || s[0].Default != OpenSplit {
		t.Fatalf("%+v", s)
	}
}
