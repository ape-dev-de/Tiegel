package docker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
)

// TestImageInterpreterVersionLabelRace: the interpreter is cached and
// shared, so Version (writer) and Label/CachedVersion (readers) run
// concurrently; run with -race.
func TestImageInterpreterVersionLabelRace(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "images", "php:8.3-cli\n")
	d := docker.NewImageInterpreter("php:8.3-cli", "", "", client(f))
	np := d.WithoutPull() // copies share the version cache
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, i := range []*docker.ImageInterpreter{d, np, d} {
			if _, err := i.Version(context.Background()); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for {
		_ = d.Label()
		_ = d.CachedVersion()
		select {
		case <-done:
			if d.CachedVersion() != "8.3.1" || !strings.HasPrefix(d.Label(), "PHP 8.3.1 ") {
				t.Fatalf("version not shared: %q %q", d.CachedVersion(), d.Label())
			}
			return
		default:
		}
	}
}

func TestImageInterpreterSeedVersion(t *testing.T) {
	bare := &docker.ImageInterpreter{Image: "php", PHPVersion: "8.2.0"}
	if bare.CachedVersion() != "8.2.0" || bare.Label() != "PHP 8.2.0 (docker php)" {
		t.Fatalf("seed %q %q", bare.CachedVersion(), bare.Label())
	}
}
