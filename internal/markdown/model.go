// Package markdown parses Markdown (CommonMark with the GitHub extensions
// tables, strikethrough, task lists and autolinks) into a small,
// renderer-neutral document model. The GUI draws the model with Gio
// (internal/ui/organisms/mdview), headless clients get it as JSON (API
// md.render) or as plain text (Doc.PlainText). No HTML, no WebView, no
// network: images are only referenced, never fetched (D534).
//
// The package imports no Gio.
package markdown

// BlockKind is the kind of a block.
type BlockKind string

// Block kinds.
const (
	// Paragraph: Spans.
	Paragraph BlockKind = "paragraph"
	// Heading: Level 1–6, Spans, Anchor.
	Heading BlockKind = "heading"
	// CodeBlock: Lang (info string's first word, may be empty), Code
	// (without the trailing newline).
	CodeBlock BlockKind = "code"
	// Quote: Children.
	Quote BlockKind = "quote"
	// List: Ordered, Start (first number of an ordered list), Tight,
	// Children of kind ListItem.
	List BlockKind = "list"
	// ListItem: Children (blocks of the item); Task is set for a task list
	// item ("[ ]" / "[x]") with Checked.
	ListItem BlockKind = "item"
	// Table: Align (per column), Rows; the first row is the header.
	Table BlockKind = "table"
	// Rule: a thematic break.
	Rule BlockKind = "rule"
	// HTML: raw HTML block shown as source text (Code); never interpreted.
	HTML BlockKind = "html"
)

// Align is the alignment of a table column.
type Align string

// Column alignments.
const (
	AlignNone   Align = ""
	AlignLeft   Align = "left"
	AlignCenter Align = "center"
	AlignRight  Align = "right"
)

// Style flags of a span (combinable).
type Style uint8

// Span styles.
const (
	Bold Style = 1 << iota
	Italic
	Strike
	// Code is inline code (monospace).
	Code
)

// Span is a run of inline text with one style. Consecutive spans may
// share a style; a renderer wraps across span boundaries.
type Span struct {
	Text  string `json:"text"`
	Style Style  `json:"style,omitempty"`
	// Link is the destination when the span is (part of) a link: an URL,
	// a path relative to the document ("docs/a.md", "img.png") or an
	// anchor ("#heading-anchor", "other.md#anchor").
	Link string `json:"link,omitempty"`
	// Image is set for an image: Link is its source, Text the alt text.
	Image bool `json:"image,omitempty"`
	// Break is a hard line break after Text.
	Break bool `json:"break,omitempty"`
}

// Cell is a table cell.
type Cell struct {
	Spans []Span `json:"spans"`
}

// Block is a block of the document.
type Block struct {
	Kind BlockKind `json:"kind"`
	// Line is the 1-based source line the block starts at (scroll sync
	// between editor and preview).
	Line  int    `json:"line"`
	Level int    `json:"level,omitempty"`
	Spans []Span `json:"spans,omitempty"`
	// Anchor is the GitHub-style anchor of a heading ("getting-started",
	// duplicates get "-1", "-2", …).
	Anchor   string   `json:"anchor,omitempty"`
	Lang     string   `json:"lang,omitempty"`
	Code     string   `json:"code,omitempty"`
	Ordered  bool     `json:"ordered,omitempty"`
	Start    int      `json:"start,omitempty"`
	Tight    bool     `json:"tight,omitempty"`
	Task     bool     `json:"task,omitempty"`
	Checked  bool     `json:"checked,omitempty"`
	Align    []Align  `json:"align,omitempty"`
	Rows     [][]Cell `json:"rows,omitempty"`
	Children []Block  `json:"children,omitempty"`
}

// Doc is a parsed document.
type Doc struct {
	Blocks []Block `json:"blocks"`
}

// OutlineEntry is a heading of the document (table of contents).
type OutlineEntry struct {
	Level  int    `json:"level"`
	Text   string `json:"text"`
	Anchor string `json:"anchor"`
	Line   int    `json:"line"`
}
