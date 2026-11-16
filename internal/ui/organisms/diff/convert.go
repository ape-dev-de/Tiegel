package diff

import (
	"time"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// The types below mirror the JSON results of the git API methods
// git.fileDiff, git.blame and git.fileHistory (see docs/headless.md), so a
// client can decode a response directly into them and convert it into
// the organisms' data. They carry no behavior besides the conversion.

// APISide is one side of a git.fileDiff result.
type APISide struct {
	Version string `json:"version"`
	Hash    string `json:"hash,omitempty"`
	Label   string `json:"label"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Binary  bool   `json:"binary,omitempty"`
	Text    string `json:"text"`
	Open    bool   `json:"open,omitempty"`
}

// APIRange is a byte range of a line.
type APIRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// APIInline are the changed ranges of one (1-based) line.
type APIInline struct {
	Line   int        `json:"line"`
	Ranges []APIRange `json:"ranges"`
}

// APIBlock is a change block of git.fileDiff (1-based lines, ends
// exclusive).
type APIBlock struct {
	Kind        string      `json:"kind"`
	LeftStart   int         `json:"leftStart"`
	LeftEnd     int         `json:"leftEnd"`
	RightStart  int         `json:"rightStart"`
	RightEnd    int         `json:"rightEnd"`
	LeftInline  []APIInline `json:"leftInline,omitempty"`
	RightInline []APIInline `json:"rightInline,omitempty"`
}

// APIFileDiff is the result of git.fileDiff.
type APIFileDiff struct {
	Path      string     `json:"path"`
	Left      APISide    `json:"left"`
	Right     APISide    `json:"right"`
	Blocks    []APIBlock `json:"blocks"`
	Identical bool       `json:"identical"`
}

// Content converts the result for DiffWindow.SetContent.
func (r *APIFileDiff) Content() Content {
	side := func(s APISide) Side {
		return Side{Label: s.Label, Path: s.Path, Text: s.Text, Missing: !s.Exists, Binary: s.Binary}
	}
	return Content{Left: side(r.Left), Right: side(r.Right), Blocks: r.BlockList()}
}

// BlockList converts the blocks for DiffWindow.SetBlocks.
func (r *APIFileDiff) BlockList() []Block {
	out := make([]Block, len(r.Blocks))
	inl := func(in []APIInline) []Inline {
		var o []Inline
		for _, l := range in {
			for _, rg := range l.Ranges {
				o = append(o, Inline{Line: l.Line - 1, Start: rg.Start, End: rg.End})
			}
		}
		return o
	}
	for i, b := range r.Blocks {
		out[i] = Block{Kind: KindOf(b.Kind), LeftStart: b.LeftStart - 1, LeftEnd: b.LeftEnd - 1,
			RightStart: b.RightStart - 1, RightEnd: b.RightEnd - 1,
			LeftInline: inl(b.LeftInline), RightInline: inl(b.RightInline)}
	}
	return out
}

// APIBlameLine is one line of git.blame.
type APIBlameLine struct {
	Line int    `json:"line"`
	Hash string `json:"hash"`
}

// APIBlameCommit is a commit of git.blame.
type APIBlameCommit struct {
	Hash        string    `json:"hash"`
	Author      string    `json:"author"`
	Date        time.Time `json:"date"`
	Summary     string    `json:"summary"`
	Uncommitted bool      `json:"uncommitted,omitempty"`
}

// APIBlame is the result of git.blame.
type APIBlame struct {
	Lines   []APIBlameLine   `json:"lines"`
	Commits []APIBlameCommit `json:"commits"`
}

// Apply fills g with the result.
func (r *APIBlame) Apply(g *BlameGutter) {
	cs := make([]BlameCommit, len(r.Commits))
	for i, c := range r.Commits {
		cs[i] = BlameCommit{Hash: c.Hash, Author: c.Author, Date: c.Date, Summary: c.Summary, Uncommitted: c.Uncommitted}
	}
	n := 0
	for _, l := range r.Lines {
		n = max(n, l.Line)
	}
	hashes := make([]string, n)
	for _, l := range r.Lines {
		if l.Line > 0 {
			hashes[l.Line-1] = l.Hash
		}
	}
	g.SetBlame(cs, hashes)
}

// APIHistoryEntry is a commit of git.fileHistory.
type APIHistoryEntry struct {
	Hash        string    `json:"hash"`
	Parents     []string  `json:"parents"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"authorEmail"`
	Date        time.Time `json:"date"`
	Subject     string    `json:"subject"`
	Refs        []string  `json:"refs,omitempty"`
	Path        string    `json:"path"`
	OldPath     string    `json:"oldPath,omitempty"`
	Status      string    `json:"status"`
}

// APIHistory is the result of git.fileHistory.
type APIHistory struct {
	Path    string            `json:"path"`
	Commits []APIHistoryEntry `json:"commits"`
	More    bool              `json:"more"`
}

// Entries converts the commits for FileHistoryView.SetHistory.
func (r *APIHistory) Entries() []gitcli.FileCommit {
	out := make([]gitcli.FileCommit, len(r.Commits))
	for i, c := range r.Commits {
		fc := gitcli.FileCommit{Commit: gitcli.Commit{Hash: c.Hash, Parents: c.Parents, AuthorName: c.Author,
			AuthorEmail: c.AuthorEmail, AuthorDate: c.Date, Subject: c.Subject, Refs: c.Refs}, Path: c.Path, OldPath: c.OldPath}
		if c.Status != "" {
			fc.Status = c.Status[0]
		}
		out[i] = fc
	}
	return out
}
