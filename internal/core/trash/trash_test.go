package trash

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMoveXDG(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Trash")
	for i := 0; i < 3; i++ {
		f := filepath.Join(root, "a b.php")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := moveXDG(dir, f); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("still there")
		}
	}
	for _, name := range []string{"a b.php", "a b.2.php", "a b.3.php"} {
		if _, err := os.Stat(filepath.Join(dir, "files", name)); err != nil {
			t.Errorf("trashed file %s: %v", name, err)
		}
		info, err := os.ReadFile(filepath.Join(dir, "info", name+".trashinfo"))
		if err != nil || !strings.Contains(string(info), "Path=") || !strings.Contains(string(info), "a%20b.php") || !strings.Contains(string(info), "DeletionDate=") {
			t.Errorf("info %s: %q %v", name, info, err)
		}
	}
	// A directory.
	d := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(d, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveXDG(dir, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "sub", "x")); err != nil {
		t.Fatal(err)
	}
	if err := moveXDG(dir, filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing file trashed")
	}
}

func TestHomeTrash(t *testing.T) {
	if d, _ := homeTrash("/data", "/home/u"); d != filepath.Join("/data", "Trash") {
		t.Fatal(d)
	}
	if d, _ := homeTrash("", "/home/u"); d != filepath.Join("/home/u", ".local", "share", "Trash") {
		t.Fatal(d)
	}
	if _, err := homeTrash("", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

// TestMoveReal uses the platform trash. On macOS it would put a file into
// the user's Trash, so it only runs with TIEGEL_TEST_TRASH=1 there.
func TestMoveReal(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		if os.Getenv("TIEGEL_TEST_TRASH") == "" {
			t.Skip("set TIEGEL_TEST_TRASH=1 to test the macOS trash")
		}
	case "windows":
		if err := Move(filepath.Join(t.TempDir(), "x")); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		return
	default:
		t.Setenv("XDG_DATA_HOME", t.TempDir())
	}
	f := filepath.Join(t.TempDir(), "tiegel-trash-test.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Move(f)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("not moved")
	}
	if err := Move(f); err == nil {
		t.Fatal("missing file trashed")
	}
}
