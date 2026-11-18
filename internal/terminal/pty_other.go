//go:build !unix && !windows

package terminal

// StartPTY is not available on this platform.
func StartPTY(Command) (Process, error) { return nil, ErrUnsupported }
