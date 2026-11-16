package dbgp

import "testing"

func TestPathToURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/var/www/index.php", "file:///var/www/index.php"},
		{"/var/www/my dir/ä#?.php", "file:///var/www/my%20dir/%C3%A4%23%3F.php"},
		{`C:\work\a b\x.php`, "file:///C:/work/a%20b/x.php"},
		{"C:/work/x.php", "file:///C:/work/x.php"},
		{`\\server\share\x.php`, "file://server/share/x.php"},
		{"relative/x.php", "file:///relative/x.php"},
		{"file:///already", "file:///already"},
	}
	for _, c := range cases {
		if got := PathToURI(c.in); got != c.want {
			t.Errorf("PathToURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestURIToPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"file:///var/www/index.php", "/var/www/index.php"},
		{"file:///var/www/my%20dir/%C3%A4.php", "/var/www/my dir/ä.php"},
		{"file:///C:/work/a%20b/x.php", `C:\work\a b\x.php`},
		{"file:///c%3A/work/x.php", `c:\work\x.php`},
		{"file://C:/work/x.php", `C:\work\x.php`},
		{"file://localhost/var/x.php", "/var/x.php"},
		{"file://server/share/x.php", `\\server\share\x.php`},
		{"FILE:///x.php", "/x.php"},
		{"file:///bad%zzname.php", "/bad%zzname.php"},
		{"dbgp://1", "dbgp://1"},
		{"/plain/path", "/plain/path"},
	}
	for _, c := range cases {
		if got := URIToPath(c.in); got != c.want {
			t.Errorf("URIToPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Round trip.
	for _, p := range []string{"/a b/c%d.php", `D:\x y\z.php`, `\\srv\share\a b.php`} {
		if got := URIToPath(PathToURI(p)); got != p {
			t.Errorf("roundtrip %q -> %q", p, got)
		}
	}
}

func TestPrefixMapper(t *testing.T) {
	m := NewPrefixMapper(
		Mapping{Host: "/Users/me/app", Remote: "/var/www/html"},
		Mapping{Host: "/Users/me/vendor-src/", Remote: "/var/www/html/vendor/"},
		Mapping{Host: "/Users/me/lib", Remote: "/opt/lib"},
		Mapping{Host: "", Remote: "/ignored"},
	)
	if len(m.Mappings()) != 3 {
		t.Fatalf("mappings = %v", m.Mappings())
	}
	h2r := []struct{ in, want string }{
		{"/Users/me/app/index.php", "file:///var/www/html/index.php"},
		{"/Users/me/app", "file:///var/www/html"},
		{"/Users/me/app/sub dir/x.php", "file:///var/www/html/sub%20dir/x.php"},
		{"/Users/me/vendor-src/pkg/a.php", "file:///var/www/html/vendor/pkg/a.php"},
		{"/Users/me/application/x.php", "file:///Users/me/application/x.php"}, // segment boundary
		{"/tmp/x.php", "file:///tmp/x.php"},
		{"file:///Users/me/lib/l.php", "file:///opt/lib/l.php"},
	}
	for _, c := range h2r {
		if got := m.HostToRemote(c.in); got != c.want {
			t.Errorf("HostToRemote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	r2h := []struct{ in, want string }{
		{"file:///var/www/html/index.php", "/Users/me/app/index.php"},
		// Longest remote prefix wins.
		{"file:///var/www/html/vendor/pkg/a.php", "/Users/me/vendor-src/pkg/a.php"},
		{"file:///var/www/html/sub%20dir/x.php", "/Users/me/app/sub dir/x.php"},
		{"file:///var/www/htmlx/x.php", "/var/www/htmlx/x.php"},
		{"file:///opt/lib", "/Users/me/lib"},
		{"/var/www/html/plain.php", "/Users/me/app/plain.php"},
		{"dbgp://42", "dbgp://42"},
	}
	for _, c := range r2h {
		if got := m.RemoteToHost(c.in); got != c.want {
			t.Errorf("RemoteToHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrefixMapperWindowsAndRoots(t *testing.T) {
	m := NewPrefixMapper(
		Mapping{Host: `C:\Work\App\`, Remote: "/app"},
		Mapping{Host: "/", Remote: "/host"},
		Mapping{Host: "/srv/data", Remote: `D:\data`},
	)
	cases := []struct {
		fn       func(string) string
		in, want string
	}{
		{m.HostToRemote, `C:\Work\App\src\A.php`, "file:///app/src/A.php"},
		{m.HostToRemote, `c:\work\app\src\A.php`, "file:///app/src/A.php"}, // case-insensitive drive paths
		{m.HostToRemote, `C:\Work\App`, "file:///app"},
		{m.RemoteToHost, "file:///app/src/A.php", `C:\Work\App\src\A.php`},
		{m.RemoteToHost, "file:///app", `C:\Work\App\`},
		{m.HostToRemote, "/etc/x.php", "file:///host/etc/x.php"},
		{m.RemoteToHost, "file:///host/etc/x.php", "/etc/x.php"},
		{m.RemoteToHost, "file:///D:/data/f.php", "/srv/data/f.php"},
		{m.HostToRemote, "/srv/data/f.php", "file:///D:/data/f.php"},
	}
	for _, c := range cases {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
	}

	root := NewPrefixMapper(Mapping{Host: "/Users/me/app", Remote: "/"})
	if got := root.HostToRemote("/Users/me/app/x.php"); got != "file:///x.php" {
		t.Errorf("remote root: %q", got)
	}
	if got := root.RemoteToHost("file:///x.php"); got != "/Users/me/app/x.php" {
		t.Errorf("remote root back: %q", got)
	}

	var nilMapper *PrefixMapper
	if got := nilMapper.RemoteToHost("file:///a.php"); got != "/a.php" {
		t.Errorf("nil mapper: %q", got)
	}
	if got := (&PrefixMapper{}).HostToRemote("/a.php"); got != "file:///a.php" {
		t.Errorf("zero mapper: %q", got)
	}
}
