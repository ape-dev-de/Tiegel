package api

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// Kernel method "diff.texts": the change blocks between two texts that
// are no files (expected/actual of a failed test, D313). The result has
// the shape of git.fileDiff, so the diff window shows it like a file
// diff.

// DiffTextsParams are the params of "diff.texts".
type DiffTextsParams struct {
	Left             string `json:"left"`
	Right            string `json:"right"`
	LeftLabel        string `json:"leftLabel,omitempty"`
	RightLabel       string `json:"rightLabel,omitempty"`
	IgnoreWhitespace bool   `json:"ignoreWhitespace,omitempty"`
}

// DiffTextsSide is one side of the result (git.fileDiff field names).
type DiffTextsSide struct {
	Label  string `json:"label"`
	Text   string `json:"text"`
	Exists bool   `json:"exists"`
}

// DiffTextsRange is a changed byte range of a line.
type DiffTextsRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// DiffTextsInline are the changed ranges of one (1-based) line.
type DiffTextsInline struct {
	Line   int              `json:"line"`
	Ranges []DiffTextsRange `json:"ranges"`
}

// DiffTextsBlock is a change block (1-based lines, ends exclusive).
type DiffTextsBlock struct {
	Kind        string            `json:"kind"`
	LeftStart   int               `json:"leftStart"`
	LeftEnd     int               `json:"leftEnd"`
	RightStart  int               `json:"rightStart"`
	RightEnd    int               `json:"rightEnd"`
	LeftInline  []DiffTextsInline `json:"leftInline,omitempty"`
	RightInline []DiffTextsInline `json:"rightInline,omitempty"`
}

// DiffTextsResult is the result of "diff.texts".
type DiffTextsResult struct {
	Left      DiffTextsSide    `json:"left"`
	Right     DiffTextsSide    `json:"right"`
	Blocks    []DiffTextsBlock `json:"blocks"`
	Identical bool             `json:"identical"`
}

func (a *App) registerDiff() {
	mustHandle(a, "diff.texts", "Change blocks between two texts (e.g. expected and actual of a test), shaped like git.fileDiff", a.diffTexts)
}

func (a *App) diffTexts(_ context.Context, p DiffTextsParams) (DiffTextsResult, error) {
	res := DiffTextsResult{
		Left:   DiffTextsSide{Label: orText(p.LeftLabel, "Expected"), Text: p.Left, Exists: true},
		Right:  DiffTextsSide{Label: orText(p.RightLabel, "Actual"), Text: p.Right, Exists: true},
		Blocks: []DiffTextsBlock{},
	}
	inline := func(in []gitcli.InlineRanges) []DiffTextsInline {
		var out []DiffTextsInline
		for _, l := range in {
			rs := make([]DiffTextsRange, len(l.Ranges))
			for i, r := range l.Ranges {
				rs[i] = DiffTextsRange{Start: r.Start, End: r.End}
			}
			out = append(out, DiffTextsInline{Line: l.Line + 1, Ranges: rs})
		}
		return out
	}
	for _, b := range gitcli.CompareTexts([]byte(p.Left), []byte(p.Right), gitcli.TextDiffOptions{IgnoreWhitespace: p.IgnoreWhitespace, Inline: true}) {
		res.Blocks = append(res.Blocks, DiffTextsBlock{
			Kind: b.Kind.String(), LeftStart: b.LeftStart + 1, LeftEnd: b.LeftEnd + 1, RightStart: b.RightStart + 1, RightEnd: b.RightEnd + 1,
			LeftInline: inline(b.LeftInline), RightInline: inline(b.RightInline),
		})
	}
	res.Identical = len(res.Blocks) == 0
	return res, nil
}
