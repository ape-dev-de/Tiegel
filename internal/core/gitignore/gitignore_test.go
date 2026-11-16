package gitignore

import "testing"

func TestIgnoreRules(t *testing.T) {
	var s Set
	for _, l := range []string{"*.tmp", "/root-only", "docs/*.md", "a/**/z", "logs/**", "dir/", "!important.tmp", "\\#hash", "", "/"} {
		s.Add("", l)
	}
	s.Add("sub", "local")
	cases := []struct {
		path  string
		dir   bool
		match bool
	}{
		{"x.tmp", false, true}, {"deep/x.tmp", false, true}, {"important.tmp", false, false},
		{"root-only", false, true}, {"x/root-only", false, false},
		{"docs/a.md", false, true}, {"docs/x/a.md", false, false},
		{"a/z", false, true}, {"a/b/z", false, true}, {"a/b/c/z", false, true}, {"b/z", false, false},
		{"logs/x", false, true}, {"logs", true, false},
		{"dir", true, true}, {"dir", false, false}, {"x/dir", true, true},
		{"#hash", false, true},
		{"sub/local", false, true}, {"local", false, false}, {"other/local", false, false},
	}
	for _, c := range cases {
		if got := s.Match(c.path, c.dir); got != c.match {
			t.Errorf("match(%q, dir=%v) = %v, want %v", c.path, c.dir, got, c.match)
		}
	}
	if firstSegments("a/b", 5) != "a/b" {
		t.Fatal("firstSegments")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	var s Set
	s.Load(dir, "") // missing file: no rules
	if s.Match("x", false) {
		t.Fatal("empty set matched")
	}
}
