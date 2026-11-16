package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.txt")
	if err := Write(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "two" {
		t.Fatalf("%q %v", b, err)
	}
	fi, _ := os.Stat(p)
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("perm not kept: %v", fi.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
}

func TestWriteErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(filepath.Join(blocker, "x"), nil, 0o600); err == nil {
		t.Fatal("expected error when parent is a file")
	}
	// Target is a non-empty directory: the rename fails.
	target := filepath.Join(dir, "d")
	if err := os.MkdirAll(filepath.Join(target, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(target, []byte("x"), 0o600); err == nil {
		t.Fatal("expected rename error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("temp file not cleaned up: %v", entries)
	}
}
