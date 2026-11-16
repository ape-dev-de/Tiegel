package diff

import (
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// Kind is the kind of a change block.
type Kind uint8

// Change block kinds.
const (
	Added    Kind = iota // lines exist only on the right
	Modified             // lines differ
	Deleted              // lines exist only on the left
)

// String returns "added", "modified" or "deleted".
func (k Kind) String() string {
	switch k {
	case Added:
		return "added"
	case Deleted:
		return "deleted"
	}
	return "modified"
}

// KindOf parses the kind names of git.fileDiff ("added", "deleted",
// "modified").
func KindOf(s string) Kind {
	switch s {
	case "added":
		return Added
	case "deleted":
		return Deleted
	}
	return Modified
}

func (k Kind) decoration() editorview.DecorationKind {
	switch k {
	case Added:
		return editorview.DecorationAdded
	case Deleted:
		return editorview.DecorationDeleted
	}
	return editorview.DecorationModified
}

// Inline is a changed byte range [Start, End) within line Line (0-based,
// without the terminator).
type Inline struct {
	Line, Start, End int
}

// Block is one change between the left and the right text. Lines are
// 0-based, ends exclusive; an added block has LeftStart == LeftEnd (the
// left line before which the right lines were inserted), a deleted block
// RightStart == RightEnd.
type Block struct {
	Kind                 Kind
	LeftStart, LeftEnd   int
	RightStart, RightEnd int
	// LeftInline and RightInline mark the changed words of modified
	// lines.
	LeftInline, RightInline []Inline
}

// Side is one version of the compared file.
type Side struct {
	// Label names the version, e.g. "HEAD (1a2b3c4d)", "Index",
	// "Working tree".
	Label string
	Path  string
	Text  string
	// Missing marks a side where the file does not exist (added or
	// deleted file); Text is empty then.
	Missing bool
	// Binary content is not shown.
	Binary bool
}

// Content is everything a DiffWindow shows: both versions and the change
// blocks between them.
type Content struct {
	Left, Right Side
	Blocks      []Block
}

// BlocksFromGit converts the blocks of gitcli.CompareTexts.
func BlocksFromGit(in []gitcli.DiffBlock) []Block {
	out := make([]Block, len(in))
	for i, b := range in {
		k := Modified
		switch b.Kind {
		case gitcli.BlockAdded:
			k = Added
		case gitcli.BlockDeleted:
			k = Deleted
		}
		out[i] = Block{Kind: k, LeftStart: b.LeftStart, LeftEnd: b.LeftEnd, RightStart: b.RightStart, RightEnd: b.RightEnd,
			LeftInline: inlineFromGit(b.LeftInline), RightInline: inlineFromGit(b.RightInline)}
	}
	return out
}

func inlineFromGit(in []gitcli.InlineRanges) []Inline {
	var out []Inline
	for _, l := range in {
		for _, r := range l.Ranges {
			out = append(out, Inline{Line: l.Line, Start: r.Start, End: r.End})
		}
	}
	return out
}

// Compute compares two texts with the same algorithm as the git.fileDiff
// API (gitcli.CompareTexts) – used to refresh the blocks locally while the
// right side is edited.
func Compute(left, right string, ignoreWhitespace bool) []Block {
	return BlocksFromGit(gitcli.CompareTexts([]byte(left), []byte(right),
		gitcli.TextDiffOptions{IgnoreWhitespace: ignoreWhitespace, Inline: true}))
}

// toGit converts blocks back for gitcli.MapLine.
func toGit(bs []Block) []gitcli.DiffBlock {
	out := make([]gitcli.DiffBlock, len(bs))
	for i, b := range bs {
		out[i] = gitcli.DiffBlock{LeftStart: b.LeftStart, LeftEnd: b.LeftEnd, RightStart: b.RightStart, RightEnd: b.RightEnd}
	}
	return out
}

// decorations returns the editor decorations of one side.
func decorations(bs []Block, left bool) []editorview.LineDecoration {
	out := make([]editorview.LineDecoration, 0, len(bs))
	for _, b := range bs {
		d := editorview.LineDecoration{Kind: b.Kind.decoration(), StartLine: b.RightStart, EndLine: b.RightEnd}
		in := b.RightInline
		if left {
			d.StartLine, d.EndLine, in = b.LeftStart, b.LeftEnd, b.LeftInline
		}
		for _, r := range in {
			d.Inline = append(d.Inline, editorview.InlineRange{Line: r.Line, Start: r.Start, End: r.End})
		}
		out = append(out, d)
	}
	return out
}
