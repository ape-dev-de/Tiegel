package git

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func (m *Module) registerHistory(p *api.Provider) {
	api.Add(p, "git.fileDiff", "Full left/right text of one file between two versions with change blocks and inline ranges", m.gitFileDiff)
	api.Add(p, "git.fileHistory", "Commits that changed a file (follows renames, paged)", m.gitFileHistory)
	api.Add(p, "git.blame", "Annotate every line of a file with the commit that last changed it", m.gitBlame)
	api.Add(p, "git.compareRevisions", "Files that differ between two revisions (or a revision and the work tree)", m.gitCompareRevisions)
}

// Version names accepted by git.fileDiff besides revisions.
const (
	VersionWorking = "working"
	VersionIndex   = "index"
	// VersionEmpty is a version without the file (e.g. the "parent" of a
	// root commit).
	VersionEmpty = "empty"
)

// FileDiffParams are the params of "git.fileDiff". Base and Target are
// "working" (work tree; an open document's buffer), "index", "empty" (no
// file) or a revision ("HEAD", a hash, a branch). Defaults: Base "HEAD",
// Target "working".
type FileDiffParams struct {
	Path string `json:"path"`
	// BasePath is the path on the base side when it differs (rename);
	// default Path.
	BasePath         string `json:"basePath,omitempty"`
	Base             string `json:"base,omitempty"`
	Target           string `json:"target,omitempty"`
	IgnoreWhitespace bool   `json:"ignoreWhitespace,omitempty"`
	// NoInline skips the word/character ranges.
	NoInline bool `json:"noInline,omitempty"`
}

// FileDiffSide is one side of a file diff.
type FileDiffSide struct {
	// Version is "working", "index" or the revision as requested.
	Version string `json:"version"`
	// Hash is the resolved commit of a revision.
	Hash string `json:"hash,omitempty"`
	// Label is a display title, e.g. "HEAD (1a2b3c4d)", "Index",
	// "Working tree".
	Label  string `json:"label"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	// Binary content is not returned (Text is empty, no blocks).
	Binary bool   `json:"binary,omitempty"`
	Text   string `json:"text"`
	// Open marks work tree content taken from an open document's buffer.
	Open bool `json:"open,omitempty"`
}

// ByteRange is a byte range [start, end) within a line (without its
// terminator).
type ByteRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// InlineRanges are the changed parts of one line (1-based).
type InlineRanges struct {
	Line   int         `json:"line"`
	Ranges []ByteRange `json:"ranges"`
}

// FileDiffBlock is one change block. Lines are 1-based, ends exclusive;
// an added block has leftStart == leftEnd (the left line before which
// the lines were inserted), a deleted block rightStart == rightEnd.
type FileDiffBlock struct {
	Kind        string         `json:"kind"`
	LeftStart   int            `json:"leftStart"`
	LeftEnd     int            `json:"leftEnd"`
	RightStart  int            `json:"rightStart"`
	RightEnd    int            `json:"rightEnd"`
	LeftInline  []InlineRanges `json:"leftInline,omitempty"`
	RightInline []InlineRanges `json:"rightInline,omitempty"`
}

// FileDiffResult is the result of "git.fileDiff".
type FileDiffResult struct {
	Path      string          `json:"path"`
	Left      FileDiffSide    `json:"left"`
	Right     FileDiffSide    `json:"right"`
	Blocks    []FileDiffBlock `json:"blocks"`
	Identical bool            `json:"identical"`
}

// loadSide reads one version of rel.
func (m *Module) loadSide(ctx context.Context, r *gitcli.Repo, version, apiPath, rel string) (FileDiffSide, error) {
	s := FileDiffSide{Version: version, Path: rel}
	var data []byte
	switch version {
	case VersionEmpty:
		s.Label = "Empty"
		return s, nil
	case VersionWorking:
		s.Label = "Working tree"
		text, open, err := m.app.ReadText(apiPath)
		if err == nil {
			data, s.Exists, s.Open = text, true, open
		}
	case VersionIndex:
		s.Label = "Index"
		b, ok, err := r.FileAt(ctx, "", rel)
		if err != nil {
			return s, gitErr(err)
		}
		data, s.Exists = b, ok
	default:
		hash, err := r.ResolveCommit(ctx, version)
		if err != nil {
			if version == "HEAD" && errors.Is(err, gitcli.ErrNotFound) {
				// Unborn branch: everything is new.
				s.Label = "HEAD"
				return s, nil
			}
			return s, gitErr(err)
		}
		s.Hash = hash
		s.Label = short(hash)
		if version != hash && !(len(version) >= 7 && len(version) < len(hash) && hash[:len(version)] == version) {
			s.Label = version + " (" + short(hash) + ")"
		}
		b, ok, err := r.FileAt(ctx, hash, rel)
		if err != nil {
			return s, gitErr(err)
		}
		data, s.Exists = b, ok
	}
	if !utf8.Valid(data) {
		s.Binary = true
		return s, nil
	}
	s.Text = string(data)
	return s, nil
}

func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

func (m *Module) gitFileDiff(ctx context.Context, p FileDiffParams) (FileDiffResult, error) {
	if p.Path == "" {
		return FileDiffResult{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	r, err := m.repo(ctx)
	if err != nil {
		return FileDiffResult{}, err
	}
	if p.Base == "" {
		p.Base = "HEAD"
	}
	if p.Target == "" {
		p.Target = VersionWorking
	}
	if p.BasePath == "" {
		p.BasePath = p.Path
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return FileDiffResult{}, err
	}
	baseRel, err := m.rel(r, p.BasePath)
	if err != nil {
		return FileDiffResult{}, err
	}
	res := FileDiffResult{Path: rel, Blocks: []FileDiffBlock{}}
	if res.Left, err = m.loadSide(ctx, r, p.Base, p.BasePath, baseRel); err != nil {
		return res, err
	}
	if res.Right, err = m.loadSide(ctx, r, p.Target, p.Path, rel); err != nil {
		return res, err
	}
	if !res.Left.Exists && !res.Right.Exists {
		return res, api.Errorf(api.CodeNotFound, "%s exists in neither %s nor %s", rel, p.Base, p.Target)
	}
	if res.Left.Binary || res.Right.Binary {
		return res, nil
	}
	blocks := gitcli.CompareTexts([]byte(res.Left.Text), []byte(res.Right.Text),
		gitcli.TextDiffOptions{IgnoreWhitespace: p.IgnoreWhitespace, Inline: !p.NoInline})
	for _, b := range blocks {
		res.Blocks = append(res.Blocks, FileDiffBlock{
			Kind: b.Kind.String(), LeftStart: b.LeftStart + 1, LeftEnd: b.LeftEnd + 1,
			RightStart: b.RightStart + 1, RightEnd: b.RightEnd + 1,
			LeftInline: toInline(b.LeftInline), RightInline: toInline(b.RightInline),
		})
	}
	res.Identical = len(res.Blocks) == 0 && res.Left.Exists == res.Right.Exists
	return res, nil
}

func toInline(in []gitcli.InlineRanges) []InlineRanges {
	if len(in) == 0 {
		return nil
	}
	out := make([]InlineRanges, len(in))
	for i, l := range in {
		rs := make([]ByteRange, len(l.Ranges))
		for j, r := range l.Ranges {
			rs[j] = ByteRange{Start: r.Start, End: r.End}
		}
		out[i] = InlineRanges{Line: l.Line + 1, Ranges: rs}
	}
	return out
}

// FileHistoryParams are the params of "git.fileHistory".
type FileHistoryParams struct {
	Path string `json:"path"`
	// Rev is the start revision (default HEAD).
	Rev  string `json:"rev,omitempty"`
	Skip int    `json:"skip,omitempty"`
	// Max is the page size (default 100).
	Max int `json:"max,omitempty"`
	// NoFollow stops at renames.
	NoFollow bool `json:"noFollow,omitempty"`
}

// FileHistoryEntry is a commit that changed the file. Path is the file's
// path in the commit, OldPath its path in the first parent after a rename
// or copy; Status is the change letter (A, M, D, R, C, T).
type FileHistoryEntry struct {
	GitCommit
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"`
}

// FileHistoryResult is the result of "git.fileHistory".
type FileHistoryResult struct {
	Path    string             `json:"path"`
	Commits []FileHistoryEntry `json:"commits"`
	// More is true when further pages exist (skip + len(commits)).
	More bool `json:"more"`
}

func (m *Module) gitFileHistory(ctx context.Context, p FileHistoryParams) (FileHistoryResult, error) {
	if p.Path == "" {
		return FileHistoryResult{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	r, err := m.repo(ctx)
	if err != nil {
		return FileHistoryResult{}, err
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return FileHistoryResult{}, err
	}
	if p.Max <= 0 {
		p.Max = 100
	}
	commits, err := r.FileHistory(ctx, rel, gitcli.FileHistoryOptions{Rev: p.Rev, Skip: p.Skip, Max: p.Max + 1, NoFollow: p.NoFollow})
	if err != nil {
		return FileHistoryResult{}, gitErr(err)
	}
	res := FileHistoryResult{Path: rel, Commits: []FileHistoryEntry{}}
	if len(commits) > p.Max {
		commits, res.More = commits[:p.Max], true
	}
	for _, c := range commits {
		parents := c.Parents
		if parents == nil {
			parents = []string{}
		}
		e := FileHistoryEntry{
			GitCommit: GitCommit{Hash: c.Hash, Parents: parents, Author: c.AuthorName, AuthorEmail: c.AuthorEmail,
				Date: c.AuthorDate, Subject: c.Subject, Refs: c.Refs},
			Path: c.Path, OldPath: c.OldPath,
		}
		if c.Status != 0 {
			e.Status = string(c.Status)
		}
		res.Commits = append(res.Commits, e)
	}
	return res, nil
}

// BlameParams are the params of "git.blame".
type BlameParams struct {
	Path string `json:"path"`
	// Rev blames the file at a revision; default is the current content
	// (an open document's buffer, otherwise the work tree file), whose
	// uncommitted lines carry gitcli.UncommittedHash.
	Rev              string `json:"rev,omitempty"`
	IgnoreWhitespace bool   `json:"ignoreWhitespace,omitempty"`
}

// BlameLine is the annotation of one line (1-based).
type BlameLine struct {
	Line     int    `json:"line"`
	Hash     string `json:"hash"`
	OrigLine int    `json:"origLine"`
}

// BlameCommit describes a commit referenced by blame lines.
type BlameCommit struct {
	Hash        string    `json:"hash"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"authorEmail,omitempty"`
	Date        time.Time `json:"date"`
	Summary     string    `json:"summary"`
	Path        string    `json:"path,omitempty"`
	Previous    string    `json:"previous,omitempty"`
	PrevPath    string    `json:"prevPath,omitempty"`
	Boundary    bool      `json:"boundary,omitempty"`
	Uncommitted bool      `json:"uncommitted,omitempty"`
}

// BlameResult is the result of "git.blame". Commits are sorted newest
// first.
type BlameResult struct {
	Path    string        `json:"path"`
	Rev     string        `json:"rev,omitempty"`
	Open    bool          `json:"open,omitempty"`
	Lines   []BlameLine   `json:"lines"`
	Commits []BlameCommit `json:"commits"`
}

func (m *Module) gitBlame(ctx context.Context, p BlameParams) (BlameResult, error) {
	if p.Path == "" {
		return BlameResult{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	r, err := m.repo(ctx)
	if err != nil {
		return BlameResult{}, err
	}
	rel, err := m.rel(r, p.Path)
	if err != nil {
		return BlameResult{}, err
	}
	opts := gitcli.BlameOptions{Rev: p.Rev, IgnoreWhitespace: p.IgnoreWhitespace}
	res := BlameResult{Path: rel, Rev: p.Rev, Lines: []BlameLine{}, Commits: []BlameCommit{}}
	if p.Rev == "" {
		text, open, err := m.app.ReadText(p.Path)
		if err != nil {
			return res, err
		}
		opts.Contents, res.Open = text, open
		if opts.Contents == nil {
			opts.Contents = []byte{}
		}
	}
	b, err := r.Blame(ctx, rel, opts)
	if err != nil {
		return res, gitErr(err)
	}
	for _, l := range b.Lines {
		res.Lines = append(res.Lines, BlameLine{Line: l.Line, Hash: l.Hash, OrigLine: l.OrigLine})
	}
	for _, c := range b.Commits {
		res.Commits = append(res.Commits, BlameCommit{
			Hash: c.Hash, Author: c.Author, AuthorEmail: c.AuthorMail, Date: c.AuthorTime, Summary: c.Summary,
			Path: c.Filename, Previous: c.Previous, PrevPath: c.PrevPath, Boundary: c.Boundary, Uncommitted: c.Uncommitted,
		})
	}
	sortCommits(res.Commits)
	return res, nil
}

// sortCommits orders newest first (uncommitted on top), then by hash.
func sortCommits(cs []BlameCommit) {
	less := func(a, b BlameCommit) bool {
		if a.Uncommitted != b.Uncommitted {
			return a.Uncommitted
		}
		if !a.Date.Equal(b.Date) {
			return a.Date.After(b.Date)
		}
		return a.Hash < b.Hash
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && less(cs[j], cs[j-1]); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

// CompareRevisionsParams are the params of "git.compareRevisions". Base
// defaults to HEAD; an empty Target compares with the work tree.
type CompareRevisionsParams struct {
	Base   string `json:"base,omitempty"`
	Target string `json:"target,omitempty"`
}

// ChangedFile is one file of "git.compareRevisions". Status is A, M, D,
// R (renamed), C (copied), T (type change) or U (unmerged).
type ChangedFile struct {
	Path       string `json:"path"`
	OldPath    string `json:"oldPath,omitempty"`
	Status     string `json:"status"`
	Similarity int    `json:"similarity,omitempty"`
}

// CompareRevisionsResult is the result of "git.compareRevisions".
type CompareRevisionsResult struct {
	Base   string        `json:"base"`
	Target string        `json:"target"`
	Files  []ChangedFile `json:"files"`
}

func (m *Module) gitCompareRevisions(ctx context.Context, p CompareRevisionsParams) (CompareRevisionsResult, error) {
	r, err := m.repo(ctx)
	if err != nil {
		return CompareRevisionsResult{}, err
	}
	if p.Base == "" {
		p.Base = "HEAD"
	}
	target := p.Target
	if target == VersionWorking {
		target = ""
	}
	files, err := r.CompareRevisions(ctx, p.Base, target)
	if err != nil {
		return CompareRevisionsResult{}, gitErr(err)
	}
	res := CompareRevisionsResult{Base: p.Base, Target: p.Target, Files: []ChangedFile{}}
	if res.Target == "" {
		res.Target = VersionWorking
	}
	for _, f := range files {
		res.Files = append(res.Files, ChangedFile{Path: f.Path, OldPath: f.OldPath, Status: string(f.Status), Similarity: f.Similarity})
	}
	return res, nil
}
