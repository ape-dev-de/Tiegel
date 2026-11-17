//go:build !unix && !windows

package proctree

import (
	"os"
	"os/exec"
)

type sysTree struct{}

func prepare(*exec.Cmd) {}

func attach(*os.Process) sysTree { return sysTree{} }

func kill(p *os.Process, _ sysTree) error { return killProcess(p) }

func release(sysTree) {}
