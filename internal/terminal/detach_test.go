package terminal

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestProcCwd(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if procCwd(os.Getpid()) != "" {
			t.Fatal("procCwd without support")
		}
		t.Skip("no process cwd on " + runtime.GOOS)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := procCwd(os.Getpid())
	a, _ := filepath.EvalSymlinks(got)
	b, _ := filepath.EvalSymlinks(wd)
	if got == "" || a != b {
		t.Fatalf("procCwd = %q, want %q", got, wd)
	}
	if procCwd(0) != "" || procCwd(-1) != "" {
		t.Fatal("invalid pid")
	}
}

// TestDetachAdoptRealShell: a shell moved between Managers keeps its
// process (variables, directory) and the output written while detached.
func TestDetachAdoptRealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	var mu sync.Mutex
	var opened int
	var detachedOut bool
	a := &Manager{OnOutput: func(o Output) {
		mu.Lock()
		if strings.Contains(o.Data, "while-detached") {
			detachedOut = true
		}
		mu.Unlock()
	}}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := a.Open(Options{Command: Command{Program: "/bin/sh", Dir: dir, Env: Env(os.Environ(), "", []string{"PS1=$ "}), Cols: 60, Rows: 10}, Project: dir})
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.Open(Options{Command: Command{Program: "/bin/sh", Env: Env(os.Environ(), "", nil), Cols: 40, Rows: 5}, Project: "/elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.CloseAll()
	s, _ := a.Get(info.ID)
	ctx := context.Background()
	_ = s.Input(ctx, []byte("export FOO=bar; cd sub; echo ready\r"))
	eventually(t, "ready", func() bool { return strings.Contains(s.AllText(), "ready\n") })
	if wd, _ := filepath.EvalSymlinks(s.WorkingDir()); runtime.GOOS != "windows" && procCwd(info.PID) != "" && filepath.Base(wd) != "sub" {
		t.Fatalf("working dir %q", s.WorkingDir())
	}

	ss := a.Detach(func(i Info) bool { return i.Project == dir })
	if len(ss) != 1 || len(a.List()) != 1 || a.List()[0].ID != other.ID {
		t.Fatalf("detach: %d sessions, left %+v", len(ss), a.List())
	}
	_ = s.Input(ctx, []byte("echo while-detached\r"))
	eventually(t, "output while detached", func() bool { return strings.Contains(s.AllText(), "while-detached\n") })
	mu.Lock()
	if detachedOut {
		t.Errorf("detached session reported output")
	}
	mu.Unlock()

	b := &Manager{OnOpen: func(Info) { mu.Lock(); opened++; mu.Unlock() }}
	if _, err := b.Open(Options{Command: Command{Program: "/bin/sh", Env: Env(os.Environ(), "", nil), Cols: 40, Rows: 5}}); err != nil {
		t.Fatal(err)
	}
	defer b.CloseAll()
	infos := b.Adopt(ss)
	if len(infos) != 1 || !infos[0].Running || len(b.List()) != 2 {
		t.Fatalf("adopt %+v / %+v", infos, b.List())
	}
	mu.Lock()
	if opened != 2 {
		t.Errorf("OnOpen %d times, want 2", opened)
	}
	mu.Unlock()
	if infos[0].ID == b.List()[0].ID {
		t.Fatalf("ID clash %s", infos[0].ID)
	}
	got, err := b.Get(infos[0].ID)
	if err != nil || got != s {
		t.Fatalf("get adopted: %v", err)
	}
	_ = s.Input(ctx, []byte("echo \"v=$FOO\"; pwd\r"))
	eventually(t, "variable kept", func() bool {
		txt := s.AllText()
		return strings.Contains(txt, "v=bar") && strings.Contains(txt, "/sub\n")
	})

	// A detached session that no Manager adopts is ended by Shutdown.
	ss = b.Detach(func(i Info) bool { return i.ID == infos[0].ID })
	if len(ss) != 1 {
		t.Fatal("detach again")
	}
	if err := ss[0].Shutdown(); err != nil {
		t.Fatal(err)
	}
	<-ss[0].Done()
}
