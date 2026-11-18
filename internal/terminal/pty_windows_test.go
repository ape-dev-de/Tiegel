//go:build windows

package terminal

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestResizeAfterClose: the pseudo console handle is freed by Close;
// Resize and Kill afterwards must not touch it.
func TestResizeAfterClose(t *testing.T) {
	cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	p, err := StartPTY(Command{Program: cmd, Args: []string{"/c", "exit 0"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Skip("no ConPTY: ", err)
	}
	go func() { _, _ = io.Copy(io.Discard, p) }()
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(100, 30); err != nil {
		t.Fatalf("resize before close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(90, 20); !errors.Is(err, errClosed) {
		t.Fatalf("resize after close: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("kill after close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
