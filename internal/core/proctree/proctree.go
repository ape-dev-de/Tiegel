// Package proctree kills a child process together with everything it
// started. Package managers and language servers are often wrappers
// (npm.cmd, composer.bat, shell scripts) whose real work runs in a
// grandchild; killing only the direct child leaves that grandchild running
// and holding the output pipes.
//
// Usage: New before cmd.Start, Attach right after it, Kill to end the
// tree and Release once the process was waited for.
//
// Unix: the child gets its own process group (Setpgid); Kill signals the
// whole group. Windows: the child is assigned to a Job Object; Kill
// terminates the job, with "taskkill /T /F" as fallback when the job could
// not be set up.
package proctree

import (
	"os"
	"os/exec"
	"sync"
)

// Tree is the process tree of a started command.
type Tree struct {
	cmd *exec.Cmd

	mu       sync.Mutex
	sys      sysTree
	released bool
}

// New prepares cmd (before Start) so that its whole tree can be killed
// and returns the tree. Call Attach right after a successful Start.
func New(cmd *exec.Cmd) *Tree {
	prepare(cmd)
	return &Tree{cmd: cmd}
}

// Attach registers the started process. On Windows it assigns the process
// to a Job Object; failures are tolerated (Kill falls back to taskkill).
// Kill may run concurrently (e.g. exec's context watcher).
func (t *Tree) Attach() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd.Process != nil && !t.released {
		t.sys = attach(t.cmd.Process)
	}
}

// Kill ends the process and all of its descendants. It is safe to call
// more than once and after the process exited (the remaining descendants
// are still killed where the platform allows it).
func (t *Tree) Kill() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.released || t.cmd.Process == nil {
		return nil
	}
	return kill(t.cmd.Process, t.sys)
}

// Release frees the platform resources (the Windows job handle) after
// the process was waited for. It does not kill anything.
func (t *Tree) Release() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.released {
		t.released = true
		release(t.sys)
	}
}

// killProcess kills only p (last resort).
func killProcess(p *os.Process) error {
	if err := p.Kill(); err != nil && err != os.ErrProcessDone {
		return err
	}
	return nil
}
