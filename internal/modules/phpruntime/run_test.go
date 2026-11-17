package phpruntime

import (
	"context"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestRunInterpreterHook(t *testing.T) {
	bin := t.TempDir()
	modtest.FakePHP(t, bin, "php", "8.3.4", "")
	a, _ := newApp(t, []string{bin}, Options{Docker: "docker"})
	ask := func(id string) api.RunInterpreterQuery {
		t.Helper()
		q, err := api.HookRunInterpreter.Apply(context.Background(), a.Hooks(), api.RunInterpreterQuery{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	// Nothing selected: the first local php.
	if q := ask(""); q.Err != "" {
		t.Fatalf("no selection: %+v", q)
	} else if _, ok := q.Interpreter.(*interp.Local); !ok {
		t.Fatalf("no selection: %T", q.Interpreter)
	}
	modtest.Project(t, a, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n",
	})
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	var composeID string
	for _, i := range list {
		if i.Kind == "compose" {
			composeID = i.ID
		}
	}
	if composeID == "" {
		t.Fatalf("no compose interpreter in %+v", list)
	}
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: composeID}, nil)
	q := ask("")
	if _, ok := q.Interpreter.(*compose.Interpreter); !ok || len(q.Mappings) != 1 || q.Mappings[0].Remote != "/var/www/html" {
		t.Fatalf("selected compose: %T %+v", q.Interpreter, q)
	}
	// Another interpreter by ID: no mappings (the interpreter's own apply).
	if q := ask(list[0].ID); q.Interpreter == nil || q.Mappings != nil {
		t.Fatalf("by id: %+v", q)
	}
	if q := ask("nope"); q.Interpreter != nil || q.Err == "" {
		t.Fatalf("unknown: %+v", q)
	}
	// An answer of another provider is kept.
	pre := interp.NewLocal("/x/php")
	if q, _ := api.HookRunInterpreter.Apply(context.Background(), a.Hooks(), api.RunInterpreterQuery{Interpreter: pre}); q.Interpreter != pre {
		t.Fatal("answer replaced")
	}

	// Without any php: an explanation.
	b, _ := newApp(t, []string{t.TempDir()}, Options{})
	if q, _ := api.HookRunInterpreter.Apply(context.Background(), b.Hooks(), api.RunInterpreterQuery{}); q.Interpreter != nil || q.Err == "" {
		t.Fatalf("no php: %+v", q)
	}
}
