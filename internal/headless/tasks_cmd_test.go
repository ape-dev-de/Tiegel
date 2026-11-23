package headless

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/tasks"
)

func TestTasksCommand(t *testing.T) {
	t.Parallel()
	code, out, _ := run(t, "", "tasks")
	if code != ExitOK || !strings.Contains(out, "no background tasks") {
		t.Fatalf("tasks %d %q", code, out)
	}
	code, out, _ = run(t, "", "tasks", "--json")
	if code != ExitOK || strings.TrimSpace(out) != "[]" {
		t.Fatalf("tasks --json %d %q", code, out)
	}
	if code, _, _ := run(t, "", "tasks", "cancel", "t99"); code != ExitError {
		t.Fatalf("cancel unknown: %d", code)
	}
	if code, _, _ := run(t, "", "tasks", "bogus"); code != ExitUsage {
		t.Fatalf("usage: %d", code)
	}
}

func TestPrintTasks(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	printTasks(&b, []tasks.Info{
		{ID: "t1", Title: "Indexing PHP files", Module: "php-index", Done: 3, Total: 10, Cancellable: true, Cancelling: true, StartedAt: time.Now()},
		{ID: "t2", Title: "Pushing", Module: "git", Text: "origin", Error: "rejected", StartedAt: time.Now()},
	})
	out := b.String()
	for _, want := range []string{"t1", "3/10", "php-index", "cancellable,cancelling", "t2", "…", "failed: rejected"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}
