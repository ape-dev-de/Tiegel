package compose

import (
	"context"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
)

// TestInterpreterVersionLabelRace: Version on an ExecOnly copy and Label
// on the cached original run concurrently; run with -race.
func TestInterpreterVersionLabelRace(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"Name":"p-app-1","Service":"app","State":"running"}`+"\n")
	c := NewInterpreter(&Project{Name: "p", Dir: t.TempDir()}, "app")
	c.Docker, c.Env = f.Binary, f.Env
	ex := c.ExecOnly()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, i := range []*Interpreter{c, ex, c} {
			if _, err := i.Version(context.Background()); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for {
		_ = c.Label()
		select {
		case <-done:
			if c.CachedVersion() != "8.3.1" || c.Label() != "PHP 8.3.1 (compose p/app)" {
				t.Fatalf("version not shared: %q", c.Label())
			}
			return
		default:
		}
	}
}

func TestInterpreterSeedVersion(t *testing.T) {
	c := &Interpreter{Project: "p", Service: "app", PHPVersion: "8.2.0"}
	if c.CachedVersion() != "8.2.0" || c.Label() != "PHP 8.2.0 (compose p/app)" {
		t.Fatalf("seed %q", c.Label())
	}
}
