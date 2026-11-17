//go:build windows

package docker

import (
	"os/exec"
	"strings"
	"syscall"

	dk "github.com/ape-dev-de/tiegel/internal/docker"
)

// createNewConsole is CREATE_NEW_CONSOLE of CreateProcess.
const createNewConsole = 0x00000010

// prepareDetached passes a cmd.exe command of dk.TerminalCommand
// verbatim (its elements are already quoted for cmd.exe; Go's
// per-argument quoting would break them) and opens it in a new console
// (D307).
func prepareDetached(cmd *exec.Cmd, argv []string) {
	if !dk.WindowsRawCmd(argv) {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       strings.Join(argv, " "),
		CreationFlags: createNewConsole,
	}
}
