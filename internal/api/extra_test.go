package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/version"
)

func TestAppVersionAndUpdateCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			_, _ = w.Write([]byte(`[]`))
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(`{"tag_name":"v99.0.0","html_url":"https://example.org/r","body":"notes"}`))
		}
	}))
	defer srv.Close()
	a := newTestApp(t, func(a *App) error { a.opts.UpdateSource = srv.URL + "/latest"; return nil })

	var vi VersionInfo
	call(t, a, "app.version", nil, &vi)
	if vi.Version != version.Version || vi.String == "" || vi.APIVersion != Version || vi.Dev != version.IsDev() {
		t.Fatalf("version %+v", vi)
	}
	var res UpdateCheckResult
	call(t, a, "update.check", nil, &res)
	if !res.Available || res.Latest != "99.0.0" || res.URL != "https://example.org/r" || res.Notes != "notes" || res.Source != srv.URL+"/latest" {
		t.Fatalf("update %+v", res)
	}
	wantCode(t, a, "update.check", UpdateCheckParams{Source: srv.URL + "/empty"}, CodeNotFound)
	wantCode(t, a, "update.check", UpdateCheckParams{Source: srv.URL + "/broken"}, CodeUnavailable)
}

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGit(t *testing.T) {
	gitAvailable(t)
	a := newTestApp(t)
	wantCode(t, a, "git.status", nil, CodeNoProject)
	openProject(t, a, map[string]string{"x": "1"})
	wantCode(t, a, "git.status", nil, CodeNotFound)

	root := openProject(t, a, map[string]string{"a.php": "<?php\n"})
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "user.email", "test@example.org")
	runGit(t, root, "config", "commit.gpgsign", "false")

	var st GitStatus
	call(t, a, "git.status", nil, &st)
	if st.Branch != "main" || !st.Initial || len(st.Files) != 1 || st.Files[0].Path != "a.php" || st.Files[0].Index != "?" {
		t.Fatalf("status %+v", st)
	}
	wantCode(t, a, "git.commit", GitCommitParams{Stage: []string{"a.php"}}, CodeInvalidParams)

	// An unsaved edit is saved before committing.
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 2}, Text: "echo 1;\n"}, nil)
	var cr GitCommitResult
	call(t, a, "git.commit", GitCommitParams{Message: "initial", Stage: []string{"a.php"}}, &cr)
	if len(cr.Hash) < 7 {
		t.Fatalf("commit %+v", cr)
	}
	call(t, a, "git.status", nil, &st)
	if len(st.Files) != 0 || st.Initial {
		t.Fatalf("status after commit %+v", st)
	}

	writeFile(t, filepath.Join(root, "a.php"), "<?php\necho 2;\n")
	var diff GitDiffResult
	call(t, a, "git.diff", GitDiffParams{Base: "HEAD"}, &diff)
	if len(diff.Files) != 1 || diff.Files[0].Added != 1 || diff.Files[0].Removed != 1 || len(diff.Files[0].Hunks) != 1 {
		t.Fatalf("diff %+v", diff)
	}
	call(t, a, "git.diff", GitDiffParams{Base: "HEAD", Raw: true}, &diff)
	if diff.Raw == "" {
		t.Fatal("raw diff empty")
	}
	var log []GitCommit
	call(t, a, "git.log", nil, &log)
	if len(log) != 1 || log[0].Subject != "initial" || log[0].Author != "Test" {
		t.Fatalf("log %+v", log)
	}
	var branches []GitBranch
	call(t, a, "git.branches", nil, &branches)
	if len(branches) != 1 || !branches[0].Current || branches[0].Name != "main" {
		t.Fatalf("branches %+v", branches)
	}
	wantCode(t, a, "git.log", GitLogParams{Rev: "no-such-rev"}, CodeFailed)
	wantCode(t, a, "git.diff", GitDiffParams{Base: "no-such-rev"}, CodeFailed)
	wantCode(t, a, "git.diff", GitDiffParams{Base: "no-such-rev", Raw: true}, CodeFailed)
}

func TestPHPVersionAndCheck(t *testing.T) {
	a := newTestApp(t)
	wantCode(t, a, "php.version", PHPVersionParams{NoInterpreter: true}, CodeNotFound)
	openProject(t, a, map[string]string{
		"composer.json": `{"require":{"php":"^8.0"}}`,
		"enum.php":      "<?php\nenum Suit { case Hearts; }\n",
	})
	var pv PHPVersionResult
	call(t, a, "php.version", nil, &pv)
	if pv.Short != "8.0" || pv.Source == "" {
		t.Fatalf("version %+v", pv)
	}
	call(t, a, "php.version", PHPVersionParams{Override: "8.3"}, &pv)
	if pv.Version != "8.3.0" {
		t.Fatalf("override %+v", pv)
	}
	wantCode(t, a, "php.version", PHPVersionParams{Override: "x"}, CodeInvalidParams)
	wantCode(t, a, "php.version", PHPVersionParams{Interpreter: "nope"}, CodeNotFound)

	var res PHPCheckResult
	call(t, a, "php.check", PHPCheckParams{Path: "enum.php"}, &res)
	if res.Version != "8.0.0" || len(res.Diagnostics) == 0 {
		t.Fatalf("check %+v", res)
	}
	d := res.Diagnostics[0]
	if d.Line != 2 || d.Col < 1 || d.Severity == "" || d.Feature == "" {
		t.Fatalf("diagnostic %+v", d)
	}
	call(t, a, "php.check", PHPCheckParams{Path: "enum.php", Version: "8.1"}, &res)
	if len(res.Diagnostics) != 0 || res.Source != "param" {
		t.Fatalf("8.1 %+v", res)
	}
	// The open (unsaved) document is checked, not the disk content.
	call(t, a, "doc.open", DocOpenParams{Path: "enum.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "enum.php", Text: "<?php\necho 1;\n"}, nil)
	call(t, a, "php.check", PHPCheckParams{Path: "enum.php"}, &res)
	if len(res.Diagnostics) != 0 {
		t.Fatalf("open doc %+v", res)
	}
	wantCode(t, a, "php.check", PHPCheckParams{Path: "missing.php"}, CodeNotFound)
	wantCode(t, a, "php.check", PHPCheckParams{Path: "enum.php", Version: "x"}, CodeInvalidParams)
	wantCode(t, a, "php.check", PHPCheckParams{}, CodeInvalidParams)
}
