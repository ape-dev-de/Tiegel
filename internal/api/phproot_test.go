package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

func TestPHPRoot(t *testing.T) {
	a := newTestApp(t)
	wantCode(t, a, "project.phpRoot", nil, CodeNoProject)
	if a.PHPRoot() != "" {
		t.Fatal("php root without project")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("project/composer.json", `{"autoload":{"psr-4":{"App\\":"src/"}}}`)
	write("project/vendor/autoload.php", "<?php\n")
	write("project/src/A.php", "<?php\n")
	write("tools/composer.json", `{}`)
	var mu sync.Mutex
	var events []PHPRootInfo
	a.bus.Subscribe(TopicPHPRootChanged, func(e bus.Event) {
		if info, ok := e.Payload.(PHPRootInfo); ok {
			mu.Lock()
			events = append(events, info)
			mu.Unlock()
		}
	})
	last := func() PHPRootInfo {
		mu.Lock()
		defer mu.Unlock()
		if len(events) == 0 {
			return PHPRootInfo{}
		}
		return events[len(events)-1]
	}
	drain := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.bus.Drain(ctx)
	}

	call(t, a, "project.open", PathParams{Path: root}, nil)
	var info PHPRootInfo
	call(t, a, "project.phpRoot", nil, &info)
	if info.Path != "project" || !info.Auto || info.Abs != filepath.Join(root, "project") || len(info.Candidates) != 2 ||
		!info.Candidates[0].Recommended || info.Candidates[1].Path != "tools" {
		t.Fatalf("detected %+v", info)
	}
	if a.PHPRoot() != filepath.Join(root, "project") {
		t.Fatalf("PHPRoot %s", a.PHPRoot())
	}
	var pi ProjectInfo
	call(t, a, "project.info", nil, &pi)
	if pi.PHPRoot != "project" || !pi.Composer {
		t.Fatalf("project.info %+v", pi)
	}
	// Roles come from project/composer.json, relative to the project.
	var roles ProjectRolesResult
	call(t, a, "project.roles", nil, &roles)
	if len(roles.Roots) != 2 || roles.Roots[0].Path != "project/src" || roles.Roots[1].Path != "project/vendor" {
		t.Fatalf("roles %+v", roles.Roots)
	}
	// The namespace of a new class comes from project/composer.json.
	var ns NamespaceResult
	call(t, a, "files.phpNamespace", PathParams{Path: "project/src"}, &ns)
	if ns.Namespace != "App" || !ns.Found {
		t.Fatalf("namespace %+v", ns)
	}

	// The setting decides and publishes the change.
	call(t, a, "settings.set", SettingsSetParams{Key: settings.KeyPHPRoot, Scope: "project", Value: json.RawMessage(`"tools"`)}, nil)
	call(t, a, "project.phpRoot", nil, &info)
	drain()
	if info.Path != "tools" || info.Auto || info.Setting != "tools" || last().Path != "tools" {
		t.Fatalf("setting %+v, event %+v", info, last())
	}
	call(t, a, "settings.set", SettingsSetParams{Key: settings.KeyPHPRoot, Scope: "project"}, nil)
	drain()
	if last().Path != "project" || !last().Auto {
		t.Fatalf("reset event %+v", last())
	}

	// A composer.json appearing at the top wins (file change event).
	write("composer.json", `{}`)
	a.bus.Publish(TopicFilesChanged, FilesChangedEvent{Changes: []FileChange{{Kind: FileCreated, Path: "composer.json", File: filepath.Join(root, "composer.json")}}})
	drain()
	if last().Path != "." || a.PHPRoot() != root {
		t.Fatalf("root composer.json: %+v %s", last(), a.PHPRoot())
	}
	n := len(events)
	a.bus.Publish(TopicFilesChanged, FilesChangedEvent{Changes: []FileChange{{Kind: FileModified, Path: "project/src/A.php"}}})
	a.bus.Publish(TopicFilesChanged, FilesChangedEvent{Changes: []FileChange{{Kind: FileModified, Path: "composer.json"}}})
	drain()
	mu.Lock()
	if len(events) != n {
		t.Fatalf("event without change: %+v", events[n:])
	}
	mu.Unlock()
	call(t, a, "project.close", nil, nil)
	if a.PHPRoot() != "" {
		t.Fatal("php root after close")
	}
}

func TestPHPRootRelevant(t *testing.T) {
	for p, want := range map[string]bool{
		"composer.json": true, "a/b/composer.lock": true, "a/vendor": true, "a/phpunit.xml.dist": true,
		"a/b/c/composer.json": false, "src/A.php": false, "": false,
	} {
		if got := phpRootRelevant(p); got != want {
			t.Errorf("%q: %v", p, got)
		}
	}
}
