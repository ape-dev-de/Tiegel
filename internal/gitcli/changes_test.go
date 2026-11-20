package gitcli

import (
	"reflect"
	"strings"
	"testing"
)

func sampleStatus() []FileStatus {
	return []FileStatus{
		{Path: "src/Http/Controller/UserController.php", Index: '.', Worktree: 'M'},
		{Path: "src/Http/Controller/Admin.php", Index: 'A', Worktree: '.'},
		{Path: "src/Mail/Mailer.php", Index: 'M', Worktree: 'M'},
		{Kind: KindRenamed, Path: "src/Support/Str.php", OrigPath: "src/Util/Str.php", Index: 'R', Worktree: '.'},
		{Path: "composer.json", Index: '.', Worktree: 'M'},
		{Path: "config/old.yaml", Index: 'D', Worktree: '.'},
		{Kind: KindUnmerged, Path: "src/Billing/Price.php", Index: 'U', Worktree: 'U'},
		{Kind: KindUntracked, Path: "docs/new.md", Index: '?', Worktree: '?'},
		{Kind: KindUntracked, Path: "README.dev", Index: '?', Worktree: '?'},
		{Kind: KindIgnored, Path: "vendor/x.php", Index: '!', Worktree: '!'},
	}
}

// dump renders the tree as indented "name (count)" lines.
func dump(n *ChangeNode, depth int, b *strings.Builder) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(n.Name)
	if n.Kind != NodeFile {
		b.WriteString(" (")
		b.WriteString(string(rune('0' + n.Count)))
		b.WriteString(")")
	} else {
		b.WriteString(" " + string(n.State))
	}
	b.WriteString("\n")
	for _, c := range n.Children {
		dump(c, depth+1, b)
	}
}

func TestBuildChangeTreeDirectory(t *testing.T) {
	tr := BuildChangeTree(sampleStatus(), "", "main")
	if tr.Grouping != GroupDirectory || tr.Changes.Badge != "main" || tr.Changes.Count != 7 || tr.Unversioned.Count != 2 {
		t.Fatalf("tree %+v", tr)
	}
	var b strings.Builder
	dump(&tr.Changes, 0, &b)
	want := `Changes (7)
  config (1)
    old.yaml deleted
  src (5)
    Billing (1)
      Price.php conflict
    Http/Controller (2)
      Admin.php added
      UserController.php modified
    Mail (1)
      Mailer.php modified
    Support (1)
      Str.php renamed
  composer.json modified
`
	if b.String() != want {
		t.Errorf("tree:\n%s\nwant:\n%s", b.String(), want)
	}
	order := tr.Order()
	wantOrder := []string{"config/old.yaml", "src/Billing/Price.php", "src/Http/Controller/Admin.php", "src/Http/Controller/UserController.php",
		"src/Mail/Mailer.php", "src/Support/Str.php", "composer.json", "docs/new.md", "README.dev"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("order %v", order)
	}
	if n := tr.Find("src/Support/Str.php"); n == nil || n.OrigPath != "src/Util/Str.php" || n.Dir != "src/Support" {
		t.Errorf("find %+v", n)
	}
	if tr.Find("nope") != nil {
		t.Error("find missing")
	}
}

func TestBuildChangeTreeModuleAndFlat(t *testing.T) {
	tr := BuildChangeTree(sampleStatus(), GroupModule, "")
	var names []string
	for _, c := range tr.Changes.Children {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"config", "src", RootModule}) || tr.Changes.Children[1].Count != 5 {
		t.Errorf("modules %v", names)
	}
	flat := BuildChangeTree(sampleStatus(), GroupFlat, "")
	if len(flat.Changes.Children) != 7 || flat.Changes.Children[0].Path != "composer.json" {
		t.Errorf("flat %+v", flat.Changes.Children[0])
	}
	if len(flat.Unversioned.Children) != 2 || flat.Unversioned.Children[0].State != StateUnversioned {
		t.Errorf("flat unversioned %+v", flat.Unversioned)
	}
	empty := BuildChangeTree(nil, GroupDirectory, "")
	if empty.Changes.Count != 0 || len(empty.Order()) != 0 {
		t.Error("empty tree")
	}
}

func TestParseHelpers(t *testing.T) {
	for _, s := range []string{"ordinary", "renamed", "unmerged", "untracked", "ignored"} {
		if ParseEntryKind(s).String() != s {
			t.Errorf("kind %s", s)
		}
	}
	if ParseEntryKind("bogus") != KindOrdinary {
		t.Error("unknown kind")
	}
	for _, s := range []string{"", "none", "directory", "module"} {
		if _, err := ParseChangeGrouping(s); err != nil {
			t.Errorf("grouping %q: %v", s, err)
		}
	}
	if _, err := ParseChangeGrouping("x"); err == nil {
		t.Error("bad grouping accepted")
	}
	for _, s := range []string{"", "local", "staged", "unstaged"} {
		if _, err := ParseDiffMode(s); err != nil {
			t.Errorf("mode %q: %v", s, err)
		}
	}
	if _, err := ParseDiffMode("x"); err == nil {
		t.Error("bad mode accepted")
	}
	if StateOf(FileStatus{Kind: KindIgnored}) != StateIgnored {
		t.Error("ignored state")
	}
}

func TestChangeDiff(t *testing.T) {
	mod := FileStatus{Path: "a.php", Index: 'M', Worktree: 'M'}
	cases := []struct {
		st   FileStatus
		mode DiffMode
		want DiffVersions
	}{
		{mod, DiffLocal, DiffVersions{Base: VersionHead, Target: VersionWorking}},
		{mod, DiffStaged, DiffVersions{Base: VersionHead, Target: VersionIndex}},
		{mod, DiffUnstaged, DiffVersions{Base: VersionIndex, Target: VersionWorking}},
		{FileStatus{Kind: KindUntracked, Path: "n.php"}, DiffLocal, DiffVersions{Base: VersionEmpty, Target: VersionWorking}},
		{FileStatus{Path: "n.php", Index: 'A'}, DiffStaged, DiffVersions{Base: VersionEmpty, Target: VersionIndex}},
		{FileStatus{Path: "n.php", Index: 'A', Worktree: 'M'}, DiffUnstaged, DiffVersions{Base: VersionIndex, Target: VersionWorking}},
		{FileStatus{Kind: KindRenamed, Path: "b.php", OrigPath: "a.php", Index: 'R'}, DiffLocal, DiffVersions{Base: VersionHead, Target: VersionWorking, BasePath: "a.php"}},
	}
	for _, c := range cases {
		if got := ChangeDiff(c.st, c.mode); got != c.want {
			t.Errorf("%+v %s: %+v, want %+v", c.st, c.mode, got, c.want)
		}
	}
	if got := (DiffVersions{Base: VersionHead, Target: VersionWorking}).Title("src/a.php"); got != "a.php (HEAD ↔ local)" {
		t.Errorf("title %q", got)
	}
	if got := (DiffVersions{Base: strings.Repeat("a", 40), Target: VersionIndex}).Title("a.php"); got != "a.php (aaaaaaaa ↔ index)" {
		t.Errorf("title %q", got)
	}
}

func TestNeighbor(t *testing.T) {
	order := []string{"a", "b", "c"}
	for _, c := range []struct {
		cur   string
		delta int
		want  string
		idx   int
	}{
		{"a", 1, "b", 1}, {"c", 1, "a", 0}, {"a", -1, "c", 2}, {"b", 0, "b", 1},
		{"x", 1, "a", 0}, {"x", -1, "c", 2},
	} {
		p, i, ok := Neighbor(order, c.cur, c.delta)
		if !ok || p != c.want || i != c.idx {
			t.Errorf("%s %+d: %s %d", c.cur, c.delta, p, i)
		}
	}
	if _, _, ok := Neighbor(nil, "a", 1); ok {
		t.Error("empty order")
	}
}
