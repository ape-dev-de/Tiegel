package markdown

import "testing"

func TestFrontMatterAsCode(t *testing.T) {
	d := Parse([]byte("---\nunit: x\ntitle: y\n---\n# Title\n\ntext\n"))
	if len(d.Blocks) < 2 || d.Blocks[0].Kind != CodeBlock || d.Blocks[0].Lang != "yaml" || d.Blocks[1].Kind != Heading || d.Blocks[1].Line != 5 {
		t.Fatalf("%+v", d.Blocks)
	}
	// At the end of the file, without a trailing newline.
	if d := Parse([]byte("---\na: b\n---")); len(d.Blocks) != 1 || d.Blocks[0].Kind != CodeBlock {
		t.Fatalf("%+v", d.Blocks)
	}
	// Not front matter: a rule, unclosed, code inside, no key.
	for _, src := range []string{"---\n\ntext\n", "---\na: b\n", "---\n```\na: b\n---\n", "---\nplain\n---\n"} {
		if d := Parse([]byte(src)); len(d.Blocks) > 0 && d.Blocks[0].Kind == CodeBlock && d.Blocks[0].Lang == "yaml" {
			t.Fatalf("%q became front matter", src)
		}
	}
}
