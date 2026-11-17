package compose

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
)

func TestMain(m *testing.M) {
	dockertest.Main()
	os.Exit(m.Run())
}

func TestInterpreterContextAndEnv(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"Name":"p-app-1","Service":"app","State":"running"}
{"Name":"p-db-1","Service":"db","State":"exited"}
`)
	c := &Interpreter{Project: "p", Dir: t.TempDir(), Service: "app", Docker: f.Binary, Context: "orbstack", Env: f.Env}
	if got := c.DockerArgs(true, "-v"); !reflect.DeepEqual(got[:3], []string{"--context", "orbstack", "compose"}) {
		t.Fatalf("args %v", got)
	}
	if cl := c.Client(); cl.Binary != f.Binary || cl.Context != "orbstack" {
		t.Fatalf("client %+v", cl)
	}
	ctx := context.Background()
	v, err := c.Version(ctx)
	if err != nil || v != "8.3.1" {
		t.Fatalf("version %q %v", v, err)
	}
	if !f.Called("--context orbstack compose", "ps --all --format json") || !f.Called("--context orbstack compose", "exec -T app php") {
		t.Fatalf("calls %v", f.Calls())
	}
	state, containers, err := c.ServiceState(ctx)
	if err != nil || state != StateRunning || len(containers) != 1 || containers[0].Name != "p-app-1" {
		t.Fatalf("state %q %+v %v", state, containers, err)
	}
	db := *c
	db.Service = "db"
	if state, _, _ := db.ServiceState(ctx); state != StateStopped {
		t.Fatalf("db %q", state)
	}
	db.Service = "redis"
	if state, _, _ := db.ServiceState(ctx); state != StateMissing {
		t.Fatalf("redis %q", state)
	}
	cp := c.ExecOnly()
	cp.Env[0] = "X=1"
	if c.Env[0] == "X=1" {
		t.Fatal("ExecOnly shares Env")
	}
	f.Write(t, "ps.json", "garbage")
	if _, _, err := c.ServiceState(ctx); err == nil {
		t.Fatal("garbage ps accepted")
	}
}
