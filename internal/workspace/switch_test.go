package workspace

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// hbProject blocks hibernation less than closing (HibernateBlocker).
type hbProject struct {
	*fakeProject
	hbusy []string
}

func (p *hbProject) HibernateBusy(context.Context) []string { return p.hbusy }

// D547: switching with HibernatePrevious hibernates the project left
// unless work blocks it; then it stays in the background with reasons.
func TestSwitchHibernatesPrevious(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	hib := SwitchOptions{HibernatePrevious: true}
	if i, err := e.m.OpenWith(ctx, root("a"), hib); err != nil || i.Previous != "" {
		t.Fatalf("first open %+v %v", i, err)
	}
	i, err := e.m.OpenWith(ctx, root("b"), hib)
	if err != nil || i.State != StateActive || i.Previous != root("a") || i.PreviousState != StateHibernated || len(i.PreviousBusy) != 0 {
		t.Fatalf("open b %+v %v", i, err)
	}
	if got := states(e.m); got != "a=hibernated b=active " {
		t.Fatal(got)
	}
	ev := e.takeEvents(t)
	if ev[len(ev)-1] != "projects.hibernated a switch" {
		t.Fatalf("events %q", ev)
	}
	// Busy: b stays in the background.
	e.proj(root("b")).mu.Lock()
	e.proj(root("b")).busy = []string{"run: server"}
	e.proj(root("b")).mu.Unlock()
	i, err = e.m.SwitchWith(ctx, "a", hib)
	if err != nil || i.PreviousState != StateBackground || fmt.Sprint(i.PreviousBusy) != "[run: server]" {
		t.Fatalf("switch a %+v %v", i, err)
	}
	if got := states(e.m); got != "a=active b=background " {
		t.Fatal(got)
	}
	// Unsaved documents keep it running too.
	e.proj(root("b")).mu.Lock()
	e.proj(root("b")).busy = nil
	e.proj(root("b")).mu.Unlock()
	e.proj(root("a")).mu.Lock()
	e.proj(root("a")).unsaved = true
	e.proj(root("a")).mu.Unlock()
	if i, err = e.m.SwitchWith(ctx, "b", hib); err != nil || fmt.Sprint(i.PreviousBusy) != "[unsaved changes]" {
		t.Fatalf("unsaved %+v %v", i, err)
	}
	// Without the option the previous project stays in the background.
	if i, err = e.m.Switch(ctx, "a"); err != nil || i.Previous != "" {
		t.Fatalf("plain switch %+v %v", i, err)
	}
	if got := states(e.m); got != "a=active b=background " {
		t.Fatal(got)
	}
}

func TestHibernateBlocker(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	p := &hbProject{fakeProject: &fakeProject{root: root("c"), running: true, busy: []string{"terminal: vim"}}}
	if _, err := e.m.Add(ctx, root("c"), p, false); err != nil {
		t.Fatal(err)
	}
	if b, err := e.m.Busy(ctx, "c"); err != nil || len(b) != 1 {
		t.Fatalf("busy %v %v", b, err)
	}
	if b, err := e.m.HibernateBusy(ctx, "c"); err != nil || len(b) != 0 {
		t.Fatalf("hibernate busy %v %v", b, err)
	}
	if _, err := e.m.HibernateBusy(ctx, "nope"); err == nil {
		t.Fatal("unknown project")
	}
	if i, err := e.m.Hibernate(ctx, "c", HibernateOptions{}); err != nil || i.State != StateHibernated {
		t.Fatalf("hibernate %+v %v", i, err)
	}
	p.hbusy = []string{"run: x"}
	if _, err := e.m.Resume(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Hibernate(ctx, "c", HibernateOptions{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("hibernate blocked: %v", err)
	}
}
