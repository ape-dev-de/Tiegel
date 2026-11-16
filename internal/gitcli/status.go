package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// EntryKind classifies a status entry.
type EntryKind int

// Status entry kinds (porcelain v2 record types).
const (
	KindOrdinary  EntryKind = iota // "1": changed tracked file
	KindRenamed                    // "2": renamed or copied
	KindUnmerged                   // "u": merge conflict
	KindUntracked                  // "?"
	KindIgnored                    // "!"
)

// String returns a short name of the kind.
func (k EntryKind) String() string {
	switch k {
	case KindOrdinary:
		return "ordinary"
	case KindRenamed:
		return "renamed"
	case KindUnmerged:
		return "unmerged"
	case KindUntracked:
		return "untracked"
	case KindIgnored:
		return "ignored"
	}
	return "unknown"
}

// FileStatus is one entry of `git status --porcelain=v2`.
type FileStatus struct {
	Kind EntryKind
	// Path is the current path (slash separated, relative to the root).
	Path string
	// OrigPath is the source path of a rename or copy.
	OrigPath string
	// Index and Worktree are the X and Y status letters
	// ('.', 'M', 'T', 'A', 'D', 'R', 'C', 'U'); for untracked entries both
	// are '?', for ignored entries '!'.
	Index, Worktree byte
	// Copied is set for copies (rename/copy entries with C).
	Copied bool
	// Score is the rename or copy similarity in percent.
	Score int
	// Submodule is the 4 character submodule state ("N..." for no submodule).
	Submodule string
	// Modes: HEAD, index, work tree (ordinary/renamed); for unmerged entries
	// stage 1, 2, 3 and work tree.
	Modes []string
	// Hashes: HEAD and index (ordinary/renamed); stage 1, 2, 3 (unmerged).
	Hashes []string
}

// IsConflict reports whether the entry is an unmerged path.
func (f *FileStatus) IsConflict() bool { return f.Kind == KindUnmerged }

// HasStaged reports whether the index differs from HEAD.
func (f *FileStatus) HasStaged() bool {
	return (f.Kind == KindOrdinary || f.Kind == KindRenamed) && f.Index != '.'
}

// HasUnstaged reports whether the work tree differs from the index
// (untracked files included).
func (f *FileStatus) HasUnstaged() bool {
	switch f.Kind {
	case KindOrdinary, KindRenamed:
		return f.Worktree != '.'
	case KindUntracked:
		return true
	}
	return false
}

// BranchStatus is the "# branch.*" header of porcelain v2.
type BranchStatus struct {
	OID         string // HEAD commit, "" when initial
	Initial     bool   // no commit yet
	Head        string // branch name, "" when detached
	Detached    bool
	Upstream    string // e.g. "origin/main", "" without upstream
	HasAheadBeh bool   // ahead/behind is known (upstream exists)
	Ahead       int
	Behind      int
}

// Status is the parsed result of [Repo.Status].
type Status struct {
	Branch BranchStatus
	Files  []FileStatus
}

// Conflicts returns the unmerged entries.
func (s *Status) Conflicts() []FileStatus {
	var out []FileStatus
	for _, f := range s.Files {
		if f.Kind == KindUnmerged {
			out = append(out, f)
		}
	}
	return out
}

// StatusOptions configures [Repo.Status].
type StatusOptions struct {
	// Ignored includes ignored files.
	Ignored bool
	// NoUntracked hides untracked files. By default every untracked file is
	// listed individually (--untracked-files=all).
	NoUntracked bool
	// Paths limits the status to these paths.
	Paths []string
}

// Status runs `git status --porcelain=v2 -z --branch`.
func (r *Repo) Status(ctx context.Context, opts StatusOptions) (*Status, error) {
	args := []string{"status", "--porcelain=v2", "-z", "--branch", "--find-renames", "--ignore-submodules=none"}
	if opts.NoUntracked {
		args = append(args, "--untracked-files=no")
	} else {
		args = append(args, "--untracked-files=all")
	}
	if opts.Ignored {
		args = append(args, "--ignored=matching")
	}
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		args = append(args, opts.Paths...)
	}
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("gitcli: status: %w", err)
	}
	return ParseStatus(out)
}

// ParseStatus parses `git status --porcelain=v2 -z [--branch]` output.
func ParseStatus(data []byte) (*Status, error) {
	st := &Status{}
	recs := bytes.Split(data, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := string(recs[i])
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			parseBranchHeader(&st.Branch, rec)
		case '1':
			f, err := parseOrdinary(rec, 8)
			if err != nil {
				return nil, err
			}
			st.Files = append(st.Files, f)
		case '2':
			f, err := parseOrdinary(rec, 9)
			if err != nil {
				return nil, err
			}
			if i+1 >= len(recs) {
				return nil, fmt.Errorf("gitcli: status: rename without source: %q", rec)
			}
			i++
			f.OrigPath = string(recs[i])
			st.Files = append(st.Files, f)
		case 'u':
			fields := strings.SplitN(rec, " ", 11)
			if len(fields) != 11 || len(fields[1]) != 2 {
				return nil, fmt.Errorf("gitcli: status: bad unmerged entry %q", rec)
			}
			st.Files = append(st.Files, FileStatus{
				Kind: KindUnmerged, Index: fields[1][0], Worktree: fields[1][1],
				Submodule: fields[2], Modes: fields[3:7], Hashes: fields[7:10], Path: fields[10],
			})
		case '?':
			st.Files = append(st.Files, FileStatus{Kind: KindUntracked, Path: strings.TrimPrefix(rec, "? "), Index: '?', Worktree: '?'})
		case '!':
			st.Files = append(st.Files, FileStatus{Kind: KindIgnored, Path: strings.TrimPrefix(rec, "! "), Index: '!', Worktree: '!'})
		default:
			return nil, fmt.Errorf("gitcli: status: unknown record %q", rec)
		}
	}
	return st, nil
}

// parseOrdinary parses "1 XY sub mH mI mW hH hI path" (n=8 fields before
// the path) and "2 XY sub mH mI mW hH hI Xscore path" (n=9).
func parseOrdinary(rec string, n int) (FileStatus, error) {
	fields := strings.SplitN(rec, " ", n+1)
	if len(fields) != n+1 || len(fields[1]) != 2 {
		return FileStatus{}, fmt.Errorf("gitcli: status: bad entry %q", rec)
	}
	f := FileStatus{
		Kind: KindOrdinary, Index: fields[1][0], Worktree: fields[1][1],
		Submodule: fields[2], Modes: fields[3:6], Hashes: fields[6:8], Path: fields[n],
	}
	if n == 9 {
		f.Kind = KindRenamed
		xs := fields[8]
		if xs == "" {
			return FileStatus{}, fmt.Errorf("gitcli: status: bad rename score %q", rec)
		}
		f.Copied = xs[0] == 'C'
		score, err := strconv.Atoi(xs[1:])
		if err != nil {
			return FileStatus{}, fmt.Errorf("gitcli: status: bad rename score %q: %w", rec, err)
		}
		f.Score = score
	}
	return f, nil
}

func parseBranchHeader(b *BranchStatus, rec string) {
	key, val, _ := strings.Cut(strings.TrimPrefix(rec, "# "), " ")
	switch key {
	case "branch.oid":
		if val == "(initial)" {
			b.Initial = true
		} else {
			b.OID = val
		}
	case "branch.head":
		if val == "(detached)" {
			b.Detached = true
		} else {
			b.Head = val
		}
	case "branch.upstream":
		b.Upstream = val
	case "branch.ab":
		var a, bh int
		if _, err := fmt.Sscanf(val, "+%d -%d", &a, &bh); err == nil {
			b.Ahead, b.Behind, b.HasAheadBeh = a, bh, true
		}
	}
}
