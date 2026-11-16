package headless

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Review 2026-11-16 #5: unsaved changes left at exit are listed by the
// recovery command at the next start and can be dismissed.
func TestRecoveryCommand(t *testing.T) {
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	env := testEnv(t, nil, io.Discard, io.Discard)
	a, err := api.New(env.Options)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := a.OpenProject(ctx, dir); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		m string
		p any
	}{
		{"doc.open", map[string]any{"path": "a.php"}},
		{"doc.replace", map[string]any{"path": "a.php", "text": "<?php // unsaved\n"}},
	} {
		if _, err := a.CallJSON(ctx, step.m, step.p); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.Close() // exits with unsaved changes

	rec := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := Run(ctx, append([]string{"recovery", "--project", dir}, args...), Env{Stdout: &out, Stderr: io.Discard, Getenv: func(string) string { return "" }, Options: env.Options})
		return code, out.String()
	}
	code, out := rec()
	if code != ExitOK || !strings.Contains(out, "a.php") || !strings.Contains(out, "unsaved on exit") {
		t.Fatalf("recovery %d %q", code, out)
	}
	if code, _ := rec("extra"); code == ExitOK {
		t.Fatal("arguments without --dismiss accepted")
	}
	if code, out = rec("--dismiss", "a.php"); code != ExitOK || !strings.Contains(out, "no recovery points") {
		t.Fatalf("dismiss %d %q", code, out)
	}
}
