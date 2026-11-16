package update

import (
	"sort"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3", "1.2.3"},
		{"V1.2.3", "1.2.3"},
		{" v0.1.0 ", "0.1.0"},
		{"v2", "2.0.0"},
		{"2.1", "2.1.0"},
		{"1.0.0-rc.1", "1.0.0-rc.1"},
		{"1.0.0+build.5", "1.0.0"},
		{"1.0.0-beta+exp.sha.5114f85", "1.0.0-beta"},
		{"v1.2.3-4-gabc1234", "1.2.3"},
		{"v1.2.3-4-gabc1234-dirty", "1.2.3"},
		{"v1.2.3-dirty", "1.2.3"},
		{"v1.0.0-rc.2-7-g0123abc", "1.0.0-rc.2"},
	}
	for _, tt := range tests {
		v, err := ParseVersion(tt.in)
		if err != nil {
			t.Errorf("ParseVersion(%q): %v", tt.in, err)
			continue
		}
		if got := v.String(); got != tt.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseVersionErrors(t *testing.T) {
	for _, in := range []string{
		"", "v", "dev", "abc1234", "1.2.3.4", "1..2", "1.x.0", "1.2.3-", "1.2.3-a..b",
		"1.2.3-a_b", "99999999999999999999999.0.0",
	} {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want error", in, v)
		}
	}
}

func TestComparePrecedence(t *testing.T) {
	// Ordered list from the semver specification plus extras.
	ordered := []string{
		"0.9.9",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
		"10.0.0",
	}
	vs := make([]Version, len(ordered))
	for i, s := range ordered {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatal(err)
		}
		vs[i] = v
	}
	for i := range vs {
		for j := range vs {
			want := cmpInt(i, j)
			if got := vs[i].Compare(vs[j]); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", vs[i], vs[j], got, want)
			}
		}
	}
	shuffled := append([]Version(nil), vs...)
	sort.Slice(shuffled, func(a, b int) bool { return shuffled[a].String() > shuffled[b].String() })
	sort.Slice(shuffled, func(a, b int) bool { return shuffled[a].Compare(shuffled[b]) < 0 })
	for i := range shuffled {
		if shuffled[i].String() != ordered[i] {
			t.Fatalf("sorted[%d] = %s, want %s", i, shuffled[i], ordered[i])
		}
	}
}

func TestDescribeEqualsTag(t *testing.T) {
	a, _ := ParseVersion("v1.2.3-12-gdeadbee")
	b, _ := ParseVersion("v1.2.3")
	if a.Compare(b) != 0 {
		t.Fatalf("describe build should equal its tag")
	}
	if a.IsPrerelease() {
		t.Fatal("describe build must not be a pre-release")
	}
}
