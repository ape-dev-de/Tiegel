package docker

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/proctree"
)

// Line is one line of output of a streamed docker command.
type Line struct {
	// Stream is "stdout" or "stderr".
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// maxLine bounds a single output line (longer lines are split by the
// scanner's buffer limit and reported as an error).
const maxLine = 1 << 20

// Stream starts cmd and calls onLine for every line it writes to standard
// output or standard error, until it exits. cmd must be built with
// exec.CommandContext (e.g. Client.Command): cancelling that context kills
// the whole process tree (`docker compose` runs the compose plugin as a
// child process that would otherwise keep the pipes open). onLine is
// called from two goroutines but never concurrently. The error carries the
// last line of standard error when the command fails.
func Stream(cmd *exec.Cmd, onLine func(Line)) error {
	tree := proctree.New(cmd)
	cmd.Cancel = tree.Kill
	cmd.WaitDelay = 2 * time.Second
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		_ = outW.Close()
		_ = errW.Close()
		return fmt.Errorf("docker: start %s: %w", strings.Join(cmd.Args, " "), err)
	}
	tree.Attach()

	var (
		mu      sync.Mutex
		lastErr string
		wg      sync.WaitGroup
	)
	pump := func(stream string, r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), maxLine)
		for sc.Scan() {
			text := strings.TrimRight(sc.Text(), "\r")
			mu.Lock()
			if stream == "stderr" && strings.TrimSpace(text) != "" {
				lastErr = strings.TrimSpace(text)
			}
			if onLine != nil {
				onLine(Line{Stream: stream, Text: text})
			}
			mu.Unlock()
		}
		_, _ = io.Copy(io.Discard, r) // drain after a scanner error
	}
	wg.Add(2)
	go pump("stdout", outR)
	go pump("stderr", errR)
	err := cmd.Wait()
	tree.Release()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		return nil
	}
	if lastErr != "" {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(cmd.Args[1:], " "), err, lastErr)
	}
	return fmt.Errorf("docker %s: %w", strings.Join(cmd.Args[1:], " "), err)
}
