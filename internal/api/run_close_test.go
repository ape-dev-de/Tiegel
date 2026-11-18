package api

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// TestRunConfigsConcurrentSave: concurrent saves and deletes of run
// configurations are not lost (read-modify-write under a lock).
func TestRunConfigsConcurrentSave(t *testing.T) {
	a := newTestApp(t)
	call(t, a, "project.open", PathParams{Path: t.TempDir()}, nil)
	var victim run.Config
	call(t, a, "run.configs.save", run.Config{Name: "victim", Type: run.TypeCommand, Values: map[string]any{"command": "x"}}, &victim)
	const n = 16
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := run.Config{Name: fmt.Sprintf("c%d", i), Type: run.TypeCommand, Values: map[string]any{"command": "echo"}}
			if err := tryCall(a, "run.configs.save", c, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := tryCall(a, "run.configs.delete", RunConfigIDParams{ID: victim.ID}, nil); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	list, _, err := settings.Lookup[[]run.Config](a.ProjectSettings(), run.KeyConfigurations)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range list {
		names[c.Name] = true
	}
	if len(list) != n || names["victim"] {
		t.Fatalf("%d configurations stored (want %d, without victim): %v", len(list), n, names)
	}
}

// TestRunCloseDecision: the choice of the close confirmation applies to
// the runs it was made for, even after the project changed, and "keep"
// survives closing the App with policy ask.
func TestRunCloseDecision(t *testing.T) {
	a := newTestApp(t)
	self := selfExe(t)
	sleep := &run.Spec{Name: "sleeper", Program: self, Env: []string{runHelperEnv + "=sleep"}}
	rootA := t.TempDir()
	call(t, a, "project.open", PathParams{Path: rootA}, nil)
	rootA = a.ProjectRoot()
	var inA, kept run.Info
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &inA)
	waitOutput(t, a, inA.ID, "ready")
	var check RunCloseCheck
	call(t, a, "run.closeCheck", nil, &check)
	if check.Project != rootA || len(check.Active) != 1 {
		t.Fatalf("check %+v", check)
	}

	// "Terminate" for project A after B was opened stops A's run.
	rootB := filepath.Join(t.TempDir(), "b")
	writeFile(t, filepath.Join(rootB, "x.php"), "<?php\n")
	call(t, a, "project.open", PathParams{Path: rootB}, nil)
	var ids []string
	call(t, a, "run.stopAll", RunStopAllParams{Project: check.Project, Wait: true}, &ids)
	if len(ids) != 1 || ids[0] != inA.ID {
		t.Fatalf("stopAll for A: %v", ids)
	}
	if i, _ := a.runs.Get(inA.ID); i.State != run.StateStopped {
		t.Fatalf("wait did not wait: %+v", i)
	}

	// "Keep Running" (policy ask): closing the App keeps the run.
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &kept)
	waitOutput(t, a, kept.ID, "ready")
	var other run.Info
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &other)
	waitOutput(t, a, other.ID, "ready")
	call(t, a, "run.keepAll", RunStopAllParams{}, &ids)
	if len(ids) != 2 {
		t.Fatalf("kept %v", ids)
	}
	// A run started later is not kept.
	var later run.Info
	call(t, a, "run.start", RunStartParams{Spec: sleep}, &later)
	waitOutput(t, a, later.ID, "ready")
	a.closeRuns()
	if waitRun(t, a, later.ID).State != run.StateStopped {
		t.Fatal("unkept run not stopped on close")
	}
	for _, id := range []string{kept.ID, other.ID} {
		if i, _ := a.runs.Get(id); !i.Active() || !i.Keep {
			t.Fatalf("kept run stopped on close: %+v", i)
		}
	}
	call(t, a, "run.stopAll", nil, &ids)
	waitRun(t, a, kept.ID)
	waitRun(t, a, other.ID)
}
