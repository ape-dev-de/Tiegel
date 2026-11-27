package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/markdown"
)

// Chunk is a searchable piece of a knowledge document: one section (or
// a part of a long one) with its heading path.
type Chunk struct {
	// ID is "<md>#<anchor>[-n]", unique in the store.
	ID string `json:"id"`
	// Unit is the page's item ID, Level its level, MD the page relative
	// to the knowledge directory.
	Unit  string `json:"unit"`
	Level string `json:"level,omitempty"`
	MD    string `json:"md"`
	// Heading is the heading path ("App\Billing › Data flows › Checkout"),
	// Anchor the anchor of the section's heading.
	Heading string `json:"heading"`
	Anchor  string `json:"anchor,omitempty"`
	// Sources are the unit's source files.
	Sources []string `json:"sources,omitempty"`
	Text    string   `json:"text"`
	// Hash identifies the text (re-embed when it changes).
	Hash string `json:"hash"`
}

// EstimateTokens estimates the tokens of a text (about four characters
// per token for English text and code).
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

// ChunkOptions configure ChunkDoc.
type ChunkOptions struct {
	// Target is the wanted chunk size in tokens (default 500); chunks
	// stay between Target*0.6 and Target*1.6 where sections allow.
	Target int
}

type section struct {
	level  int
	path   []string
	anchor string
	text   strings.Builder
}

// ChunkDoc splits a page (with front matter) into chunks by
// heading: sections of level 2 and 3 are chunks; small neighbours below
// the same level-2 heading are merged, long sections are split at
// paragraphs.
func ChunkDoc(unit, level, md string, sources []string, doc string, o ChunkOptions) []Chunk {
	if o.Target <= 0 {
		o.Target = 500
	}
	lo, hi := o.Target*6/10, o.Target*16/10
	_, body := SplitFrontMatter(doc)
	var secs []*section
	cur := &section{}
	secs = append(secs, cur)
	path := []string{}
	anchors := map[string]int{}
	fence := false
	for _, line := range strings.SplitAfter(body, "\n") {
		t := strings.TrimRight(line, "\n")
		if strings.HasPrefix(strings.TrimSpace(t), "```") {
			fence = !fence
		}
		if lvl, title := heading(t); lvl > 0 && !fence {
			for len(path) >= lvl {
				path = path[:len(path)-1]
			}
			for len(path) < lvl-1 {
				path = append(path, "")
			}
			path = append(path, title)
			a := markdown.Slug(title)
			if n := anchors[a]; n > 0 {
				anchors[a] = n + 1
				a += "-" + strconv.Itoa(n)
			} else {
				anchors[a] = 1
			}
			if lvl <= 3 {
				cur = &section{level: lvl, path: append([]string(nil), path...), anchor: a}
				secs = append(secs, cur)
			}
		}
		cur.text.WriteString(line)
	}
	// Merge small sections into their predecessor below the same h2.
	var merged []*section
	for _, s := range secs {
		txt := strings.TrimSpace(s.text.String())
		if txt == "" {
			continue
		}
		if n := len(merged); n > 0 {
			p := merged[n-1]
			pt := EstimateTokens(p.text.String())
			if s.level == 3 && p.level >= 2 && sameH2(p.path, s.path) && pt < lo && pt+EstimateTokens(txt) <= hi {
				p.text.WriteString("\n" + s.text.String())
				continue
			}
			if p.level <= 1 && pt < lo/2 && s.level == 2 {
				// The title and a short intro go with the first section.
				s2 := &section{level: s.level, path: s.path, anchor: s.anchor}
				s2.text.WriteString(p.text.String() + "\n" + s.text.String())
				merged[n-1] = s2
				continue
			}
		}
		merged = append(merged, s)
	}
	var out []Chunk
	for _, s := range merged {
		head := strings.Join(nonEmpty(s.path), " › ")
		parts := splitLong(strings.TrimSpace(s.text.String()), hi)
		for i, p := range parts {
			id := md + "#" + s.anchor
			if i > 0 {
				id += "-" + strconv.Itoa(i+1)
			}
			text := p
			if i > 0 && head != "" {
				text = head + "\n\n" + p // keep the context of a continued section
			}
			sum := sha256.Sum256([]byte(text))
			out = append(out, Chunk{ID: id, Unit: unit, Level: level, MD: md, Heading: head, Anchor: s.anchor, Sources: sources, Text: text, Hash: hex.EncodeToString(sum[:8])})
		}
	}
	return out
}

func sameH2(a, b []string) bool {
	return len(a) >= 2 && len(b) >= 2 && a[1] == b[1] && a[0] == b[0]
}

func nonEmpty(l []string) []string {
	var out []string
	for _, s := range l {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// heading returns the level and text of an ATX heading line.
func heading(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n < len(line) && line[n] != ' ' {
		return 0, ""
	}
	t := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[n:]), "#"))
	return n, strings.NewReplacer("**", "", "`", "").Replace(t)
}

// splitLong splits text at blank lines (or lines) into parts of at most
// max tokens.
func splitLong(text string, max int) []string {
	if EstimateTokens(text) <= max {
		return []string{text}
	}
	paras := strings.Split(text, "\n\n")
	var out []string
	var b strings.Builder
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for _, p := range paras {
		if EstimateTokens(p) > max {
			flush()
			for _, l := range strings.Split(p, "\n") {
				if EstimateTokens(b.String()+l) > max {
					flush()
				}
				b.WriteString(l + "\n")
			}
			flush()
			continue
		}
		if EstimateTokens(b.String()+p) > max {
			flush()
		}
		b.WriteString(p + "\n\n")
	}
	flush()
	return out
}
