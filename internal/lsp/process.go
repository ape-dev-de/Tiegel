package lsp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
)

// process is a language server child process. It implements
// io.ReadWriteCloser over the server's stdout (read) and stdin (write).
//
// Plain os.Pipe pairs are used instead of exec's StdoutPipe so that reading
// may continue concurrently with cmd.Wait.
type process struct {
	cmd    *exec.Cmd
	stdout *os.File // read end of server stdout
	stdin  *os.File // write end of server stdin

	exited  chan struct{}
	mu      sync.Mutex
	waitErr error

	closeOnce sync.Once
}

func startProcess(command string, args []string, dir string, env []string, log *slog.Logger, onStderr func(string)) (*process, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: creating pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)
		return nil, fmt.Errorf("lsp: creating pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, fmt.Errorf("lsp: creating pipe: %w", err)
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		closeAll(inR, inW, outR, outW, errR, errW)
		return nil, fmt.Errorf("lsp: starting %s: %w", command, err)
	}
	// The child holds its own copies now.
	closeAll(inR, outW, errW)

	p := &process{cmd: cmd, stdout: outR, stdin: inW, exited: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(errR)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			log.Info("lsp: server stderr", "server", command, "line", sc.Text())
			if onStderr != nil {
				onStderr(sc.Text())
			}
		}
		_ = errR.Close()
	}()
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.waitErr = err
		p.mu.Unlock()
		close(p.exited)
		if err != nil {
			log.Warn("lsp: server exited", "server", command, "err", err)
		}
		// Make pending reads fail even if a grandchild keeps stdout open.
		p.close()
	}()
	return p, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

func (p *process) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *process) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Close closes the pipes; the process itself is left alone.
func (p *process) Close() error {
	p.close()
	return nil
}

func (p *process) close() {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
	})
}

func (p *process) exitStatus() (bool, error) {
	select {
	case <-p.exited:
		p.mu.Lock()
		defer p.mu.Unlock()
		return true, p.waitErr
	default:
		return false, nil
	}
}

func (p *process) pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *process) kill() {
	select {
	case <-p.exited:
		return
	default:
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return
	}
	<-p.exited
}

// wait waits for the process to exit; on ctx expiry it kills it.
func (p *process) wait(ctx context.Context) error {
	select {
	case <-p.exited:
		return nil
	case <-ctx.Done():
		p.kill()
		return fmt.Errorf("lsp: waiting for server exit: %w", ctx.Err())
	}
}
