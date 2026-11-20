package headless

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func realDir(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProjectsCommand(t *testing.T) {
	t.Parallel()
	a := realDir(t, project(t, map[string]string{"a.php": "<?php\n"}))
	b := realDir(t, project(t, map[string]string{"b.php": "<?php\n"}))

	if code, out, errs := run(t, "", "projects"); code != ExitOK || !strings.Contains(out, "no open projects") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	code, out, errs := run(t, "", "projects", "list", "--project", a, "--json")
	var list api.ProjectsListResult
	if code != ExitOK || json.Unmarshal([]byte(out), &list) != nil || list.Active != a || len(list.Projects) != 1 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if code, out, errs := run(t, "", "projects", "open", b, "--project", a); code != ExitOK || !strings.Contains(out, "active") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	for _, args := range [][]string{
		{"projects", "switch", a},
		{"projects", "hibernate", a},
		{"projects", "hibernate", a, "--force"},
		{"projects", "resume", a},
		{"projects", "busy", a},
		{"projects", "close", a, "--force"},
	} {
		if code, out, errs := run(t, "", append(args, "--project", a)...); code != ExitOK || out == "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errs)
		}
	}
	for _, args := range [][]string{
		{"projects", "open"},
		{"projects", "list", "x"},
		{"projects", "frobnicate", "x"},
		{"projects", "open", b, "--force"},
	} {
		if code, _, _ := run(t, "", args...); code != ExitUsage {
			t.Fatalf("%v: %d", args, code)
		}
	}
	if code, _, errs := run(t, "", "projects", "switch", "nope"); code != ExitError || !strings.Contains(errs, "not open") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestServeProjects(t *testing.T) {
	t.Parallel()
	a := realDir(t, project(t, map[string]string{"a.php": "<?php\n"}))
	b := realDir(t, project(t, map[string]string{"b.php": "<?php\n"}))
	in := `{"jsonrpc":"2.0","id":1,"method":"projects.open","params":{"path":` + quoteJSON(b) + `}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"project.info"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"projects.switch","params":{"name":` + quoteJSON(a) + `}}` + "\n" +
		`{"jsonrpc":"2.0","id":4,"method":"projects.list"}` + "\n"
	code, out, errs := run(t, in, "serve", "--project", a)
	if code != ExitOK || strings.Count(out, `"result"`) != 4 {
		t.Fatalf("serve %d %q %q", code, out, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[1], `"root":`+quoteJSON(b)) {
		t.Fatalf("project.info not forwarded: %s", lines[1])
	}
	if !strings.Contains(lines[3], `"active":`+quoteJSON(a)) {
		t.Fatalf("list: %s", lines[3])
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
