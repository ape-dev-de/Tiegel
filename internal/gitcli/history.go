package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// FileCommit is one entry of [Repo.FileHistory]: a commit that changed the
// file, with the file's path in that commit.
type FileCommit struct {
	Commit
	// Path is the file's path in this commit, OldPath its path in the
	// first parent when the commit renamed or copied it ("" otherwise).
	Path, OldPath string
	// Status is the change letter of the file in this commit (A, M, D,
	// R, C, T).
	Status byte
}

// ParentPath returns the file's path in the first parent.
func (c *FileCommit) ParentPath() string {
	if c.OldPath != "" {
		return c.OldPath
	}
	return c.Path
}

// FileHistoryOptions configure [Repo.FileHistory].
type FileHistoryOptions struct {
	// Rev is the start revision (default HEAD).
	Rev string
	// Skip and Max implement paging; Max 0 means no limit.
	Skip, Max int
	// NoFollow disables following renames.
	NoFollow bool
}

const historyFormat = "%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%cn%x1f%ce%x1f%ct%x1f%D%x1f%s"

// FileHistory lists the commits that changed path, newest first,
// following renames (git log --follow). An unborn branch yields no
// commits.
func (r *Repo) FileHistory(ctx context.Context, path string, opts FileHistoryOptions) ([]FileCommit, error) {
	args := []string{"log", "--no-show-signature", "--no-color", "--decorate=short", "--name-status", "-z", "--format=" + historyFormat}
	skip, limit := opts.Skip, opts.Max
	if !opts.NoFollow {
		args = append(args, "--follow")
		// git applies --skip before following a rename, so a page behind
		// a rename would come back empty: skip locally instead.
		if limit > 0 {
			limit += skip
		}
		skip = 0
	}
	if skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(skip))
	}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	rev := opts.Rev
	if rev == "" {
		rev = "HEAD"
		if _, err := r.resolve(ctx, "HEAD"); errors.Is(err, ErrNotFound) {
			return nil, nil
		}
	}
	args = append(args, rev, "--", path)
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("gitcli: file history: %w", err)
	}
	res, err := parseFileHistory(out)
	if err != nil {
		return nil, err
	}
	if !opts.NoFollow && opts.Skip > 0 {
		if opts.Skip >= len(res) {
			return nil, nil
		}
		res = res[opts.Skip:]
	}
	return res, nil
}

func parseFileHistory(out []byte) ([]FileCommit, error) {
	var res []FileCommit
	for _, rec := range bytes.Split(out, []byte{0x1e}) {
		if len(bytes.TrimSpace(rec)) == 0 {
			continue
		}
		head, rest, _ := bytes.Cut(rec, []byte{0})
		f := strings.Split(string(head), "\x1f")
		if len(f) != 10 {
			return nil, fmt.Errorf("gitcli: file history: bad record %q", head)
		}
		c := FileCommit{Commit: Commit{
			Hash: f[0], Parents: strings.Fields(f[1]),
			AuthorName: f[2], AuthorEmail: f[3], AuthorDate: unixTime(f[4]),
			CommitterName: f[5], CommitterEmail: f[6], CommitDate: unixTime(f[7]),
			Subject: f[9],
		}}
		if f[8] != "" {
			c.Refs = strings.Split(f[8], ", ")
		}
		fields := strings.Split(strings.TrimLeft(string(rest), "\n"), "\x00")
		if len(fields) >= 2 && fields[0] != "" {
			c.Status = fields[0][0]
			c.Path = fields[1]
			if (c.Status == 'R' || c.Status == 'C') && len(fields) >= 3 {
				c.OldPath, c.Path = fields[1], fields[2]
			}
		}
		res = append(res, c)
	}
	return res, nil
}

// ChangedFile is one entry of [Repo.CompareRevisions].
type ChangedFile struct {
	// Status is the change letter: A, M, D, R (renamed), C (copied), T
	// (type change), U (unmerged).
	Status byte
	// Similarity is the rename/copy score in percent (R and C only).
	Similarity int
	Path       string
	OldPath    string // source of a rename or copy
}

// CompareRevisions lists the files that differ between base and target
// (git diff --name-status -M). An empty target compares base with the
// work tree; an empty base means HEAD.
func (r *Repo) CompareRevisions(ctx context.Context, base, target string) ([]ChangedFile, error) {
	if base == "" {
		base = "HEAD"
	}
	args := []string{"diff", "--no-color", "--no-ext-diff", "--name-status", "-z", "-M", base}
	if target != "" {
		args = append(args, target)
	}
	args = append(args, "--")
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		if isNotFound(stderrOf(err)) {
			return nil, fmt.Errorf("gitcli: compare %s..%s: %w", base, target, ErrNotFound)
		}
		return nil, fmt.Errorf("gitcli: compare %s..%s: %w", base, target, err)
	}
	return parseNameStatus(out)
}

func parseNameStatus(out []byte) ([]ChangedFile, error) {
	f := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var res []ChangedFile
	for i := 0; i < len(f); i++ {
		if f[i] == "" {
			continue
		}
		cf := ChangedFile{Status: f[i][0]}
		if cf.Status == 'R' || cf.Status == 'C' {
			cf.Similarity, _ = strconv.Atoi(f[i][1:])
			if i+2 >= len(f) {
				return nil, fmt.Errorf("gitcli: name-status: truncated record")
			}
			cf.OldPath, cf.Path = f[i+1], f[i+2]
			i += 2
		} else {
			if i+1 >= len(f) {
				return nil, fmt.Errorf("gitcli: name-status: truncated record")
			}
			cf.Path = f[i+1]
			i++
		}
		res = append(res, cf)
	}
	return res, nil
}

// FileAt returns the content of path at revision rev, or in the index
// when rev is "" – with the work tree filters applied (line endings,
// smudge filters), so it compares byte-exactly with the work tree file.
// ok is false when the path does not exist there; an unknown revision
// wraps [ErrNotFound].
func (r *Repo) FileAt(ctx context.Context, rev, path string) (content []byte, ok bool, err error) {
	if rev == "" {
		return r.BaseContent(ctx, path, BaseIndex)
	}
	if _, err := r.resolve(ctx, rev); err != nil {
		return nil, false, err
	}
	obj := rev + ":" + path
	out, err := r.run(ctx, runOpts{}, "cat-file", "--filters", obj)
	if err != nil {
		if _, verr := r.run(ctx, runOpts{}, "cat-file", "-e", obj); verr != nil {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("gitcli: file %s: %w", obj, err)
	}
	return out, true, nil
}
