package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestMappingsLongestPrefix(t *testing.T) {
	root := filepath.FromSlash("/home/u/project")
	m := Mappings{}.
		Set(root, "/var/www/html").
		Set(filepath.Join(root, "src"), "/opt/src").
		Set(filepath.Join(root, "app"), "/app")

	cases := []struct {
		host, remote string
		ok           bool
	}{
		{root, "/var/www/html", true},
		{filepath.Join(root, "index.php"), "/var/www/html/index.php", true},
		{filepath.Join(root, "src", "A", "B.php"), "/opt/src/A/B.php", true},
		{filepath.Join(root, "src"), "/opt/src", true},
		// Segment aware: "application" is not below "app".
		{filepath.Join(root, "application", "x.php"), "/var/www/html/application/x.php", true},
		{filepath.FromSlash("/home/u/project2/x.php"), "", false},
		{filepath.FromSlash("/elsewhere"), "", false},
	}
	for _, c := range cases {
		got, ok := m.ToRemote(c.host)
		if got != c.remote || ok != c.ok {
			t.Errorf("ToRemote(%q) = %q %v, want %q %v", c.host, got, ok, c.remote, c.ok)
		}
		if !c.ok {
			continue
		}
		back, ok := m.ToHost(c.remote)
		if !ok || back != c.host {
			t.Errorf("ToHost(%q) = %q %v, want %q", c.remote, back, ok, c.host)
		}
	}
	if _, ok := m.ToHost("/etc/passwd"); ok {
		t.Error("ToHost outside mappings must fail")
	}
	if got, ok := m.ToHost("/var/www/html/./a/../b.php"); !ok || got != filepath.Join(root, "b.php") {
		t.Errorf("ToHost unclean = %q %v", got, ok)
	}
}

func TestMappingsEdit(t *testing.T) {
	a := filepath.FromSlash("/a")
	b := filepath.FromSlash("/b")
	m := Mappings{}.Set(a, "/x").Set(b, "/y/")
	if len(m) != 2 || m[1].Remote != "/y" {
		t.Fatalf("Set: %v", m)
	}
	m2 := m.Set(a, "/z")
	if m[0].Remote != "/x" {
		t.Error("Set must not modify the receiver")
	}
	if !reflect.DeepEqual(m2, Mappings{{a, "/z"}, {b, "/y"}}) {
		t.Errorf("replace: %v", m2)
	}
	// Duplicate hosts collapse to one entry on Set.
	dup := Mappings{{a, "/1"}, {a, "/2"}}.Set(a, "/3")
	if !reflect.DeepEqual(dup, Mappings{{a, "/3"}}) {
		t.Errorf("dedupe: %v", dup)
	}
	m3 := m2.Remove(a + string(filepath.Separator))
	if !reflect.DeepEqual(m3, Mappings{{b, "/y"}}) {
		t.Errorf("Remove: %v", m3)
	}
	if len(m2) != 2 {
		t.Error("Remove must not modify the receiver")
	}
	// Root mapping.
	root := Mappings{}.Set(filepath.FromSlash("/srv"), "/")
	if r, ok := root.ToRemote(filepath.FromSlash("/srv/x/y")); !ok || r != "/x/y" {
		t.Errorf("root remote: %q %v", r, ok)
	}
	if h, ok := root.ToHost("/x"); !ok || h != filepath.FromSlash("/srv/x") {
		t.Errorf("root host: %q %v", h, ok)
	}
}

func TestServiceMappings(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir := filepath.FromSlash("/proj")
	var svc types.ServiceConfig
	svc.Volumes = []types.ServiceVolumeConfig{
		{Type: types.VolumeTypeBind, Source: ".", Target: "/var/www/html"},
		{Type: types.VolumeTypeBind, Source: "~/.composer", Target: "/root/.composer"},
		{Type: types.VolumeTypeBind, Source: "~", Target: "/home"},
		{Type: types.VolumeTypeBind, Source: filepath.FromSlash("/abs/conf"), Target: "/etc/conf/"},
		{Type: types.VolumeTypeVolume, Source: "vendor", Target: "/var/www/html/vendor"},
		{Type: types.VolumeTypeTmpfs, Target: "/tmp"},
		{Type: types.VolumeTypeBind, Source: "", Target: "/x"},
	}
	want := Mappings{
		{dir, "/var/www/html"},
		{filepath.Join(home, ".composer"), "/root/.composer"},
		{home, "/home"},
		{filepath.FromSlash("/abs/conf"), "/etc/conf"},
	}
	if got := ServiceMappings(dir, svc); !reflect.DeepEqual(got, want) {
		t.Errorf("ServiceMappings = %v, want %v", got, want)
	}
}
