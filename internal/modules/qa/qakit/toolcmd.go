package qakit

import "github.com/ape-dev-de/tiegel/internal/run"

// toolCmd is the command of a tool run: Program with Args, or with PHP
// the php of the target running Args (the first being the script).
type toolCmd = run.BatchCommand

// hostFS is what toolCommand needs from the host: PATH lookup and file
// inspection (fakes in tests).
type hostFS = run.BatchFS

// toolCommand builds the command running program with args in root.
// Processes started directly on the host (hostSide) of a Windows system
// never pass repository file names to batch files (D362): the shared
// rules are run.WindowsBatchSafe, which External Tools use as well
// (D461). Elsewhere program runs unchanged.
func toolCommand(goos string, hostSide bool, root, program string, args []string, fsys hostFS) (toolCmd, error) {
	if !hostSide {
		return toolCmd{Program: program, Args: args}, nil
	}
	return run.WindowsBatchSafe(goos, root, program, args, fsys)
}
