package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// UncommittedHash is the commit hash git blame reports for lines that are
// not committed yet.
const UncommittedHash = "0000000000000000000000000000000000000000"

// BlameCommit describes a commit that last changed some lines.
type BlameCommit struct {
	Hash        string
	Author      string
	AuthorMail  string
	AuthorTime  time.Time
	Committer   string
	CommitTime  time.Time
	Summary     string
	Filename    string // path of the file in this commit
	Previous    string // parent commit the lines came from ("" if none)
	PrevPath    string // path in Previous
	Boundary    bool   // boundary commit (first commit or range limit)
	Uncommitted bool   // the lines are not committed yet
}

// BlameLine is the annotation of one line of the blamed file.
type BlameLine struct {
	Hash string
	// Line is the 1-based line in the blamed version, OrigLine the 1-based
	// line in the commit Hash.
	Line, OrigLine int
}

// Blame is the result of [Repo.Blame]: one entry per line, plus the
// commits referenced by them.
type Blame struct {
	Lines   []BlameLine
	Commits map[string]*BlameCommit
}

// BlameOptions configure [Repo.Blame].
type BlameOptions struct {
	// Rev blames the file at this revision (default: the work tree file).
	Rev string
	// Contents, if non-nil, is blamed instead of the work tree file (e.g.
	// the unsaved buffer of an editor); not combinable with Rev.
	Contents []byte
	// IgnoreWhitespace ignores white space changes (-w).
	IgnoreWhitespace bool
}

// Blame annotates every line of path with the commit that last changed
// it (`git blame --porcelain`). It wraps [ErrNotFound] when the file is
// not tracked at the revision.
func (r *Repo) Blame(ctx context.Context, path string, opts BlameOptions) (*Blame, error) {
	if opts.Rev != "" && opts.Contents != nil {
		return nil, fmt.Errorf("gitcli: blame: Rev and Contents are exclusive")
	}
	args := []string{"blame", "--porcelain"}
	if opts.IgnoreWhitespace {
		args = append(args, "-w")
	}
	ro := runOpts{}
	if opts.Contents != nil {
		args = append(args, "--contents", "-")
		ro.stdin = opts.Contents
	}
	if opts.Rev != "" {
		args = append(args, opts.Rev)
	}
	args = append(args, "--", path)
	out, err := r.run(ctx, ro, args...)
	if err != nil {
		se := stderrOf(err)
		if isNotFound(se) || strings.Contains(se, "no such path") || strings.Contains(se, "no such ref") {
			return nil, fmt.Errorf("gitcli: blame %s: %w", path, ErrNotFound)
		}
		return nil, fmt.Errorf("gitcli: blame %s: %w", path, err)
	}
	return ParseBlame(out)
}

// ParseBlame parses `git blame --porcelain` output.
func ParseBlame(data []byte) (*Blame, error) {
	b := &Blame{Commits: map[string]*BlameCommit{}}
	var cur *BlameCommit
	var line BlameLine
	expectHeader := true
	for len(data) > 0 {
		var l []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			l, data = data[:i], data[i+1:]
		} else {
			l, data = data, nil
		}
		if expectHeader {
			if len(l) == 0 {
				continue
			}
			f := strings.Fields(string(l))
			if len(f) < 3 || len(f[0]) < 40 {
				return nil, fmt.Errorf("gitcli: blame: bad header %q", l)
			}
			orig, err1 := strconv.Atoi(f[1])
			final, err2 := strconv.Atoi(f[2])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("gitcli: blame: bad header %q", l)
			}
			line = BlameLine{Hash: f[0], OrigLine: orig, Line: final}
			cur = b.Commits[f[0]]
			if cur == nil {
				cur = &BlameCommit{Hash: f[0], Uncommitted: f[0] == UncommittedHash}
				b.Commits[f[0]] = cur
			}
			expectHeader = false
			continue
		}
		if len(l) > 0 && l[0] == '\t' {
			// Content line: ends the entry.
			b.Lines = append(b.Lines, line)
			expectHeader = true
			continue
		}
		key, val, _ := strings.Cut(string(l), " ")
		switch key {
		case "author":
			cur.Author = val
		case "author-mail":
			cur.AuthorMail = strings.Trim(val, "<>")
		case "author-time":
			cur.AuthorTime = unixTime(val)
		case "committer":
			cur.Committer = val
		case "committer-time":
			cur.CommitTime = unixTime(val)
		case "summary":
			cur.Summary = val
		case "filename":
			cur.Filename = val
		case "boundary":
			cur.Boundary = true
		case "previous":
			h, p, _ := strings.Cut(val, " ")
			cur.Previous, cur.PrevPath = h, p
		}
	}
	if !expectHeader {
		return nil, fmt.Errorf("gitcli: blame: truncated output")
	}
	return b, nil
}
