package run

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

func TestTargetPathMappings(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	ci := &compose.Interpreter{Service: "php", Mappings: compose.Mappings{}.Set(root, "/var/www")}
	if h, ok := (Target{Interp: ci}).PathMappings().ToHost("/var/www/src/A.php"); !ok || h != filepath.Join(root, "src", "A.php") {
		t.Fatal(h, ok)
	}
	own := compose.Mappings{}.Set(root, "/srv")
	if _, ok := (Target{Interp: ci, Mappings: own}).PathMappings().ToHost("/var/www/x"); ok {
		t.Fatal("configured mappings win")
	}
	d := docker.NewImageInterpreter("php:8.3-cli", "/app", root, docker.Client{Binary: "docker"})
	if h, ok := (Target{Interp: d}).PathMappings().ToHost("/app/a.php"); !ok || h != filepath.Join(root, "a.php") {
		t.Fatal(h, ok)
	}
	w := &interp.WSL{Distro: "Ubuntu"}
	m := Target{Interp: w, ProjectDir: `C:\proj`}.PathMappings()
	if runtime.GOOS == "windows" && len(m) != 1 {
		t.Fatal(m)
	}
	if len((Target{}).PathMappings()) != 0 || !(Target{}).OnHost() || !(Target{Interp: &interp.Local{}}).OnHost() || (Target{Interp: ci}).OnHost() {
		t.Fatal("host")
	}
}
