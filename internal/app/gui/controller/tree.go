package controller

import (
	"context"
	"path"
	"sort"
	"strings"
)

// Node is a node of the project tree. Directories are loaded lazily, one
// level per files.list call.
type Node struct {
	Name    string
	Path    string // project relative, slash separated; "" is the root
	Dir     bool
	Ignored bool
	Library bool
	// Loaded is true once the children of a directory are known.
	Loaded   bool
	Loading  bool
	Children []*Node
}

// Tree is the lazily loaded project tree.
type Tree struct {
	Root *Node
	// Version changes whenever the tree changes.
	Version uint64
	byPath  map[string]*Node
}

// NewTree returns a tree with an unloaded root.
func NewTree(name string) *Tree {
	root := &Node{Name: name, Dir: true}
	return &Tree{Root: root, byPath: map[string]*Node{"": root}}
}

// Node returns the node at path.
func (t *Tree) Node(p string) *Node { return t.byPath[p] }

// merge replaces the children of the directory at fn.Path with those of fn
// (one level; deeper levels of fn are merged recursively if listed).
func (t *Tree) merge(fn *FileNode) {
	n := t.byPath[fn.Path]
	if n == nil {
		return
	}
	n.Loading = false
	if fn.Collapsed {
		return
	}
	old := map[string]*Node{}
	for _, ch := range n.Children {
		old[ch.Path] = ch
	}
	n.Children = n.Children[:0]
	for _, ch := range fn.Children {
		cn := old[ch.Path]
		if cn == nil {
			cn = &Node{Path: ch.Path}
		}
		cn.Name, cn.Dir, cn.Ignored, cn.Library = ch.Name, ch.Dir, ch.Ignored, ch.Library
		n.Children = append(n.Children, cn)
		t.byPath[ch.Path] = cn
		if ch.Dir && !ch.Collapsed && ch.Children != nil {
			t.merge(ch)
		}
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	n.Loaded = true
	t.Version++
}

// LoadDir lists the directory rel (one level) and merges it into the tree.
// Library directories (vendor/) are listed on explicit expansion.
func (c *Controller) LoadDir(rel string) {
	if c.Tree == nil {
		return
	}
	n := c.Tree.Node(rel)
	if n == nil || n.Loading {
		return
	}
	n.Loading = true
	lib := n.Library
	c.Go(func(ctx context.Context) func() {
		var res filesListResult
		err := c.call(ctx, "files.list", map[string]any{"path": rel, "depth": 1, "libraries": lib}, &res)
		return func() {
			if c.Tree == nil {
				return
			}
			if err != nil {
				n.Loading = false
				c.notice(err)
				return
			}
			if res.Tree != nil {
				res.Tree.Path = rel
				res.Tree.Collapsed = false
				c.Tree.merge(res.Tree)
			}
		}
	})
}

// LoadAncestors loads the directories containing the project-relative
// path p that the tree knows but has not listed yet (a file revealed in
// the tree expands its parents, which must then be loaded). Deeper levels
// appear once their parent is merged, so callers repeat it whenever
// Tree.Version changes until everything is loaded.
func (c *Controller) LoadAncestors(p string) {
	if c.Tree == nil || p == "" {
		return
	}
	var dirs []string
	for d := path.Dir(p); d != "." && d != "/" && d != ""; d = path.Dir(d) {
		dirs = append(dirs, d)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		n := c.Tree.Node(dirs[i])
		if n == nil {
			return // its parent is still loading
		}
		if n.Dir && !n.Loaded {
			c.LoadDir(dirs[i])
			return
		}
	}
}

// GitState returns the git state of a project-relative path: the letter
// state of a file, "M" for directories containing changes, "" if clean.
func (c *Controller) GitState(rel string) string {
	if f, ok := c.Git[rel]; ok {
		switch {
		case f.Conflict || f.Kind == "unmerged":
			return "U"
		case f.Kind == "untracked":
			return "?"
		case f.Kind == "ignored":
			return "!"
		case f.Index == "A" || f.Worktree == "A":
			return "A"
		case f.Index == "D" || f.Worktree == "D":
			return "D"
		}
		return "M"
	}
	if rel == "" {
		return ""
	}
	prefix := rel + "/"
	for p, f := range c.Git {
		if strings.HasPrefix(p, prefix) && f.Kind != "ignored" {
			return "M"
		}
	}
	return ""
}

// LoadFileIndex loads the full file list for quick open.
func (c *Controller) LoadFileIndex() {
	if !c.Project.Open {
		return
	}
	c.Go(func(ctx context.Context) func() {
		var res filesListResult
		if err := c.call(ctx, "files.list", map[string]any{}, &res); err != nil || res.Tree == nil {
			return nil
		}
		var files []string
		var walk func(n *FileNode)
		walk = func(n *FileNode) {
			for _, ch := range n.Children {
				if ch.Dir {
					walk(ch)
				} else {
					files = append(files, ch.Path)
				}
			}
		}
		walk(res.Tree)
		return func() { c.Files = files }
	})
}

// Match is a fuzzy match result.
type Match struct {
	Index int
	Score int
	// Pos are the matched byte positions in the candidate.
	Pos []int
}

// FuzzyMatch matches query as a case-insensitive subsequence of candidate
// and scores it: consecutive characters, matches at word starts and in the
// file name rank higher. ok is false if query is not a subsequence.
func FuzzyMatch(query, candidate string) (score int, positions []int, ok bool) {
	if query == "" {
		return 0, nil, true
	}
	q := strings.ToLower(query)
	s := strings.ToLower(candidate)
	base := strings.LastIndexByte(s, '/') + 1
	qi := 0
	prev := -2
	for i := 0; i < len(s) && qi < len(q); i++ {
		if s[i] != q[qi] {
			continue
		}
		pts := 1
		if i == prev+1 {
			pts += 5
		}
		if i == 0 || strings.IndexByte("/_-. ", s[i-1]) >= 0 || (candidate[i] >= 'A' && candidate[i] <= 'Z') {
			pts += 3
		}
		if i >= base {
			pts += 2
		}
		score += pts
		positions = append(positions, i)
		prev = i
		qi++
	}
	if qi < len(q) {
		return 0, nil, false
	}
	// Shorter candidates win ties.
	score = score*100 - len(candidate)
	if strings.HasPrefix(s[base:], q) {
		score += 1000
	}
	return score, positions, true
}

// FilterFuzzy returns the matches of query in candidates, best first, at
// most limit (0 = all).
func FilterFuzzy(query string, candidates []string, limit int) []Match {
	var out []Match
	for i, cand := range candidates {
		if sc, pos, ok := FuzzyMatch(query, cand); ok {
			out = append(out, Match{Index: i, Score: sc, Pos: pos})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// baseName returns the last element of a slash path.
func baseName(p string) string { return path.Base(p) }
