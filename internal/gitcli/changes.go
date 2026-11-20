package gitcli

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// The change tree of the commit tool window and the versions a change is
// compared with. Both are pure functions of a status, so the GUI, the API
// (vcs.changes, vcs.diffTab) and the headless commands group, order and
// compare changes identically.

// ParseEntryKind returns the kind named by EntryKind.String ("ordinary"
// for unknown names).
func ParseEntryKind(s string) EntryKind {
	switch s {
	case "renamed":
		return KindRenamed
	case "unmerged":
		return KindUnmerged
	case "untracked":
		return KindUntracked
	case "ignored":
		return KindIgnored
	}
	return KindOrdinary
}

// ChangeState is the display state of a changed file.
type ChangeState string

// Change states.
const (
	StateModified    ChangeState = "modified"
	StateAdded       ChangeState = "added"
	StateDeleted     ChangeState = "deleted"
	StateRenamed     ChangeState = "renamed"
	StateConflict    ChangeState = "conflict"
	StateUnversioned ChangeState = "unversioned"
	StateIgnored     ChangeState = "ignored"
)

// StateOf classifies a status entry. Deletions and additions win over
// modifications in either the index or the work tree.
func StateOf(st FileStatus) ChangeState {
	switch st.Kind {
	case KindUntracked:
		return StateUnversioned
	case KindIgnored:
		return StateIgnored
	case KindUnmerged:
		return StateConflict
	}
	switch {
	case st.Index == 'D' || st.Worktree == 'D':
		return StateDeleted
	case st.Index == 'A':
		return StateAdded
	case st.Kind == KindRenamed:
		return StateRenamed
	}
	return StateModified
}

// ChangeGrouping selects how the change tree groups files.
type ChangeGrouping string

// Groupings of the change tree.
const (
	// GroupFlat lists the files directly below their group.
	GroupFlat ChangeGrouping = "none"
	// GroupDirectory nests the files in their directories; chains of
	// directories with a single child directory and no files are merged
	// into one node ("src/Http/Controller").
	GroupDirectory ChangeGrouping = "directory"
	// GroupModule groups by the top-level directory (the module of a PHP
	// project: src, tests, config, …); files in the root form "<root>".
	GroupModule ChangeGrouping = "module"
)

// ChangeGroupings lists the groupings in menu order.
var ChangeGroupings = []ChangeGrouping{GroupDirectory, GroupModule, GroupFlat}

// ParseChangeGrouping parses a grouping name ("" is GroupDirectory).
func ParseChangeGrouping(s string) (ChangeGrouping, error) {
	switch g := ChangeGrouping(s); g {
	case "":
		return GroupDirectory, nil
	case GroupFlat, GroupDirectory, GroupModule:
		return g, nil
	}
	return "", fmt.Errorf("unknown grouping %q (none, directory, module)", s)
}

// Kinds of change tree nodes.
const (
	NodeGroup = "group"
	NodeDir   = "dir"
	NodeFile  = "file"
)

// RootModule is the module name of files in the repository root.
const RootModule = "<root>"

// ChangeNode is a node of the change tree: a group ("Changes",
// "Unversioned Files"), a directory or module, or a file.
type ChangeNode struct {
	// Kind is NodeGroup, NodeDir or NodeFile.
	Kind string `json:"kind"`
	// Name is the display name: the group title, the (merged) directory
	// path relative to the parent node, or the file's base name.
	Name string `json:"name"`
	// Path is the slash separated repository path of a directory or file
	// ("" for groups).
	Path string `json:"path,omitempty"`
	// Count is the number of files below the node (1 for a file).
	Count int `json:"count"`
	// Badge is a label shown next to a group (the branch of "Changes").
	Badge string `json:"badge,omitempty"`
	// File fields.
	OrigPath string      `json:"origPath,omitempty"`
	State    ChangeState `json:"state,omitempty"`
	// Dir is the directory of a file ("" in the root), for display.
	Dir      string        `json:"dir,omitempty"`
	Children []*ChangeNode `json:"children,omitempty"`
}

// ChangeTree is the tree of the commit tool window.
type ChangeTree struct {
	Grouping ChangeGrouping `json:"grouping"`
	// Changes holds the tracked changes (conflicts included),
	// Unversioned the untracked files. Ignored files are dropped.
	Changes     ChangeNode `json:"changes"`
	Unversioned ChangeNode `json:"unversioned"`
}

// Group titles.
const (
	TitleChanges     = "Changes"
	TitleUnversioned = "Unversioned Files"
)

// BuildChangeTree groups the status entries. branch, if not empty, is
// the badge of the "Changes" group.
func BuildChangeTree(files []FileStatus, g ChangeGrouping, branch string) ChangeTree {
	if g == "" {
		g = GroupDirectory
	}
	var tracked, untracked []FileStatus
	for _, f := range files {
		switch f.Kind {
		case KindIgnored:
		case KindUntracked:
			untracked = append(untracked, f)
		default:
			tracked = append(tracked, f)
		}
	}
	t := ChangeTree{Grouping: g}
	t.Changes = ChangeNode{Kind: NodeGroup, Name: TitleChanges, Badge: branch, Count: len(tracked), Children: groupFiles(tracked, g)}
	t.Unversioned = ChangeNode{Kind: NodeGroup, Name: TitleUnversioned, Count: len(untracked), Children: groupFiles(untracked, g)}
	return t
}

// Order returns the file paths in display order (Changes, then
// Unversioned Files) – the sequence the diff tab steps through.
func (t ChangeTree) Order() []string {
	var out []string
	var walk func(n *ChangeNode)
	walk = func(n *ChangeNode) {
		if n.Kind == NodeFile {
			out = append(out, n.Path)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(&t.Changes)
	walk(&t.Unversioned)
	return out
}

// Find returns the file node of p, or nil.
func (t ChangeTree) Find(p string) *ChangeNode {
	var found *ChangeNode
	var walk func(n *ChangeNode)
	walk = func(n *ChangeNode) {
		if found != nil {
			return
		}
		if n.Kind == NodeFile && n.Path == p {
			found = n
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(&t.Changes)
	walk(&t.Unversioned)
	return found
}

func fileNode(f FileStatus) *ChangeNode {
	dir := path.Dir(f.Path)
	if dir == "." {
		dir = ""
	}
	return &ChangeNode{Kind: NodeFile, Name: path.Base(f.Path), Path: f.Path, Count: 1, OrigPath: f.OrigPath, State: StateOf(f), Dir: dir}
}

func sortFiles(files []FileStatus) []FileStatus {
	out := append([]FileStatus(nil), files...)
	sort.SliceStable(out, func(i, j int) bool { return lessPath(out[i].Path, out[j].Path) })
	return out
}

// lessPath orders case-insensitively, ties by byte order.
func lessPath(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

func groupFiles(files []FileStatus, g ChangeGrouping) []*ChangeNode {
	files = sortFiles(files)
	switch g {
	case GroupFlat:
		out := make([]*ChangeNode, 0, len(files))
		for _, f := range files {
			out = append(out, fileNode(f))
		}
		return out
	case GroupModule:
		var out []*ChangeNode
		byName := map[string]*ChangeNode{}
		var rootFiles []*ChangeNode
		for _, f := range files {
			mod, _, ok := strings.Cut(f.Path, "/")
			if !ok {
				rootFiles = append(rootFiles, fileNode(f))
				continue
			}
			n := byName[mod]
			if n == nil {
				n = &ChangeNode{Kind: NodeDir, Name: mod, Path: mod}
				byName[mod] = n
				out = append(out, n)
			}
			n.Children = append(n.Children, fileNode(f))
			n.Count++
		}
		sort.SliceStable(out, func(i, j int) bool { return lessPath(out[i].Name, out[j].Name) })
		if len(rootFiles) > 0 {
			out = append(out, &ChangeNode{Kind: NodeDir, Name: RootModule, Count: len(rootFiles), Children: rootFiles})
		}
		return out
	}
	// Directories: build the full tree, then merge single-child chains.
	root := &ChangeNode{Kind: NodeDir}
	dirs := map[string]*ChangeNode{"": root}
	var dirOf func(p string) *ChangeNode
	dirOf = func(p string) *ChangeNode {
		if n := dirs[p]; n != nil {
			return n
		}
		parent := path.Dir(p)
		if parent == "." {
			parent = ""
		}
		n := &ChangeNode{Kind: NodeDir, Name: path.Base(p), Path: p}
		dirs[p] = n
		pn := dirOf(parent)
		pn.Children = append(pn.Children, n)
		return n
	}
	for _, f := range files {
		d := path.Dir(f.Path)
		if d == "." {
			d = ""
		}
		dirOf(d).Children = append(dirOf(d).Children, fileNode(f))
	}
	var finish func(n *ChangeNode) int
	finish = func(n *ChangeNode) int {
		count := 0
		for i, c := range n.Children {
			if c.Kind == NodeDir {
				for len(c.Children) == 1 && c.Children[0].Kind == NodeDir {
					only := c.Children[0]
					only.Name = c.Name + "/" + only.Name
					c = only
				}
				n.Children[i] = c
				count += finish(c)
			} else {
				count++
			}
		}
		n.Count = count
		// Directories first, then files; each alphabetically.
		sort.SliceStable(n.Children, func(i, j int) bool {
			a, b := n.Children[i], n.Children[j]
			if (a.Kind == NodeDir) != (b.Kind == NodeDir) {
				return a.Kind == NodeDir
			}
			return lessPath(a.Name, b.Name)
		})
		return count
	}
	finish(root)
	return root.Children
}

// Versions compared by a change diff (the names of git.fileDiff).
const (
	VersionHead    = "HEAD"
	VersionIndex   = "index"
	VersionWorking = "working"
	VersionEmpty   = "empty"
)

// DiffMode selects which state of a change a diff tab compares.
type DiffMode string

// Diff modes of a change.
const (
	// DiffLocal compares HEAD with the work tree (everything a commit of
	// the whole file would contain).
	DiffLocal DiffMode = "local"
	// DiffStaged compares HEAD with the index.
	DiffStaged DiffMode = "staged"
	// DiffUnstaged compares the index with the work tree.
	DiffUnstaged DiffMode = "unstaged"
)

// ParseDiffMode parses a mode name ("" is DiffLocal).
func ParseDiffMode(s string) (DiffMode, error) {
	switch m := DiffMode(s); m {
	case "":
		return DiffLocal, nil
	case DiffLocal, DiffStaged, DiffUnstaged:
		return m, nil
	}
	return "", fmt.Errorf("unknown diff mode %q (local, staged, unstaged)", s)
}

// DiffVersions are the sides of a change diff.
type DiffVersions struct {
	// Base and Target are git.fileDiff versions.
	Base   string `json:"base"`
	Target string `json:"target"`
	// BasePath is the base side's path of a rename ("" = the file's path).
	BasePath string `json:"basePath,omitempty"`
}

// ChangeDiff returns the versions a diff of st compares in mode. New
// files (untracked, or added in the index for the HEAD side) are compared
// with the empty file (the staged diff of an untracked file is empty on
// both sides); the source of a rename is the base path.
func ChangeDiff(st FileStatus, mode DiffMode) DiffVersions {
	v := DiffVersions{Base: VersionHead, Target: VersionWorking}
	switch mode {
	case DiffStaged:
		v.Target = VersionIndex
	case DiffUnstaged:
		v.Base = VersionIndex
	}
	switch {
	case st.Kind == KindUntracked:
		// Not in HEAD and not in the index: nothing is staged.
		v.Base = VersionEmpty
		if mode == DiffStaged {
			v.Target = VersionEmpty
		}
	case v.Base == VersionHead && st.Index == 'A':
		v.Base = VersionEmpty
	case v.Base == VersionHead && st.OrigPath != "" && st.OrigPath != st.Path:
		v.BasePath = st.OrigPath
	}
	return v
}

// Title is the diff tab title of p, e.g. "Mailer.php (HEAD ↔ local)".
func (v DiffVersions) Title(p string) string {
	short := func(s string) string {
		switch {
		case s == VersionWorking:
			return "local"
		case len(s) == 40:
			return s[:8]
		}
		return s
	}
	return fmt.Sprintf("%s (%s ↔ %s)", path.Base(p), short(v.Base), short(v.Target))
}

// Neighbor returns the path delta steps away from cur in order (wrapping
// around) and its index; cur not in order yields the first (delta > 0) or
// last entry. ok is false for an empty order.
func Neighbor(order []string, cur string, delta int) (p string, index int, ok bool) {
	n := len(order)
	if n == 0 {
		return "", -1, false
	}
	i := indexOf(order, cur)
	switch {
	case i < 0 && delta >= 0:
		i = 0
	case i < 0:
		i = n - 1
	default:
		i = ((i+delta)%n + n) % n
	}
	return order[i], i, true
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
